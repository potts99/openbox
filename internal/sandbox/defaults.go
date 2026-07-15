// SPDX-License-Identifier: AGPL-3.0-only

// Package sandbox owns disposable-instance policy: create defaults, expiry
// helpers, and (later) exec limits. It does not talk to Incus or HTTP.
package sandbox

import (
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/images"
)

// DefaultResources is the v0.1 create baseline when the caller omits limits.
func DefaultResources() domain.Resources {
	return domain.Resources{
		VCPUs:       2,
		MemoryBytes: 2 << 30,
		DiskBytes:   10 << 30,
	}
}

// CreateDefaults is the partial create request plus host context needed to
// fill kind-specific image, isolation, resource, and lifetime defaults.
type CreateDefaults struct {
	Kind               domain.InstanceKind
	Architecture       string
	Runtime            string // container or virtual-machine
	Catalog            images.Catalog
	Image              string
	RequestedIsolation domain.IsolationRequest
	Resources          domain.Resources
	Lifetime           time.Duration
	EgressMode         domain.EgressMode
}

// AppliedDefaults is the create policy after gaps are filled.
type AppliedDefaults struct {
	Image              string
	RequestedIsolation domain.IsolationRequest
	Resources          domain.Resources
	Lifetime           time.Duration // 0 for VPS/Devbox
	EgressMode         domain.EgressMode
}

// ApplyDefaults fills empty create fields from the curated catalog and kind
// policy. Explicit caller values win. Sandbox lifetime defaults to one hour
// and may not exceed MaxSandboxLifetime.
func ApplyDefaults(in CreateDefaults) (AppliedDefaults, error) {
	if in.Kind == "" {
		in.Kind = domain.KindVPS
	}
	manifest, err := in.Catalog.DefaultFor(in.Kind, in.Architecture, in.Runtime)
	if err != nil {
		return AppliedDefaults{}, err
	}
	out := AppliedDefaults{
		Image:              in.Image,
		RequestedIsolation: in.RequestedIsolation,
		Resources:          in.Resources,
		Lifetime:           in.Lifetime,
		EgressMode:         in.EgressMode,
	}
	if out.Image == "" {
		out.Image = manifest.Alias
	}
	if out.RequestedIsolation == "" {
		out.RequestedIsolation = domain.IsolationBestAvailable
	}
	if out.Resources == (domain.Resources{}) {
		out.Resources = DefaultResources()
	}
	if out.EgressMode == "" {
		if in.Kind == domain.KindSandbox {
			out.EgressMode = domain.EgressRestricted
		} else {
			out.EgressMode = domain.EgressStandard
		}
	}
	switch in.Kind {
	case domain.KindSandbox:
		if out.Lifetime == 0 {
			out.Lifetime = domain.DefaultSandboxLifetime
		}
		if out.Lifetime > domain.MaxSandboxLifetime {
			return AppliedDefaults{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "lifetime"}
		}
	default:
		out.Lifetime = 0
	}
	return out, nil
}
