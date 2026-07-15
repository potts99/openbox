// SPDX-License-Identifier: AGPL-3.0-only

package routes_test

import (
	"context"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/routes"
)

func TestCertificateAllowedForPersistedRouteHostname(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	repo.instances["inst-1"] = domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "incus-1"}
	svc := newTestService(t, repo)

	created, err := svc.Create(context.Background(), "owner-1", routes.CreateInput{
		InstanceID: "inst-1", Hostname: "dev.openbox.example", TargetPort: 3000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Visibility != domain.RoutePrivate {
		t.Fatalf("fixture visibility=%q", created.Visibility)
	}

	allowed, err := svc.CertificateAllowed(context.Background(), "dev.openbox.example")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("approved private route hostname denied")
	}

	// Public routes are also approved for TLS.
	if _, err := svc.Publish(context.Background(), "owner-1", created.ID); err != nil {
		t.Fatal(err)
	}
	allowed, err = svc.CertificateAllowed(context.Background(), "dev.openbox.example")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("approved public route hostname denied")
	}
}

func TestCertificateDeniedForUnknownHostname(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, newFakeRepo())

	allowed, err := svc.CertificateAllowed(context.Background(), "unknown.openbox.example")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("unknown hostname allowed")
	}
}

func TestCertificateDeniedAfterRouteDeleted(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	repo.instances["inst-1"] = domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "incus-1"}
	svc := newTestService(t, repo)

	created, err := svc.Create(context.Background(), "owner-1", routes.CreateInput{
		InstanceID: "inst-1", Hostname: "gone.openbox.example", TargetPort: 3000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(context.Background(), "owner-1", created.ID); err != nil {
		t.Fatal(err)
	}

	allowed, err := svc.CertificateAllowed(context.Background(), "gone.openbox.example")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("deleted route hostname still allowed")
	}
}

func TestCertificateAllowlistAbuseRejected(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	repo.instances["inst-1"] = domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "incus-1"}
	svc := newTestService(t, repo)
	_, err := svc.Create(context.Background(), "owner-1", routes.CreateInput{
		InstanceID: "inst-1", Hostname: "legit.openbox.example", TargetPort: 3000,
	})
	if err != nil {
		t.Fatal(err)
	}

	abuse := []string{
		"",
		"   ",
		"evil.example",
		"google.com",
		"legit.openbox.example.evil.com",
		"*.openbox.example",
		"legit.openbox.example/admin",
		"legit.openbox.example\x00.evil.com",
		"' OR 1=1 --",
		"legit.openbox.example'; DROP TABLE routes;--",
		"../../etc/passwd",
		"http://legit.openbox.example",
		"legit.openbox.example:443",
		string(make([]byte, 300)),
	}
	for _, hostname := range abuse {
		allowed, err := svc.CertificateAllowed(context.Background(), hostname)
		if err != nil {
			t.Fatalf("hostname %q: unexpected error %v", hostname, err)
		}
		if allowed {
			t.Fatalf("abuse hostname %q allowed", hostname)
		}
	}
}

func TestCertificateAllowedIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	repo.routes["route-1"] = domain.Route{
		ID: "route-1", OwnerID: "owner-1", InstanceID: "inst-1",
		Hostname: "Dev.OpenBox.Example", TargetPort: 3000,
		Visibility: domain.RoutePrivate, TLSState: routes.TLSStateNone,
		CreatedAt: now, UpdatedAt: now,
	}
	svc := newTestService(t, repo)

	allowed, err := svc.CertificateAllowed(context.Background(), "dev.openbox.example")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("case-insensitive approved hostname denied")
	}
}
