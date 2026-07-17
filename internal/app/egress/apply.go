// SPDX-License-Identifier: AGPL-3.0-only

// Package egress applies host-enforced egress profiles to instances.
package egress

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/openbox-dev/openbox/internal/dnsproxy"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/runtime/incus"
)

// PolicyApply is the resolved policy payload for one instance.
type PolicyApply struct {
	Instance     domain.Instance
	Mode         domain.EgressMode
	Destinations []string
	Resolution   domain.AllowlistResolution
}

// PolicyRuntime programs Incus ACLs for a resolved allowlist.
type PolicyRuntime interface {
	ProgramNetworkPolicy(context.Context, PolicyApply) error
	SetAllowlistResolution(domain.InstanceID, domain.AllowlistResolution)
}

// Applicator resolves profile hostnames on the host and programs runtime ACLs.
type Applicator struct {
	resolver *dnsproxy.AllowlistResolver
	runtime  PolicyRuntime
	auditor  PolicyAuditor

	mu           sync.Mutex
	lastPrograms map[domain.InstanceID]string
}

func NewApplicator(resolver *dnsproxy.AllowlistResolver, runtime PolicyRuntime) *Applicator {
	return &Applicator{
		resolver:     resolver,
		runtime:      runtime,
		lastPrograms: make(map[domain.InstanceID]string),
	}
}

// SetAuditor optionally records redacted policy.apply / policy.apply_failed events.
func (a *Applicator) SetAuditor(auditor PolicyAuditor) {
	if a != nil {
		a.auditor = auditor
	}
}

// AdapterRuntime adapts *incus.Adapter to PolicyRuntime.
type AdapterRuntime struct {
	Adapter *incus.Adapter
}

func (r AdapterRuntime) ProgramNetworkPolicy(ctx context.Context, apply PolicyApply) error {
	return r.Adapter.ProgramNetworkPolicy(ctx, incus.PolicyApply{
		Instance:     apply.Instance,
		Mode:         apply.Mode,
		Destinations: apply.Destinations,
		Resolution:   apply.Resolution,
	})
}

func (r AdapterRuntime) SetAllowlistResolution(instanceID domain.InstanceID, resolution domain.AllowlistResolution) {
	r.Adapter.SetAllowlistResolution(instanceID, resolution)
}

// Apply resolves the profile allowlist and programs the instance NIC policy.
func (a *Applicator) Apply(ctx context.Context, instance domain.Instance, profile domain.EgressProfile) error {
	_, err := a.apply(ctx, instance, profile, true)
	return err
}

// apply resolves and programs policy. When audit is false, callers emit their
// own policy.refresh events instead of policy.apply. changed is false when a
// refresh would reprogram the same destination set already on the instance.
func (a *Applicator) apply(ctx context.Context, instance domain.Instance, profile domain.EgressProfile, audit bool) (changed bool, err error) {
	if instance.RuntimeRef == "" {
		return false, fmt.Errorf("network policy runtime ref is required")
	}
	instance.EgressMode = profile.Mode
	literals, hostnames, err := domain.ParseAllowlistEntries(profile.AllowedDestinationsJSON)
	if err != nil {
		return false, err
	}
	if profile.Mode == domain.EgressRestricted {
		for _, literal := range literals {
			if !domain.SafeRestrictedAllowlistLiteral(literal) {
				return false, fmt.Errorf("restricted allowlist destination %q is not a public unicast address or CIDR", literal)
			}
		}
	}

	resolution := domain.AllowlistResolution{
		State:    "idle",
		Pending:  []string{},
		Resolved: []string{},
		Failed:   []string{},
	}
	if len(hostnames) > 0 {
		resolution.State = "pending"
		resolution.Pending = append([]string(nil), hostnames...)
		a.runtime.SetAllowlistResolution(instance.ID, resolution)
	}

	destinations := append([]string(nil), literals...)
	if len(hostnames) > 0 {
		if a.resolver == nil {
			resolution.State = "failed"
			resolution.Pending = []string{}
			resolution.Failed = append([]string(nil), hostnames...)
			a.runtime.SetAllowlistResolution(instance.ID, resolution)
			err := fmt.Errorf("dns allowlist resolver is required")
			if audit {
				a.recordApply(ctx, instance, profile, resolution.State, "failed", err)
			}
			return false, err
		}
		resolved := make([]string, 0, len(hostnames))
		for _, hostname := range hostnames {
			addresses, resolveErr := a.resolver.Resolve(ctx, hostname)
			if resolveErr != nil {
				resolution.State = "failed"
				resolution.Pending = []string{}
				resolution.Failed = []string{hostname}
				resolution.Resolved = resolved
				a.runtime.SetAllowlistResolution(instance.ID, resolution)
				if audit {
					a.recordApply(ctx, instance, profile, resolution.State, "failed", resolveErr)
				}
				return false, resolveErr
			}
			for _, address := range addresses {
				destinations = append(destinations, address.String())
			}
			resolved = append(resolved, hostname)
		}
		resolution.State = "resolved"
		resolution.Pending = []string{}
		resolution.Resolved = resolved
		resolution.Failed = []string{}
	}

	fingerprint := destinationFingerprint(destinations)
	if !audit && a.programmedFingerprint(instance.ID) == fingerprint {
		a.runtime.SetAllowlistResolution(instance.ID, resolution)
		return false, nil
	}

	apply := PolicyApply{
		Instance:     instance,
		Mode:         profile.Mode,
		Destinations: destinations,
		Resolution:   resolution,
	}
	if err := a.runtime.ProgramNetworkPolicy(ctx, apply); err != nil {
		if audit {
			a.recordApply(ctx, instance, profile, resolution.State, "failed", err)
		}
		return false, err
	}
	a.rememberProgrammed(instance.ID, fingerprint)
	a.runtime.SetAllowlistResolution(instance.ID, resolution)
	if audit {
		a.recordApply(ctx, instance, profile, resolution.State, "succeeded", nil)
	}
	return true, nil
}

func destinationFingerprint(destinations []string) string {
	sorted := append([]string(nil), destinations...)
	sort.Strings(sorted)
	return strings.Join(sorted, "\n")
}

func (a *Applicator) programmedFingerprint(instanceID domain.InstanceID) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastPrograms[instanceID]
}

func (a *Applicator) rememberProgrammed(instanceID domain.InstanceID, fingerprint string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastPrograms[instanceID] = fingerprint
}

func (a *Applicator) recordApply(ctx context.Context, instance domain.Instance, profile domain.EgressProfile, resolutionState, outcome string, applyErr error) {
	if a == nil || a.auditor == nil {
		return
	}
	action := ActionPolicyApply
	message := ""
	if applyErr != nil {
		action = ActionPolicyApplyFailed
		message = applyErr.Error()
	}
	_ = a.auditor.RecordPolicyEvent(ctx, PolicyAuditEvent{
		OwnerID: instance.OwnerID, Actor: "openboxd", Action: action,
		InstanceID: instance.ID, ProfileID: profile.ID, Mode: profile.Mode,
		Outcome: outcome, Message: message, Resolution: resolutionState,
	})
}
