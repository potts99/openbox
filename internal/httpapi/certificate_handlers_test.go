// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/routes"
)

func TestCertificateAllowEndpoint(t *testing.T) {
	t.Parallel()
	repo := newRouteTestRepo()
	repo.instances["inst-1"] = domain.Instance{ID: "inst-1", OwnerID: "owner-local", RuntimeRef: "incus-1"}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	repo.routes["route-1"] = domain.Route{
		ID: "route-1", OwnerID: "owner-local", InstanceID: "inst-1",
		Hostname: "dev.openbox.example", TargetPort: 3000,
		Visibility: domain.RoutePrivate, TLSState: routes.TLSStateNone,
		CreatedAt: now, UpdatedAt: now,
	}
	handler := newRouteTestHandler(t, repo)

	t.Run("allows approved hostname", func(t *testing.T) {
		t.Parallel()
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/certificates/allow?domain=dev.openbox.example", nil))
		if res.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
	})

	t.Run("denies unknown hostname", func(t *testing.T) {
		t.Parallel()
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/certificates/allow?domain=unknown.openbox.example", nil))
		if res.Code == http.StatusOK || (res.Code >= 200 && res.Code <= 299) {
			t.Fatalf("unknown hostname allowed: status=%d body=%s", res.Code, res.Body.String())
		}
	})

	t.Run("denies missing domain", func(t *testing.T) {
		t.Parallel()
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/certificates/allow", nil))
		if res.Code >= 200 && res.Code <= 299 {
			t.Fatalf("missing domain allowed: status=%d", res.Code)
		}
	})
}

func TestCertificateAllowlistAbuseHTTP(t *testing.T) {
	t.Parallel()
	repo := newRouteTestRepo()
	repo.instances["inst-1"] = domain.Instance{ID: "inst-1", OwnerID: "owner-local", RuntimeRef: "incus-1"}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	repo.routes["route-1"] = domain.Route{
		ID: "route-1", OwnerID: "owner-local", InstanceID: "inst-1",
		Hostname: "legit.openbox.example", TargetPort: 3000,
		Visibility: domain.RoutePrivate, TLSState: routes.TLSStateNone,
		CreatedAt: now, UpdatedAt: now,
	}
	handler := newRouteTestHandler(t, repo)

	abuse := []string{
		"evil.example",
		"google.com",
		"legit.openbox.example.evil.com",
		"*.openbox.example",
		"legit.openbox.example/admin",
		"' OR 1=1 --",
		"legit.openbox.example'; DROP TABLE routes;--",
		"../../etc/passwd",
		"http://legit.openbox.example",
		"legit.openbox.example:443",
	}
	for _, domainName := range abuse {
		domainName := domainName
		t.Run(domainName, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/v1/certificates/allow?domain="+url.QueryEscape(domainName), nil)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code >= 200 && res.Code <= 299 {
				t.Fatalf("abuse domain %q allowed: status=%d body=%s", domainName, res.Code, res.Body.String())
			}
		})
	}
}

func TestCertificateAllowIsUnauthenticated(t *testing.T) {
	t.Parallel()
	// Caddy ask calls this without owner credentials; loopback API is the trust boundary.
	repo := newRouteTestRepo()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	repo.routes["route-1"] = domain.Route{
		ID: "route-1", OwnerID: "owner-local", InstanceID: "inst-1",
		Hostname: "dev.openbox.example", TargetPort: 3000,
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
	handler, _, _ := newAuthHandlerWithOptions(t, Options{Routes: svc})

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/certificates/allow?domain=dev.openbox.example", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s (want unauthenticated 200 for Caddy ask)", res.Code, res.Body.String())
	}
}
