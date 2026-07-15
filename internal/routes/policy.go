// SPDX-License-Identifier: AGPL-3.0-only

// Package routes implements HTTPS route policy and administrator-facing route
// management: managed target identity, allowed ports, private-by-default
// visibility, hostname uniqueness, CRUD, publish, and detected-port suggestions.
// Caddy configuration and certificate allowlisting belong to later slice tasks.
package routes

import (
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

// Allowed target port range — matches SQLite CHECK(target_port BETWEEN 1 AND 65535).
// No tighter product range is documented for v0.1.
const (
	MinTargetPort = 1
	MaxTargetPort = 65535
)

// Forbidden RuntimeRef sentinels that must never be reverse-proxy targets.
// Empty refs are treated as unmanaged (see ValidateManagedTarget).
const (
	runtimeRefHost    = "host"
	runtimeRefGateway = "gateway"
)

// ValidateTargetPort rejects ports outside the allowed range.
func ValidateTargetPort(port int) error {
	if port < MinTargetPort || port > MaxTargetPort {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "target_port"}
	}
	return nil
}

// ValidateManagedTarget ensures a route may proxy only to a managed instance
// address owned by the same owner. It rejects the host, gateway, another
// owner's instance, and unmanaged/missing targets (SSRF posture).
func ValidateManagedTarget(routeOwner domain.OwnerID, instance *domain.Instance) error {
	if instance == nil || instance.OwnerID != routeOwner {
		// Treat foreign ownership like absence so callers cannot probe other owners.
		return &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	}
	ref := strings.TrimSpace(instance.RuntimeRef)
	if ref == "" {
		return &domain.Error{Code: domain.CodeRuntimeMissing, Field: "runtime_ref"}
	}
	switch strings.ToLower(ref) {
	case runtimeRefHost, runtimeRefGateway:
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "target"}
	default:
		return nil
	}
}

// NewRoute constructs a route that is private by default.
func NewRoute(
	id domain.RouteID,
	ownerID domain.OwnerID,
	instanceID domain.InstanceID,
	hostname string,
	port int,
	now time.Time,
) (domain.Route, error) {
	return NewRouteWithVisibility(id, ownerID, instanceID, hostname, port, domain.RoutePrivate, now)
}

// NewRouteWithVisibility constructs a route with an explicit visibility.
// Callers that omit visibility should use NewRoute (private by default).
func NewRouteWithVisibility(
	id domain.RouteID,
	ownerID domain.OwnerID,
	instanceID domain.InstanceID,
	hostname string,
	port int,
	visibility domain.RouteVisibility,
	now time.Time,
) (domain.Route, error) {
	if id == "" {
		return domain.Route{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "id"}
	}
	if ownerID == "" {
		return domain.Route{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "owner_id"}
	}
	if instanceID == "" {
		return domain.Route{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "instance_id"}
	}
	if strings.TrimSpace(hostname) == "" {
		return domain.Route{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "hostname"}
	}
	if err := ValidateTargetPort(port); err != nil {
		return domain.Route{}, err
	}
	switch visibility {
	case domain.RoutePrivate, domain.RoutePublic:
	default:
		return domain.Route{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "visibility"}
	}
	now = now.UTC()
	return domain.Route{
		ID:         id,
		OwnerID:    ownerID,
		InstanceID: instanceID,
		Hostname:   strings.TrimSpace(hostname),
		TargetPort: port,
		Visibility: visibility,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

// CheckHostnameUnique reports a conflict when hostname is already used by the
// same owner among existing routes. Hostnames may repeat across owners.
func CheckHostnameUnique(ownerID domain.OwnerID, hostname string, existing []domain.Route) error {
	hostname = strings.TrimSpace(hostname)
	for _, route := range existing {
		if route.OwnerID == ownerID && route.Hostname == hostname {
			return &domain.Error{Code: domain.CodeConflict, Field: "hostname"}
		}
	}
	return nil
}
