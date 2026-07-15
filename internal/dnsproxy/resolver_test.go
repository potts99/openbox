// SPDX-License-Identifier: AGPL-3.0-only

package dnsproxy_test

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/clock"
	"github.com/openbox-dev/openbox/internal/dnsproxy"
)

func TestAllowlistResolverKeepsPublicIPv4AndIPv6(t *testing.T) {
	t.Parallel()

	resolver := &fakeResolver{responses: []response{{
		result: dnsproxy.LookupResult{
			Addresses: []netip.Addr{
				netip.MustParseAddr("198.51.100.7"),
				netip.MustParseAddr("2001:db8::7"),
			},
			TTL: time.Minute,
		},
	}}}
	allowlist, _ := newAllowlistResolver(t, resolver, time.Unix(0, 0), time.Second, 5*time.Minute)

	addresses, err := allowlist.Resolve(context.Background(), "packages.example.com")
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Addr{netip.MustParseAddr("198.51.100.7"), netip.MustParseAddr("2001:db8::7")}
	if !reflect.DeepEqual(addresses, want) {
		t.Fatalf("addresses = %v, want %v", addresses, want)
	}
}

func TestAllowlistResolverRejectsRebindingAddresses(t *testing.T) {
	t.Parallel()

	for _, address := range []string{
		"127.0.0.1",
		"10.42.0.8",
		"10.0.0.8",
		"169.254.1.8",
		"100.64.1.8",
		"224.0.0.1",
		"0.0.0.0",
		"255.255.255.255",
		"::1",
		"fc00::8",
		"fe80::8",
		"ff00::8",
		"::",
	} {
		t.Run(address, func(t *testing.T) {
			resolver := &fakeResolver{responses: []response{{
				result: dnsproxy.LookupResult{
					Addresses: []netip.Addr{netip.MustParseAddr(address)},
					TTL:       time.Minute,
				},
			}}}
			allowlist, _ := newAllowlistResolver(t, resolver, time.Unix(0, 0), time.Second, 5*time.Minute)

			_, err := allowlist.Resolve(context.Background(), "packages.example.com")
			if err == nil {
				t.Fatalf("Resolve accepted rebinding address %s", address)
			}
		})
	}
}

func TestAllowlistResolverRefreshesAtBoundedTTL(t *testing.T) {
	t.Parallel()

	now := time.Unix(0, 0)
	resolver := &fakeResolver{responses: []response{
		{result: dnsproxy.LookupResult{Addresses: []netip.Addr{netip.MustParseAddr("198.51.100.7")}, TTL: time.Hour}},
		{result: dnsproxy.LookupResult{Addresses: []netip.Addr{netip.MustParseAddr("198.51.100.8")}, TTL: time.Hour}},
	}}
	allowlist, fakeClock := newAllowlistResolver(t, resolver, now, time.Second, time.Minute)

	first, err := allowlist.Resolve(context.Background(), "packages.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got := resolver.calls; got != 1 {
		t.Fatalf("lookup calls = %d, want 1", got)
	}

	fakeClock.Advance(59 * time.Second)
	cached, err := allowlist.Resolve(context.Background(), "packages.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cached, first) || resolver.calls != 1 {
		t.Fatalf("cached resolve = %v after %d calls, want %v after 1 call", cached, resolver.calls, first)
	}

	fakeClock.Advance(time.Second)
	refreshed, err := allowlist.Resolve(context.Background(), "packages.example.com")
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Addr{netip.MustParseAddr("198.51.100.8")}
	if !reflect.DeepEqual(refreshed, want) || resolver.calls != 2 {
		t.Fatalf("refreshed resolve = %v after %d calls, want %v after 2 calls", refreshed, resolver.calls, want)
	}
}

func TestAllowlistResolverHonorsMinimumRefreshInterval(t *testing.T) {
	t.Parallel()

	now := time.Unix(0, 0)
	resolver := &fakeResolver{responses: []response{
		{result: dnsproxy.LookupResult{Addresses: []netip.Addr{netip.MustParseAddr("198.51.100.7")}, TTL: time.Second}},
		{result: dnsproxy.LookupResult{Addresses: []netip.Addr{netip.MustParseAddr("198.51.100.8")}, TTL: time.Second}},
	}}
	allowlist, fakeClock := newAllowlistResolver(t, resolver, now, time.Minute, 5*time.Minute)

	if _, err := allowlist.Resolve(context.Background(), "packages.example.com"); err != nil {
		t.Fatal(err)
	}
	fakeClock.Advance(59 * time.Second)
	if _, err := allowlist.Resolve(context.Background(), "packages.example.com"); err != nil {
		t.Fatal(err)
	}
	if got := resolver.calls; got != 1 {
		t.Fatalf("lookup calls = %d, want 1 before minimum refresh interval", got)
	}
}

func TestAllowlistResolverFailsClosedWhenExpiredRefreshFails(t *testing.T) {
	t.Parallel()

	now := time.Unix(0, 0)
	resolver := &fakeResolver{responses: []response{
		{result: dnsproxy.LookupResult{Addresses: []netip.Addr{netip.MustParseAddr("198.51.100.7")}, TTL: time.Second}},
		{err: errors.New("DNS unavailable")},
	}}
	allowlist, fakeClock := newAllowlistResolver(t, resolver, now, time.Second, time.Minute)

	if _, err := allowlist.Resolve(context.Background(), "packages.example.com"); err != nil {
		t.Fatal(err)
	}
	fakeClock.Advance(time.Second)
	if _, err := allowlist.Resolve(context.Background(), "packages.example.com"); err == nil {
		t.Fatal("Resolve succeeded with stale addresses after failed refresh")
	}
}

func newAllowlistResolver(t *testing.T, resolver dnsproxy.Resolver, now time.Time, minTTL, maxTTL time.Duration) (*dnsproxy.AllowlistResolver, *clock.Fake) {
	t.Helper()

	fakeClock := clock.NewFake(now)
	instance, err := dnsproxy.NewAllowlistResolver(dnsproxy.Config{
		Resolver: resolver,
		Clock:    fakeClock,
		MinTTL:   minTTL,
		MaxTTL:   maxTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return instance, fakeClock
}

type response struct {
	result dnsproxy.LookupResult
	err    error
}

type fakeResolver struct {
	responses []response
	calls     int
}

func (r *fakeResolver) Lookup(_ context.Context, _ string) (dnsproxy.LookupResult, error) {
	response := r.responses[r.calls]
	r.calls++
	return response.result, response.err
}
