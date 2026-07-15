// SPDX-License-Identifier: AGPL-3.0-only

package routes_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/routes"
)

func TestValidateDNSPendingInvalidAndActive(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	repo.instances["inst-1"] = domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "incus-1"}
	resolver := &fakeDNS{
		answers: map[string][]net.IP{},
	}
	svc, err := routes.New(repo, routes.Options{
		Now:         func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) },
		NewID:       func() string { return "route-1" },
		DNS:         resolver,
		ExpectedIPs: []net.IP{net.ParseIP("203.0.113.10")},
	})
	if err != nil {
		t.Fatal(err)
	}

	created, err := svc.Create(context.Background(), "owner-1", routes.CreateInput{
		InstanceID: "inst-1", Hostname: "app.example.com", TargetPort: 3000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.TLSState != routes.TLSStateNone {
		t.Fatalf("tls_state=%q", created.TLSState)
	}

	// No DNS answers yet → pending.
	resolver.answers["app.example.com"] = nil
	pending, err := svc.ValidateDNS(context.Background(), "owner-1", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.TLSState != routes.TLSStatePending {
		t.Fatalf("want pending, got %q", pending.TLSState)
	}

	// Wrong IP → invalid.
	resolver.answers["app.example.com"] = []net.IP{net.ParseIP("198.51.100.1")}
	invalid, err := svc.ValidateDNS(context.Background(), "owner-1", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if invalid.TLSState != routes.TLSStateInvalid {
		t.Fatalf("want invalid, got %q", invalid.TLSState)
	}

	// Matching IP → active.
	resolver.answers["app.example.com"] = []net.IP{net.ParseIP("203.0.113.10")}
	active, err := svc.ValidateDNS(context.Background(), "owner-1", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if active.TLSState != routes.TLSStateActive {
		t.Fatalf("want active, got %q", active.TLSState)
	}
}

func TestValidateDNSRequiresExpectedIPs(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	repo.instances["inst-1"] = domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "incus-1"}
	svc, err := routes.New(repo, routes.Options{
		Now:   func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) },
		NewID: func() string { return "route-1" },
		DNS:   &fakeDNS{answers: map[string][]net.IP{"app.example.com": {net.ParseIP("203.0.113.10")}}},
		// ExpectedIPs empty → validation reports pending with actionable state.
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.Create(context.Background(), "owner-1", routes.CreateInput{
		InstanceID: "inst-1", Hostname: "app.example.com", TargetPort: 3000,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.ValidateDNS(context.Background(), "owner-1", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TLSState != routes.TLSStatePending {
		t.Fatalf("without expected IPs want pending, got %q", got.TLSState)
	}
}

type fakeDNS struct {
	answers map[string][]net.IP
}

func (f *fakeDNS) LookupIP(_ context.Context, host string) ([]net.IP, error) {
	ips, ok := f.answers[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return ips, nil
}
