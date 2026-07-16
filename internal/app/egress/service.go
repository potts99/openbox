// SPDX-License-Identifier: AGPL-3.0-only

package egress

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

// ApplyError reports a per-instance failure during profile fan-out re-apply.
type ApplyError struct {
	InstanceID domain.InstanceID
	Message    string
}

// ProfileStore persists egress profiles and instance bindings.
type ProfileStore interface {
	EnsureSystemEgressProfiles(context.Context) error
	CreateEgressProfile(context.Context, domain.EgressProfile) (domain.EgressProfile, error)
	GetEgressProfile(context.Context, domain.EgressProfileID) (domain.EgressProfile, error)
	GetEgressProfileByName(context.Context, string) (domain.EgressProfile, error)
	ListEgressProfiles(context.Context) ([]domain.EgressProfile, error)
	UpdateEgressProfile(context.Context, domain.EgressProfile) error
	DeleteEgressProfile(context.Context, domain.EgressProfileID) error
	CountInstancesWithEgressProfile(context.Context, domain.EgressProfileID) (int, error)
	ListInstancesWithEgressProfile(context.Context, domain.EgressProfileID) ([]domain.Instance, error)
	UpdateInstanceEgressProfile(context.Context, domain.OwnerID, domain.InstanceID, domain.EgressProfileID, domain.EgressMode, time.Time) error
	GetInstance(context.Context, domain.OwnerID, domain.InstanceID) (domain.Instance, error)
}

// InstanceMarker marks an instance as error after a failed fan-out apply.
type InstanceMarker interface {
	MarkInstanceError(context.Context, domain.OwnerID, domain.InstanceID) error
}

type Options struct {
	Now   func() time.Time
	NewID func() string
}

// Service manages system egress profiles and fan-out re-apply.
type Service struct {
	store      ProfileStore
	applicator *Applicator
	marker     InstanceMarker
	now        func() time.Time
	newID      func() string
}

func New(store ProfileStore, applicator *Applicator, options Options) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("egress profile store is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = func() string {
			return fmt.Sprintf("egress-%d", time.Now().UnixNano())
		}
	}
	return &Service{store: store, applicator: applicator, now: options.Now, newID: options.NewID}, nil
}

// SetInstanceMarker optionally records apply failures on instances during fan-out.
func (s *Service) SetInstanceMarker(marker InstanceMarker) {
	s.marker = marker
}

func (s *Service) EnsureSeeds(ctx context.Context) error {
	return s.store.EnsureSystemEgressProfiles(ctx)
}

func (s *Service) List(ctx context.Context) ([]domain.EgressProfile, error) {
	return s.store.ListEgressProfiles(ctx)
}

func (s *Service) Get(ctx context.Context, id domain.EgressProfileID) (domain.EgressProfile, int, error) {
	profile, err := s.store.GetEgressProfile(ctx, id)
	if err != nil {
		return domain.EgressProfile{}, 0, err
	}
	count, err := s.store.CountInstancesWithEgressProfile(ctx, id)
	if err != nil {
		return domain.EgressProfile{}, 0, err
	}
	return profile, count, nil
}

type CreateProfileInput struct {
	Name                string
	Mode                domain.EgressMode
	AllowedDestinations []string
}

func (s *Service) Create(ctx context.Context, input CreateProfileInput) (domain.EgressProfile, error) {
	raw, err := json.Marshal(input.AllowedDestinations)
	if err != nil {
		return domain.EgressProfile{}, err
	}
	now := s.now().UTC()
	profile := domain.EgressProfile{
		ID:                      domain.EgressProfileID(s.newID()),
		Name:                    input.Name,
		Mode:                    input.Mode,
		AllowedDestinationsJSON: raw,
		DNSPolicy:               domain.DNSPolicyHostResolve,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	return s.store.CreateEgressProfile(ctx, profile)
}

type UpdateProfileInput struct {
	Name                *string
	Mode                *domain.EgressMode
	AllowedDestinations *[]string
}

func (s *Service) Update(ctx context.Context, id domain.EgressProfileID, input UpdateProfileInput) (domain.EgressProfile, []ApplyError, error) {
	profile, err := s.store.GetEgressProfile(ctx, id)
	if err != nil {
		return domain.EgressProfile{}, nil, err
	}
	if input.Name != nil {
		profile.Name = *input.Name
	}
	if input.Mode != nil {
		profile.Mode = *input.Mode
	}
	if input.AllowedDestinations != nil {
		raw, marshalErr := json.Marshal(*input.AllowedDestinations)
		if marshalErr != nil {
			return domain.EgressProfile{}, nil, marshalErr
		}
		profile.AllowedDestinationsJSON = raw
	}
	profile.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateEgressProfile(ctx, profile); err != nil {
		return domain.EgressProfile{}, nil, err
	}
	updated, err := s.store.GetEgressProfile(ctx, id)
	if err != nil {
		return domain.EgressProfile{}, nil, err
	}
	applyErrors := s.fanOut(ctx, updated)
	return updated, applyErrors, nil
}

func (s *Service) Delete(ctx context.Context, id domain.EgressProfileID) error {
	return s.store.DeleteEgressProfile(ctx, id)
}

func (s *Service) Attach(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID, profileID domain.EgressProfileID) (domain.Instance, error) {
	profile, err := s.store.GetEgressProfile(ctx, profileID)
	if err != nil {
		return domain.Instance{}, err
	}
	now := s.now().UTC()
	if err := s.store.UpdateInstanceEgressProfile(ctx, ownerID, instanceID, profile.ID, profile.Mode, now); err != nil {
		return domain.Instance{}, err
	}
	instance, err := s.store.GetInstance(ctx, ownerID, instanceID)
	if err != nil {
		return domain.Instance{}, err
	}
	if instance.RuntimeRef != "" && s.applicator != nil {
		if err := s.applicator.Apply(ctx, instance, profile); err != nil {
			if s.marker != nil {
				_ = s.marker.MarkInstanceError(ctx, ownerID, instanceID)
			}
			return domain.Instance{}, err
		}
	}
	return instance, nil
}

func (s *Service) fanOut(ctx context.Context, profile domain.EgressProfile) []ApplyError {
	if s.applicator == nil {
		return nil
	}
	instances, err := s.store.ListInstancesWithEgressProfile(ctx, profile.ID)
	if err != nil {
		return []ApplyError{{InstanceID: "", Message: err.Error()}}
	}
	var applyErrors []ApplyError
	for _, instance := range instances {
		if instance.RuntimeRef == "" {
			continue
		}
		instance.EgressMode = profile.Mode
		instance.EgressProfileID = profile.ID
		if err := s.store.UpdateInstanceEgressProfile(ctx, instance.OwnerID, instance.ID, profile.ID, profile.Mode, s.now().UTC()); err != nil {
			applyErrors = append(applyErrors, ApplyError{InstanceID: instance.ID, Message: err.Error()})
			continue
		}
		if err := s.applicator.Apply(ctx, instance, profile); err != nil {
			if s.marker != nil {
				_ = s.marker.MarkInstanceError(ctx, instance.OwnerID, instance.ID)
			}
			applyErrors = append(applyErrors, ApplyError{InstanceID: instance.ID, Message: err.Error()})
		}
	}
	return applyErrors
}

// ResolveProfileForCreate returns the profile that should bind to a new instance.
func (s *Service) ResolveProfileForCreate(ctx context.Context, kind domain.InstanceKind, profileID domain.EgressProfileID) (domain.EgressProfile, error) {
	if profileID == "" {
		profileID = domain.DefaultEgressProfileID(kind)
	}
	return s.store.GetEgressProfile(ctx, profileID)
}
