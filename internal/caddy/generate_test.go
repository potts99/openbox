// SPDX-License-Identifier: AGPL-3.0-only

package caddy_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/caddy"
	"github.com/openbox-dev/openbox/internal/domain"
)

func TestGenerateEmptyRoutesMatchesGolden(t *testing.T) {
	t.Parallel()
	got, err := caddy.Generate(nil, mapResolver{})
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "empty.caddyfile", got)
}

func TestGenerateMixedVisibilityMatchesGolden(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	routes := []domain.Route{
		{
			ID: "rt-public", OwnerID: "owner-1", InstanceID: "inst-public",
			Hostname: "public.example.com", TargetPort: 8080,
			Visibility: domain.RoutePublic, TLSState: "none",
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "rt-private", OwnerID: "owner-1", InstanceID: "inst-private",
			Hostname: "private.example.com", TargetPort: 3000,
			Visibility: domain.RoutePrivate, TLSState: "none",
			CreatedAt: now, UpdatedAt: now,
		},
	}
	resolver := mapResolver{
		"inst-public": {
			Instance: domain.Instance{ID: "inst-public", OwnerID: "owner-1", RuntimeRef: "incus-public"},
			Address:  "10.42.0.5",
		},
		"inst-private": {
			Instance: domain.Instance{ID: "inst-private", OwnerID: "owner-1", RuntimeRef: "incus-private"},
			Address:  "10.42.0.12",
		},
	}
	got, err := caddy.Generate(routes, resolver)
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "mixed_visibility.caddyfile", got)
}

func TestGenerateEmitsStreamingAndForwardedHeaders(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	got, err := caddy.Generate([]domain.Route{{
		ID: "rt-1", OwnerID: "owner-1", InstanceID: "inst-1",
		Hostname: "app.example.com", TargetPort: 3000,
		Visibility: domain.RoutePublic, TLSState: "none",
		CreatedAt: now, UpdatedAt: now,
	}}, mapResolver{
		"inst-1": {
			Instance: domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "incus-1"},
			Address:  "10.42.0.9",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{
		"header_up Host {host}",
		"header_up X-Forwarded-Host {host}",
		"header_up X-Forwarded-Proto {scheme}",
		"header_up X-Forwarded-For {remote_host}",
		"flush_interval -1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated config missing %q:\n%s", want, text)
		}
	}
}

func TestGenerateDoesNotInventRoutes(t *testing.T) {
	t.Parallel()
	// Resolver knows about an instance, but no approved route was passed.
	resolver := mapResolver{
		"inst-1": {
			Instance: domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "incus-1"},
			Address:  "10.42.0.99",
		},
	}
	got, err := caddy.Generate(nil, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(mustReadGolden(t, "empty.caddyfile")) {
		t.Fatalf("generator invented routes from resolver alone:\n%s", got)
	}
	if strings.Contains(string(got), "10.42.0.99") {
		t.Fatal("empty input must not emit any upstream")
	}
}

func TestGenerateRejectsHostGatewayUnmanagedTargets(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	base := domain.Route{
		ID: "rt-1", OwnerID: "owner-1", InstanceID: "inst-1",
		Hostname: "app.example.com", TargetPort: 3000,
		Visibility: domain.RoutePrivate, CreatedAt: now, UpdatedAt: now,
	}
	tests := []struct {
		name     string
		upstream caddy.Upstream
		wantCode domain.ErrorCode
	}{
		{
			name: "host",
			upstream: caddy.Upstream{
				Instance: domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "host"},
				Address:  "10.42.0.1",
			},
			wantCode: domain.CodeInvalidArgument,
		},
		{
			name: "gateway",
			upstream: caddy.Upstream{
				Instance: domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "gateway"},
				Address:  "10.42.0.1",
			},
			wantCode: domain.CodeInvalidArgument,
		},
		{
			name: "unmanaged empty runtime",
			upstream: caddy.Upstream{
				Instance: domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: ""},
				Address:  "10.42.0.1",
			},
			wantCode: domain.CodeRuntimeMissing,
		},
		{
			name: "foreign owner",
			upstream: caddy.Upstream{
				Instance: domain.Instance{ID: "inst-1", OwnerID: "other", RuntimeRef: "incus-1"},
				Address:  "10.42.0.1",
			},
			wantCode: domain.CodeNotFound,
		},
		{
			name: "empty address",
			upstream: caddy.Upstream{
				Instance: domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "incus-1"},
				Address:  "  ",
			},
			wantCode: domain.CodeInvalidArgument,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := caddy.Generate([]domain.Route{base}, mapResolver{"inst-1": tt.upstream})
			var de *domain.Error
			if !errors.As(err, &de) {
				t.Fatalf("err=%v, want domain.Error", err)
			}
			if de.Code != tt.wantCode {
				t.Fatalf("code=%q, want %q", de.Code, tt.wantCode)
			}
		})
	}
}

func TestGenerateRejectsMissingUpstream(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	route := domain.Route{
		ID: "rt-1", OwnerID: "owner-1", InstanceID: "missing",
		Hostname: "app.example.com", TargetPort: 3000,
		Visibility: domain.RoutePrivate, CreatedAt: now, UpdatedAt: now,
	}
	_, err := caddy.Generate([]domain.Route{route}, mapResolver{})
	if err == nil {
		t.Fatal("expected error for unresolved instance")
	}
}

type mapResolver map[domain.InstanceID]caddy.Upstream

func (m mapResolver) Resolve(ownerID domain.OwnerID, instanceID domain.InstanceID) (caddy.Upstream, error) {
	up, ok := m[instanceID]
	if !ok {
		return caddy.Upstream{}, &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	}
	_ = ownerID
	return up, nil
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	want := mustReadGolden(t, name)
	if string(got) != string(want) {
		t.Fatalf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func mustReadGolden(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	return data
}
