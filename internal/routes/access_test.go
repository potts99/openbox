// SPDX-License-Identifier: AGPL-3.0-only

package routes_test

import (
	"context"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/routes"
)

func TestAuthorizeAccessPublicBypassesCredentials(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	repo.routes["rt-pub"] = domain.Route{
		ID: "rt-pub", OwnerID: "owner-1", InstanceID: "inst-1",
		Hostname: "public.example.com", TargetPort: 8080,
		Visibility: domain.RoutePublic, TLSState: routes.TLSStateNone,
		CreatedAt: now, UpdatedAt: now,
	}
	svc := newTestService(t, repo)

	if err := svc.AuthorizeAccess(context.Background(), "public.example.com", routes.AccessCredentials{}); err != nil {
		t.Fatalf("public route rejected without credentials: %v", err)
	}
}

func TestAuthorizeAccessPrivateRequiresCredentials(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	repo.routes["rt-priv"] = domain.Route{
		ID: "rt-priv", OwnerID: "owner-1", InstanceID: "inst-1",
		Hostname: "private.example.com", TargetPort: 3000,
		Visibility: domain.RoutePrivate, TLSState: routes.TLSStateNone,
		CreatedAt: now, UpdatedAt: now,
	}
	svc := newTestService(t, repo)

	if err := svc.AuthorizeAccess(context.Background(), "private.example.com", routes.AccessCredentials{}); err == nil {
		t.Fatal("private route allowed without credentials")
	}

	token, err := svc.CreateRouteToken(context.Background(), "owner-1", "rt-priv", "ci")
	if err != nil {
		t.Fatal(err)
	}
	if token.Secret == "" || token.ID == "" {
		t.Fatalf("token missing secret/id: %+v", token)
	}
	listed, err := svc.ListRouteTokens(context.Background(), "owner-1", "rt-priv")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Secret != "" {
		t.Fatalf("list should hide secrets: %+v", listed)
	}

	if err := svc.AuthorizeAccess(context.Background(), "private.example.com", routes.AccessCredentials{
		RouteToken: token.Secret,
	}); err != nil {
		t.Fatalf("route token rejected: %v", err)
	}

	// Token scoped to this route must not open another private hostname.
	repo.routes["rt-other"] = domain.Route{
		ID: "rt-other", OwnerID: "owner-1", InstanceID: "inst-1",
		Hostname: "other.example.com", TargetPort: 3000,
		Visibility: domain.RoutePrivate, TLSState: routes.TLSStateNone,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.AuthorizeAccess(context.Background(), "other.example.com", routes.AccessCredentials{
		RouteToken: token.Secret,
	}); err == nil {
		t.Fatal("route token authorized wrong hostname")
	}

	if err := svc.RevokeRouteToken(context.Background(), "owner-1", "rt-priv", token.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.AuthorizeAccess(context.Background(), "private.example.com", routes.AccessCredentials{
		RouteToken: token.Secret,
	}); err == nil {
		t.Fatal("revoked route token still authorized")
	}
}

func TestAuthorizeAccessPrivateAcceptsOwnerPrincipal(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	repo.routes["rt-priv"] = domain.Route{
		ID: "rt-priv", OwnerID: "owner-1", InstanceID: "inst-1",
		Hostname: "private.example.com", TargetPort: 3000,
		Visibility: domain.RoutePrivate, TLSState: routes.TLSStateNone,
		CreatedAt: now, UpdatedAt: now,
	}
	svc := newTestService(t, repo)

	if err := svc.AuthorizeAccess(context.Background(), "private.example.com", routes.AccessCredentials{
		OwnerID: "owner-1",
	}); err != nil {
		t.Fatalf("owner session/bearer rejected: %v", err)
	}
	if err := svc.AuthorizeAccess(context.Background(), "private.example.com", routes.AccessCredentials{
		OwnerID: "other-owner",
	}); err == nil {
		t.Fatal("foreign owner authorized")
	}
}

func TestAuthorizeAccessUnknownHostDenied(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, newFakeRepo())
	if err := svc.AuthorizeAccess(context.Background(), "missing.example.com", routes.AccessCredentials{}); err == nil {
		t.Fatal("unknown host allowed")
	}
}
