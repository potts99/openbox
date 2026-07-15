// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

// TLSStateNone is the initial TLS state before certificate issuance (later tasks).
const TLSStateNone = "none"

// Repository persists routes and loads instances for managed-target checks.
type Repository interface {
	CreateRoute(context.Context, domain.Route) error
	GetRoute(context.Context, domain.OwnerID, domain.RouteID) (domain.Route, error)
	ListRoutes(context.Context, domain.OwnerID) ([]domain.Route, error)
	UpdateRoute(context.Context, domain.Route) error
	DeleteRoute(context.Context, domain.OwnerID, domain.RouteID) error
	GetInstance(context.Context, domain.OwnerID, domain.InstanceID) (domain.Instance, error)
}

// CreateInput is the administrator-supplied create payload. Visibility is never
// taken from the client — new routes are always private.
type CreateInput struct {
	InstanceID domain.InstanceID
	Hostname   string
	TargetPort int
}

// UpdateInput changes hostname and/or target port without changing visibility.
type UpdateInput struct {
	Hostname   *string
	TargetPort *int
}

// Service owns route CRUD, publish, and detected-port suggestions.
type Service struct {
	repo  Repository
	now   func() time.Time
	newID func() string
}

// Options configures Service clocks and ID generation.
type Options struct {
	Now   func() time.Time
	NewID func() string
}

// New constructs a route Service.
func New(repo Repository, options Options) (*Service, error) {
	if repo == nil {
		return nil, &domain.Error{Code: domain.CodeInvalidArgument, Field: "repository"}
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.NewID == nil {
		options.NewID = randomRouteID
	}
	return &Service{repo: repo, now: options.Now, newID: options.NewID}, nil
}

// Create validates the managed target and persists a private route.
func (s *Service) Create(ctx context.Context, ownerID domain.OwnerID, input CreateInput) (domain.Route, error) {
	instance, err := s.repo.GetInstance(ctx, ownerID, input.InstanceID)
	if err != nil {
		return domain.Route{}, err
	}
	if err := ValidateManagedTarget(ownerID, &instance); err != nil {
		return domain.Route{}, err
	}
	existing, err := s.repo.ListRoutes(ctx, ownerID)
	if err != nil {
		return domain.Route{}, err
	}
	if err := CheckHostnameUnique(ownerID, input.Hostname, existing); err != nil {
		return domain.Route{}, err
	}
	now := s.now().UTC()
	route, err := NewRoute(domain.RouteID(s.newID()), ownerID, input.InstanceID, input.Hostname, input.TargetPort, now)
	if err != nil {
		return domain.Route{}, err
	}
	route.TLSState = TLSStateNone
	if err := s.repo.CreateRoute(ctx, route); err != nil {
		return domain.Route{}, err
	}
	return route, nil
}

// List returns owner-scoped routes.
func (s *Service) List(ctx context.Context, ownerID domain.OwnerID) ([]domain.Route, error) {
	return s.repo.ListRoutes(ctx, ownerID)
}

// Get returns one owner-scoped route.
func (s *Service) Get(ctx context.Context, ownerID domain.OwnerID, id domain.RouteID) (domain.Route, error) {
	return s.repo.GetRoute(ctx, ownerID, id)
}

// Update changes hostname and/or target port; visibility is unchanged.
func (s *Service) Update(ctx context.Context, ownerID domain.OwnerID, id domain.RouteID, input UpdateInput) (domain.Route, error) {
	route, err := s.repo.GetRoute(ctx, ownerID, id)
	if err != nil {
		return domain.Route{}, err
	}
	if input.Hostname != nil {
		hostname := *input.Hostname
		if hostname != route.Hostname {
			existing, listErr := s.repo.ListRoutes(ctx, ownerID)
			if listErr != nil {
				return domain.Route{}, listErr
			}
			others := make([]domain.Route, 0, len(existing))
			for _, item := range existing {
				if item.ID != route.ID {
					others = append(others, item)
				}
			}
			if err := CheckHostnameUnique(ownerID, hostname, others); err != nil {
				return domain.Route{}, err
			}
		}
		route.Hostname = hostname
	}
	if input.TargetPort != nil {
		if err := ValidateTargetPort(*input.TargetPort); err != nil {
			return domain.Route{}, err
		}
		route.TargetPort = *input.TargetPort
	}
	// Re-validate through constructor fields without flipping visibility.
	rebuilt, err := NewRouteWithVisibility(route.ID, route.OwnerID, route.InstanceID, route.Hostname, route.TargetPort, route.Visibility, s.now().UTC())
	if err != nil {
		return domain.Route{}, err
	}
	rebuilt.TLSState = route.TLSState
	rebuilt.CreatedAt = route.CreatedAt
	if err := s.repo.UpdateRoute(ctx, rebuilt); err != nil {
		return domain.Route{}, err
	}
	return rebuilt, nil
}

// Delete removes an owner-scoped route.
func (s *Service) Delete(ctx context.Context, ownerID domain.OwnerID, id domain.RouteID) error {
	return s.repo.DeleteRoute(ctx, ownerID, id)
}

// Publish flips visibility to public. This is the explicit publish action;
// create never publishes automatically.
func (s *Service) Publish(ctx context.Context, ownerID domain.OwnerID, id domain.RouteID) (domain.Route, error) {
	route, err := s.repo.GetRoute(ctx, ownerID, id)
	if err != nil {
		return domain.Route{}, err
	}
	if route.Visibility == domain.RoutePublic {
		return route, nil
	}
	route.Visibility = domain.RoutePublic
	route.UpdatedAt = s.now().UTC()
	if err := s.repo.UpdateRoute(ctx, route); err != nil {
		return domain.Route{}, err
	}
	return route, nil
}

// SuggestPorts returns candidate ports for an instance without creating routes.
// v0.1 has no Incus listening-port scanner yet, so this returns an empty list
// after confirming the instance is an owned managed target.
func (s *Service) SuggestPorts(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID) ([]int, error) {
	instance, err := s.repo.GetInstance(ctx, ownerID, instanceID)
	if err != nil {
		return nil, err
	}
	if err := ValidateManagedTarget(ownerID, &instance); err != nil {
		return nil, err
	}
	return []int{}, nil
}

func randomRouteID() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "route_" + hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
	}
	return "route_" + hex.EncodeToString(raw[:])
}
