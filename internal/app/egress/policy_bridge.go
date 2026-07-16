// SPDX-License-Identifier: AGPL-3.0-only

package egress

import (
	"context"

	"github.com/openbox-dev/openbox/internal/domain"
)

// ProfileGetter loads an egress profile by ID.
type ProfileGetter interface {
	GetEgressProfile(context.Context, domain.EgressProfileID) (domain.EgressProfile, error)
}

// PolicyBackend removes, verifies, and reports network policy status.
type PolicyBackend interface {
	RemoveNetworkPolicy(context.Context, domain.Instance) error
	VerifyNetworkPolicy(context.Context, domain.Instance) error
	NetworkPolicyStatus(domain.Instance) domain.NetworkPolicyStatus
}

// PolicyBridge implements instances.NetworkPolicy using profiles + Applicator.
type PolicyBridge struct {
	Profiles   ProfileGetter
	Applicator *Applicator
	Backend    PolicyBackend
}

func (b *PolicyBridge) ApplyNetworkPolicy(ctx context.Context, instance domain.Instance) error {
	profileID := instance.EgressProfileID
	if profileID == "" {
		profileID = domain.DefaultEgressProfileID(instance.Kind)
	}
	profile, err := b.Profiles.GetEgressProfile(ctx, profileID)
	if err != nil {
		return err
	}
	instance.EgressMode = profile.Mode
	instance.EgressProfileID = profile.ID
	return b.Applicator.Apply(ctx, instance, profile)
}

func (b *PolicyBridge) RemoveNetworkPolicy(ctx context.Context, instance domain.Instance) error {
	return b.Backend.RemoveNetworkPolicy(ctx, instance)
}

func (b *PolicyBridge) VerifyNetworkPolicy(ctx context.Context, instance domain.Instance) error {
	return b.Backend.VerifyNetworkPolicy(ctx, instance)
}

func (b *PolicyBridge) NetworkPolicyStatus(instance domain.Instance) domain.NetworkPolicyStatus {
	return b.Backend.NetworkPolicyStatus(instance)
}
