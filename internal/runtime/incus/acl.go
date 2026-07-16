// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/networkpolicy"
)

const (
	// ManagedBridgeGateway is the stable gateway address for the OpenBox bridge.
	ManagedBridgeGateway = "10.42.0.1/24"

	// ManagedBridgeGatewayHost is the host address within ManagedBridgeGateway.
	ManagedBridgeGatewayHost = "10.42.0.1"

	// DefaultDenyACLName is attached to every managed instance NIC.
	DefaultDenyACLName = "openbox-default-deny"

	// StandardEgressACLName permits public-internet egress for standard instances.
	StandardEgressACLName = "openbox-egress-standard"

	// LLMGatewayPort is a bridge-gateway placeholder until the management
	// gateway is assigned its final location in a later slice.
	LLMGatewayPort = "18789"
)

type networkACLRule struct {
	Action          string `json:"action"`
	Source          string `json:"source,omitempty"`
	Destination     string `json:"destination,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
	DestinationPort string `json:"destination_port,omitempty"`
	Description     string `json:"description,omitempty"`
	// State is required by Incus 7+ (enabled|disabled|logged).
	State string `json:"state,omitempty"`
}

func enabledACLRule(rule networkACLRule) networkACLRule {
	rule.State = "enabled"
	return rule
}

func networkACLResource() resource {
	return resource{
		Name:        DefaultDenyACLName,
		Description: "OpenBox default-deny instance network policy",
		Config:      managedConfig("network-acl", nil),
		Egress: []networkACLRule{
			enabledACLRule(networkACLRule{
				Action: "allow", Destination: ManagedBridgeGatewayHost, Protocol: "udp", DestinationPort: "53",
				Description: "allow DNS UDP to managed bridge gateway",
			}),
			enabledACLRule(networkACLRule{
				Action: "allow", Destination: ManagedBridgeGatewayHost, Protocol: "tcp", DestinationPort: "53",
				Description: "allow DNS TCP to managed bridge gateway",
			}),
			enabledACLRule(networkACLRule{
				Action: "allow", Destination: ManagedBridgeGatewayHost, Protocol: "tcp", DestinationPort: LLMGatewayPort,
				Description: "allow LLM Gateway management placeholder",
			}),
		},
		Ingress: []networkACLRule{
			enabledACLRule(networkACLRule{
				Action: "allow", Source: ManagedBridgeGatewayHost, Protocol: "tcp", DestinationPort: "22",
				Description: "allow OpenBox SSH gateway access",
			}),
		},
	}
}

func standardEgressACLResource() resource {
	egress := make([]networkACLRule, 0, len(managedBridgePeerCIDRs())+1)
	for _, peerCIDR := range managedBridgePeerCIDRs() {
		egress = append(egress, enabledACLRule(networkACLRule{
			Action: "reject", Destination: peerCIDR,
			Description: "deny peer instances on managed bridge",
		}))
	}
	egress = append(egress, enabledACLRule(networkACLRule{
		Action: "allow", Destination: "0.0.0.0/0",
		Description: "allow public internet egress",
	}))

	return resource{
		Name:        StandardEgressACLName,
		Description: "OpenBox standard instance egress policy",
		Config:      managedConfig("network-acl", nil),
		Egress:      egress,
		Ingress:     []networkACLRule{},
	}
}

// managedBridgePeerCIDRs covers 10.42.0.2 through 10.42.0.255 without
// covering the bridge gateway at 10.42.0.1.
func managedBridgePeerCIDRs() []string {
	return []string{
		"10.42.0.2/31",
		"10.42.0.4/30",
		"10.42.0.8/29",
		"10.42.0.16/28",
		"10.42.0.32/27",
		"10.42.0.64/26",
		"10.42.0.128/25",
	}
}

// RestrictedACL builds a named ACL for a restricted egress profile. Its
// destinations must first be validated by networkpolicy.ParseAllowedDestinations.
func RestrictedACL(name string, destinations []string) resource {
	rules := make([]networkACLRule, 0, len(destinations))
	for _, destination := range destinations {
		rules = append(rules, enabledACLRule(networkACLRule{
			Action: "allow", Destination: destination,
			Description: "allow administrator-approved destination",
		}))
	}
	return resource{
		Name:        name,
		Description: "OpenBox restricted instance egress policy",
		Config:      managedConfig("network-acl", nil),
		Egress:      rules,
		Ingress:     []networkACLRule{},
	}
}

// EnsureRestrictedACL reconciles a named restricted ACL for later
// instance/profile identity binding.
func (a *Adapter) EnsureRestrictedACL(ctx context.Context, name string, destinations []string) error {
	acl := RestrictedACL(name, destinations)
	return a.ensure(ctx, "network ACL", "/1.0/network-acls/"+url.PathEscape(acl.Name), "/1.0/network-acls", nil, acl)
}

// ApplyNetworkPolicy reconciles the instance NIC ACL stack after the runtime
// starts and before OpenBox reports the instance ready.
func (a *Adapter) ApplyNetworkPolicy(ctx context.Context, instance domain.Instance) error {
	if instance.RuntimeRef == "" {
		a.recordPolicyDenied(instance.ID)
		return fmt.Errorf("network policy runtime ref is required")
	}
	if err := a.setInstanceNICACLs(ctx, instance.RuntimeRef, NICACLs(instance.EgressMode)); err != nil {
		a.recordPolicyDenied(instance.ID)
		return err
	}
	if err := a.VerifyNetworkPolicy(ctx, instance); err != nil {
		a.recordPolicyDenied(instance.ID)
		return err
	}
	return nil
}

// VerifyNetworkPolicy checks that Incus reports the exact ACL stack OpenBox
// intends to enforce. A mismatch is an apply failure: callers must not report
// the instance ready while containment is uncertain.
func (a *Adapter) VerifyNetworkPolicy(ctx context.Context, instance domain.Instance) error {
	if instance.RuntimeRef == "" {
		return fmt.Errorf("network policy runtime ref is required")
	}
	actual, err := a.instanceNICACLs(ctx, instance.RuntimeRef)
	if err != nil {
		return err
	}
	expected := NICACLs(instance.EgressMode)
	if !sameStrings(actual, expected) {
		return fmt.Errorf("NIC ACL mismatch: got %q, want %q", actual, expected)
	}
	return nil
}

// NetworkPolicyStatus exposes the effective ACL composition and best-effort
// denied-flow counter. It intentionally excludes addresses and packet data.
func (a *Adapter) NetworkPolicyStatus(instance domain.Instance) domain.NetworkPolicyStatus {
	a.policyMu.RLock()
	deniedFlows := a.policyDenied[instance.ID]
	a.policyMu.RUnlock()
	return domain.NetworkPolicyStatus{
		EgressMode: instance.EgressMode,
		ACLs:       NICACLs(instance.EgressMode),
		Resolution: domain.AllowlistResolution{
			State:    "idle",
			Pending:  []string{},
			Resolved: []string{},
			Failed:   []string{},
		},
		DeniedFlows: deniedFlows,
	}
}

func (a *Adapter) recordPolicyDenied(instanceID domain.InstanceID) {
	a.policyMu.Lock()
	a.policyDenied[instanceID]++
	a.policyMu.Unlock()
}

// RemoveNetworkPolicy deletes the per-instance restricted ACL after the
// runtime resource is gone. Shared ACLs are intentionally retained.
func (a *Adapter) RemoveNetworkPolicy(ctx context.Context, instance domain.Instance) error {
	if instance.EgressMode != domain.EgressRestricted {
		return nil
	}
	err := a.request(ctx, http.MethodDelete, "/1.0/network-acls/"+url.PathEscape(networkpolicy.RestrictedACLName(string(instance.ID))), nil, nil, nil)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete restricted network ACL: %w", err)
	}
	return nil
}

func (a *Adapter) setInstanceNICACLs(ctx context.Context, ref string, acls []string) error {
	var instance instanceRecord
	query := url.Values{"project": {a.project}}
	if err := a.request(ctx, http.MethodGet, "/1.0/instances/"+url.PathEscape(ref), query, nil, &instance); err != nil {
		return fmt.Errorf("inspect Incus instance NIC: %w", err)
	}
	updated := cloneInstanceDevices(instance.Devices)
	eth0, ok := updated["eth0"]
	if !ok {
		eth0, ok = instance.ExpandedDevices["eth0"]
		if !ok || eth0["type"] != "nic" {
			return fmt.Errorf("managed instance NIC eth0 is missing")
		}
		updated["eth0"] = cloneDeviceMap(eth0)
	}
	if eth0["type"] != "nic" {
		return fmt.Errorf("managed instance NIC eth0 is missing")
	}
	updated["eth0"]["security.acls"] = strings.Join(acls, ",")
	if err := a.request(ctx, http.MethodPatch, "/1.0/instances/"+url.PathEscape(ref), query, struct {
		Devices map[string]map[string]string `json:"devices"`
	}{Devices: updated}, nil); err != nil {
		return fmt.Errorf("update Incus instance NIC ACLs: %w", err)
	}
	return nil
}

func (a *Adapter) instanceNICACLs(ctx context.Context, ref string) ([]string, error) {
	var instance instanceRecord
	query := url.Values{"project": {a.project}}
	if err := a.request(ctx, http.MethodGet, "/1.0/instances/"+url.PathEscape(ref), query, nil, &instance); err != nil {
		return nil, fmt.Errorf("inspect Incus instance NIC: %w", err)
	}
	eth0, ok := instance.Devices["eth0"]
	if !ok {
		eth0, ok = instance.ExpandedDevices["eth0"]
	}
	if !ok || eth0["type"] != "nic" {
		return nil, fmt.Errorf("managed instance NIC eth0 is missing")
	}
	return splitACLs(eth0["security.acls"]), nil
}

func cloneInstanceDevices(devices map[string]map[string]string) map[string]map[string]string {
	if len(devices) == 0 {
		return map[string]map[string]string{}
	}
	updated := make(map[string]map[string]string, len(devices))
	for name, device := range devices {
		updated[name] = cloneDeviceMap(device)
	}
	return updated
}

func cloneDeviceMap(device map[string]string) map[string]string {
	copy := make(map[string]string, len(device))
	for key, value := range device {
		copy[key] = value
	}
	return copy
}

func splitACLs(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// NICACLs returns the ACL names to attach to an instance NIC. Restricted modes
// must provide the stable, per-profile ACL name when it is known.
func NICACLs(mode domain.EgressMode, restrictedACLName ...string) []string {
	acls := []string{DefaultDenyACLName}
	switch mode {
	case domain.EgressStandard:
		return append(acls, StandardEgressACLName)
	case domain.EgressRestricted:
		if len(restrictedACLName) > 0 && restrictedACLName[0] != "" {
			return append(acls, restrictedACLName[0])
		}
	}
	return acls
}
