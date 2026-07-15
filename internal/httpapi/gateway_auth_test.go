// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/routes"
)

func TestGatewayAuthPublicBypassesLogin(t *testing.T) {
	t.Parallel()
	repo := newRouteTestRepo()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	repo.routes["rt-pub"] = domain.Route{
		ID: "rt-pub", OwnerID: "owner-local", InstanceID: "inst-1",
		Hostname: "public.example.com", TargetPort: 8080,
		Visibility: domain.RoutePublic, TLSState: routes.TLSStateNone,
		CreatedAt: now, UpdatedAt: now,
	}
	handler := newRouteTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/auth", nil)
	req.Host = "public.example.com"
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestGatewayAuthPrivateRequiresRouteTokenOrOwner(t *testing.T) {
	t.Parallel()
	repo := newRouteTestRepo()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	repo.routes["rt-priv"] = domain.Route{
		ID: "rt-priv", OwnerID: "owner-local", InstanceID: "inst-1",
		Hostname: "private.example.com", TargetPort: 3000,
		Visibility: domain.RoutePrivate, TLSState: routes.TLSStateNone,
		CreatedAt: now, UpdatedAt: now,
	}
	svc, err := routes.New(repo, routes.Options{
		Now:   func() time.Time { return now },
		NewID: func() string { return "route-test-1" },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithOptions(t, &fakeService{}, Options{OwnerID: "owner-local", Routes: svc})

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/auth", nil)
	req.Host = "private.example.com"
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code == http.StatusOK {
		t.Fatal("private route allowed without credentials")
	}

	token, err := svc.CreateRouteToken(context.Background(), "owner-local", "rt-priv", "ci")
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/gateway/auth", nil)
	req.Host = "private.example.com"
	req.Header.Set("Authorization", "Bearer "+token.Secret)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("route token status=%d body=%s", res.Code, res.Body.String())
	}
}
