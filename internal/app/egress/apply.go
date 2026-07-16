// SPDX-License-Identifier: AGPL-3.0-only

// Package egress applies host-enforced egress profiles to instances.
package egress

import (
	"context"
	"fmt"

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
}

func NewApplicator(resolver *dnsproxy.AllowlistResolver, runtime PolicyRuntime) *Applicator {
	return &Applicator{resolver: resolver, runtime: runtime}
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
	if instance.RuntimeRef == "" {
		return fmt.Errorf("network policy runtime ref is required")
	}
	instance.EgressMode = profile.Mode
	literals, hostnames, err := domain.ParseAllowlistEntries(profile.AllowedDestinationsJSON)
	if err != nil {
		return err
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
			return fmt.Errorf("dns allowlist resolver is required")
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
				return resolveErr
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

	apply := PolicyApply{
		Instance:     instance,
		Mode:         profile.Mode,
		Destinations: destinations,
		Resolution:   resolution,
	}
	if err := a.runtime.ProgramNetworkPolicy(ctx, apply); err != nil {
		return err
	}
	a.runtime.SetAllowlistResolution(instance.ID, resolution)
	return nil
}
