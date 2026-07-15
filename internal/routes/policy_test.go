// SPDX-License-Identifier: AGPL-3.0-only

package routes_test

import (
	"errors"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/routes"
)

func TestValidateManagedTargetAcceptsOwnedManagedInstance(t *testing.T) {
	t.Parallel()
	instance := &domain.Instance{
		ID: "inst-1", OwnerID: "owner-1", Name: "dev",
		RuntimeRef: "incus-owned-ref",
	}
	if err := routes.ValidateManagedTarget("owner-1", instance); err != nil {
		t.Fatalf("managed target rejected: %v", err)
	}
}

func TestValidateManagedTargetRejectsHostGatewayUnmanagedAndForeignOwner(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		owner     domain.OwnerID
		instance  *domain.Instance
		wantCode  domain.ErrorCode
		wantField string
	}{
		{
			name:     "missing instance unmanaged",
			owner:    "owner-1",
			instance: nil,
			wantCode: domain.CodeNotFound, wantField: "instance",
		},
		{
			name:  "empty runtime ref unmanaged",
			owner: "owner-1",
			instance: &domain.Instance{
				ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "  ",
			},
			wantCode: domain.CodeRuntimeMissing, wantField: "runtime_ref",
		},
		{
			name:  "host sentinel",
			owner: "owner-1",
			instance: &domain.Instance{
				ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "host",
			},
			wantCode: domain.CodeInvalidArgument, wantField: "target",
		},
		{
			name:  "HOST sentinel case insensitive",
			owner: "owner-1",
			instance: &domain.Instance{
				ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "HOST",
			},
			wantCode: domain.CodeInvalidArgument, wantField: "target",
		},
		{
			name:  "gateway sentinel",
			owner: "owner-1",
			instance: &domain.Instance{
				ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "gateway",
			},
			wantCode: domain.CodeInvalidArgument, wantField: "target",
		},
		{
			name:  "GATEWAY sentinel case insensitive",
			owner: "owner-1",
			instance: &domain.Instance{
				ID: "inst-1", OwnerID: "owner-1", RuntimeRef: " Gateway ",
			},
			wantCode: domain.CodeInvalidArgument, wantField: "target",
		},
		{
			name:  "another owner",
			owner: "owner-1",
			instance: &domain.Instance{
				ID: "inst-2", OwnerID: "owner-2", RuntimeRef: "incus-other",
			},
			wantCode: domain.CodeNotFound, wantField: "instance",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := routes.ValidateManagedTarget(tt.owner, tt.instance)
			assertDomainError(t, err, tt.wantCode, tt.wantField)
		})
	}
}

func TestValidateTargetPortRange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		port  int
		valid bool
	}{
		{name: "min", port: 1, valid: true},
		{name: "max", port: 65535, valid: true},
		{name: "common", port: 3000, valid: true},
		{name: "zero", port: 0, valid: false},
		{name: "negative", port: -1, valid: false},
		{name: "above max", port: 65536, valid: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := routes.ValidateTargetPort(tt.port)
			if tt.valid {
				if err != nil {
					t.Fatalf("port %d rejected: %v", tt.port, err)
				}
				return
			}
			assertDomainError(t, err, domain.CodeInvalidArgument, "target_port")
		})
	}
}

func TestNewRoutePrivateByDefault(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.FixedZone("local", 3600))
	route, err := routes.NewRoute("route-1", "owner-1", "inst-1", "my-vm.openbox.example", 3000, now)
	if err != nil {
		t.Fatal(err)
	}
	if route.Visibility != domain.RoutePrivate {
		t.Fatalf("visibility = %q, want private by default", route.Visibility)
	}
	if route.CreatedAt.Location() != time.UTC || route.UpdatedAt.Location() != time.UTC {
		t.Fatal("timestamps must be UTC")
	}
	if route.Hostname != "my-vm.openbox.example" || route.TargetPort != 3000 {
		t.Fatalf("route fields = %+v", route)
	}
}

func TestNewRouteRejectsInvalidPort(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	_, err := routes.NewRoute("route-1", "owner-1", "inst-1", "app.example", 0, now)
	assertDomainError(t, err, domain.CodeInvalidArgument, "target_port")
}

func TestNewRouteWithExplicitPublicVisibility(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	route, err := routes.NewRouteWithVisibility(
		"route-1", "owner-1", "inst-1", "app.example", 8080, domain.RoutePublic, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if route.Visibility != domain.RoutePublic {
		t.Fatalf("visibility = %q, want public", route.Visibility)
	}
}

func TestNewRouteRejectsInvalidVisibility(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	_, err := routes.NewRouteWithVisibility(
		"route-1", "owner-1", "inst-1", "app.example", 8080, domain.RouteVisibility("shared"), now,
	)
	assertDomainError(t, err, domain.CodeInvalidArgument, "visibility")
}

func TestHostnameUniquenessWithinOwner(t *testing.T) {
	t.Parallel()
	existing := []domain.Route{
		{ID: "r1", OwnerID: "owner-1", Hostname: "app.openbox.example"},
		{ID: "r2", OwnerID: "owner-2", Hostname: "app.openbox.example"},
	}
	if err := routes.CheckHostnameUnique("owner-1", "other.openbox.example", existing); err != nil {
		t.Fatalf("unused hostname rejected: %v", err)
	}
	if err := routes.CheckHostnameUnique("owner-1", "app.openbox.example", existing); err == nil {
		t.Fatal("duplicate hostname for same owner accepted")
	} else {
		assertDomainError(t, err, domain.CodeConflict, "hostname")
	}
	if err := routes.CheckHostnameUnique("owner-3", "app.openbox.example", existing); err == nil {
		t.Fatal("same hostname for different owner accepted")
	}
}

func assertDomainError(t *testing.T, err error, code domain.ErrorCode, field string) {
	t.Helper()
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("got %v (%T), want domain.Error", err, err)
	}
	if domainErr.Code != code || domainErr.Field != field {
		t.Fatalf("got %v, want code=%s field=%s", err, code, field)
	}
}
