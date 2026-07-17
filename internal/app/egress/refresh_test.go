// SPDX-License-Identifier: AGPL-3.0-only

package egress_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/egress"
	"github.com/openbox-dev/openbox/internal/dnsproxy"
	"github.com/openbox-dev/openbox/internal/domain"
)

type markerCapture struct {
	marked []domain.InstanceID
}

func (m *markerCapture) MarkInstanceError(_ context.Context, _ domain.OwnerID, id domain.InstanceID) error {
	m.marked = append(m.marked, id)
	return nil
}

type staticResolver struct {
	addresses []netip.Addr
	err       error
}

func (r staticResolver) Lookup(context.Context, string) (dnsproxy.LookupResult, error) {
	if r.err != nil {
		return dnsproxy.LookupResult{}, r.err
	}
	return dnsproxy.LookupResult{Addresses: r.addresses, TTL: time.Minute}, nil
}

func TestRefreshHostnameAllowlistsDoesNotMarkInstanceErrorOnFailure(t *testing.T) {
	store := openEgressStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	if err := store.EnsureSystemEgressProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOwner(ctx, domain.Owner{ID: "owner-1", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	custom, err := store.CreateEgressProfile(ctx, domain.EgressProfile{
		ID: "egress-hosts", Name: "hosts", Mode: domain.EgressRestricted,
		AllowedDestinationsJSON: []byte(`["example.com"]`), DNSPolicy: domain.DNSPolicyHostResolve,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	createBoundInstance(t, store, "inst-host", custom.ID, now)

	runtime := &recordingApplicatorRuntime{}
	app := egress.NewApplicator(nil, runtime)
	service, err := egress.New(store, app, egress.Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	marker := &markerCapture{}
	service.SetInstanceMarker(marker)

	report, err := service.RefreshHostnameAllowlists(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Refreshed != 0 || len(report.Errors) != 1 {
		t.Fatalf("report=%#v", report)
	}
	if len(marker.marked) != 0 {
		t.Fatalf("refresh must not mark instance error: %#v", marker.marked)
	}
}

func TestRefreshHostnameAllowlistsSucceedsWithResolver(t *testing.T) {
	store := openEgressStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	if err := store.EnsureSystemEgressProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOwner(ctx, domain.Owner{ID: "owner-1", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	custom, err := store.CreateEgressProfile(ctx, domain.EgressProfile{
		ID: "egress-hosts", Name: "hosts", Mode: domain.EgressRestricted,
		AllowedDestinationsJSON: []byte(`["example.com"]`), DNSPolicy: domain.DNSPolicyHostResolve,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	createBoundInstance(t, store, "inst-host", custom.ID, now)

	resolver, err := dnsproxy.NewAllowlistResolver(dnsproxy.Config{
		Resolver: staticResolver{addresses: []netip.Addr{netip.MustParseAddr("203.0.113.50")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &recordingApplicatorRuntime{}
	app := egress.NewApplicator(resolver, runtime)
	service, err := egress.New(store, app, egress.Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.RefreshHostnameAllowlists(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Refreshed != 1 || len(report.Errors) != 0 {
		t.Fatalf("report=%#v", report)
	}
	if len(runtime.applied) != 1 {
		t.Fatalf("applied=%v", runtime.applied)
	}

	// Second pass with the same resolved destinations must not reprogram ACLs
	// or count as refreshed (avoids reconcile-tick audit/Incus churn).
	report, err = service.RefreshHostnameAllowlists(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Refreshed != 0 || report.Skipped != 1 || len(report.Errors) != 0 {
		t.Fatalf("unchanged refresh report=%#v", report)
	}
	if len(runtime.applied) != 1 {
		t.Fatalf("applied after unchanged refresh=%v", runtime.applied)
	}
}
