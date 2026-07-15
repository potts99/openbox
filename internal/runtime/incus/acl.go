// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"net/url"

	"github.com/openbox-dev/openbox/internal/domain"
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
}

func networkACLResource() resource {
	return resource{
		Name:        DefaultDenyACLName,
		Description: "OpenBox default-deny instance network policy",
		Config:      managedConfig("network-acl", nil),
		Egress: []networkACLRule{
			{
				Action: "allow", Destination: ManagedBridgeGatewayHost, Protocol: "udp", DestinationPort: "53",
				Description: "allow DNS UDP to managed bridge gateway",
			},
			{
				Action: "allow", Destination: ManagedBridgeGatewayHost, Protocol: "tcp", DestinationPort: "53",
				Description: "allow DNS TCP to managed bridge gateway",
			},
			{
				Action: "allow", Destination: ManagedBridgeGatewayHost, Protocol: "tcp", DestinationPort: LLMGatewayPort,
				Description: "allow LLM Gateway management placeholder",
			},
		},
		Ingress: []networkACLRule{
			{
				Action: "allow", Source: ManagedBridgeGatewayHost, Protocol: "tcp", DestinationPort: "22",
				Description: "allow OpenBox SSH gateway access",
			},
		},
	}
}

func standardEgressACLResource() resource {
	egress := make([]networkACLRule, 0, len(managedBridgePeerCIDRs())+1)
	for _, peerCIDR := range managedBridgePeerCIDRs() {
		egress = append(egress, networkACLRule{
			Action: "reject", Destination: peerCIDR,
			Description: "deny peer instances on managed bridge",
		})
	}
	egress = append(egress, networkACLRule{
		Action: "allow", Destination: "0.0.0.0/0",
		Description: "allow public internet egress",
	})

	return resource{
		Name:        StandardEgressACLName,
		Description: "OpenBox standard instance egress policy",
		Config:      managedConfig("network-acl", nil),
		Egress:      egress,
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
		rules = append(rules, networkACLRule{
			Action: "allow", Destination: destination,
			Description: "allow administrator-approved destination",
		})
	}
	return resource{
		Name:        name,
		Description: "OpenBox restricted instance egress policy",
		Config:      managedConfig("network-acl", nil),
		Egress:      rules,
	}
}

// EnsureRestrictedACL reconciles a named restricted ACL for later
// instance/profile identity binding.
func (a *Adapter) EnsureRestrictedACL(ctx context.Context, name string, destinations []string) error {
	acl := RestrictedACL(name, destinations)
	return a.ensure(ctx, "network ACL", "/1.0/network-acls/"+url.PathEscape(acl.Name), "/1.0/network-acls", nil, acl)
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
