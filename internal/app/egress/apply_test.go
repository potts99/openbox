// SPDX-License-Identifier: AGPL-3.0-only

package egress_test

import (
	"context"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/egress"
	"github.com/openbox-dev/openbox/internal/dnsproxy"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/networkpolicy"
	"github.com/openbox-dev/openbox/internal/runtime/incus"
)

type fakePolicyRuntime struct {
	ensuredName string
	ensuredDest []string
	nicACLs     []string
	applyErr    error
	resolution  domain.AllowlistResolution
}

func (f *fakePolicyRuntime) ProgramNetworkPolicy(_ context.Context, apply egress.PolicyApply) error {
	if apply.Instance.EgressMode == domain.EgressRestricted || apply.Mode == domain.EgressRestricted {
		f.ensuredName = networkpolicy.RestrictedACLName(string(apply.Instance.ID))
		f.ensuredDest = append([]string(nil), apply.Destinations...)
		f.nicACLs = incus.NICACLs(domain.EgressRestricted, f.ensuredName)
	} else {
		f.nicACLs = incus.NICACLs(domain.EgressStandard)
	}
	f.resolution = apply.Resolution
	return f.applyErr
}

func (f *fakePolicyRuntime) SetAllowlistResolution(_ domain.InstanceID, resolution domain.AllowlistResolution) {
	f.resolution = resolution
}

type stubResolver struct {
	addresses map[string][]netip.Addr
	err       error
}

func (s stubResolver) Lookup(_ context.Context, hostname string) (dnsproxy.LookupResult, error) {
	if s.err != nil {
		return dnsproxy.LookupResult{}, s.err
	}
	addrs, ok := s.addresses[hostname]
	if !ok {
		return dnsproxy.LookupResult{}, context.Canceled
	}
	return dnsproxy.LookupResult{Addresses: addrs, TTL: time.Minute}, nil
}

func TestApplyRestrictedWithHostnameProgramsACL(t *testing.T) {
	t.Parallel()
	fake := &fakePolicyRuntime{}
	resolver, err := dnsproxy.NewAllowlistResolver(dnsproxy.Config{
		Resolver: stubResolver{addresses: map[string][]netip.Addr{
			"packages.example.com": {netip.MustParseAddr("203.0.113.10")},
		}},
		MinTTL: time.Second,
		MaxTTL: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	app := egress.NewApplicator(resolver, fake)
	err = app.Apply(context.Background(), domain.Instance{
		ID: "inst-1", RuntimeRef: "ref-1", EgressMode: domain.EgressRestricted,
	}, domain.EgressProfile{
		Mode:                    domain.EgressRestricted,
		AllowedDestinationsJSON: []byte(`["packages.example.com"]`),
		DNSPolicy:               domain.DNSPolicyHostResolve,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantACL := networkpolicy.RestrictedACLName("inst-1")
	if fake.ensuredName != wantACL {
		t.Fatalf("ensured ACL=%q want %q", fake.ensuredName, wantACL)
	}
	if !reflect.DeepEqual(fake.ensuredDest, []string{"203.0.113.10"}) {
		t.Fatalf("destinations=%v", fake.ensuredDest)
	}
	wantNIC := []string{incus.DefaultDenyACLName, wantACL}
	if !reflect.DeepEqual(fake.nicACLs, wantNIC) {
		t.Fatalf("nic ACLs=%v want %v", fake.nicACLs, wantNIC)
	}
	if fake.resolution.State != "resolved" || !reflect.DeepEqual(fake.resolution.Resolved, []string{"packages.example.com"}) {
		t.Fatalf("resolution=%#v", fake.resolution)
	}
}

func TestApplyRebindingHostnameFailsClosed(t *testing.T) {
	t.Parallel()
	fake := &fakePolicyRuntime{}
	resolver, err := dnsproxy.NewAllowlistResolver(dnsproxy.Config{
		Resolver: stubResolver{addresses: map[string][]netip.Addr{
			"evil.example.com": {netip.MustParseAddr("10.0.0.1")},
		}},
		MinTTL: time.Second,
		MaxTTL: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	app := egress.NewApplicator(resolver, fake)
	err = app.Apply(context.Background(), domain.Instance{
		ID: "inst-1", RuntimeRef: "ref-1", EgressMode: domain.EgressRestricted,
	}, domain.EgressProfile{
		Mode:                    domain.EgressRestricted,
		AllowedDestinationsJSON: []byte(`["evil.example.com"]`),
		DNSPolicy:               domain.DNSPolicyHostResolve,
	})
	if err == nil {
		t.Fatal("expected rebinding failure")
	}
	if fake.resolution.State != "failed" || !reflect.DeepEqual(fake.resolution.Failed, []string{"evil.example.com"}) {
		t.Fatalf("resolution=%#v", fake.resolution)
	}
	if fake.ensuredName != "" {
		t.Fatal("should not program ACL after resolve failure")
	}
}
