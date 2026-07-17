// SPDX-License-Identifier: AGPL-3.0-only

package domain_test

import (
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

func TestEgressProfileValidate(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0).UTC()
	p := domain.EgressProfile{
		ID:                      "prof-1",
		Name:                    "restricted",
		Mode:                    domain.EgressRestricted,
		AllowedDestinationsJSON: []byte(`["1.2.3.4","packages.example.com"]`),
		DNSPolicy:               domain.DNSPolicyHostResolve,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	literals, hostnames, err := domain.ParseAllowlistEntries(p.AllowedDestinationsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(literals) != 1 || literals[0] != "1.2.3.4" {
		t.Fatalf("literals=%v", literals)
	}
	if len(hostnames) != 1 || hostnames[0] != "packages.example.com" {
		t.Fatalf("hostnames=%v", hostnames)
	}
}

func TestEgressProfileRejectsWildcardHostname(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0).UTC()
	p := domain.EgressProfile{
		ID:                      "prof-1",
		Name:                    "custom",
		Mode:                    domain.EgressRestricted,
		AllowedDestinationsJSON: []byte(`["*.example.com"]`),
		DNSPolicy:               domain.DNSPolicyHostResolve,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestDefaultEgressProfileID(t *testing.T) {
	t.Parallel()
	if got := domain.DefaultEgressProfileID(domain.KindSandbox); got != domain.EgressProfileIDRestricted {
		t.Fatalf("sandbox default=%q", got)
	}
	if got := domain.DefaultEgressProfileID(domain.KindVPS); got != domain.EgressProfileIDStandard {
		t.Fatalf("vps default=%q", got)
	}
}

func TestRestrictedProfileRejectsBypassAllowlistLiterals(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0).UTC()
	for _, destinations := range []string{
		`["0.0.0.0/0"]`,
		`["10.0.0.0/8"]`,
		`["10.42.0.0/24"]`,
		`["192.168.1.1"]`,
		`["127.0.0.1"]`,
		`["100.64.0.1"]`,
	} {
		p := domain.EgressProfile{
			ID: "prof-1", Name: "restricted", Mode: domain.EgressRestricted,
			AllowedDestinationsJSON: []byte(destinations), DNSPolicy: domain.DNSPolicyHostResolve,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := p.Validate(); err == nil {
			t.Fatalf("expected rejection for %s", destinations)
		}
	}
}

func TestRestrictedProfileAcceptsPublicAllowlistLiterals(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0).UTC()
	p := domain.EgressProfile{
		ID: "prof-1", Name: "restricted", Mode: domain.EgressRestricted,
		AllowedDestinationsJSON: []byte(`["203.0.113.10","1.1.1.1/32"]`),
		DNSPolicy:               domain.DNSPolicyHostResolve,
		CreatedAt:               now, UpdatedAt: now,
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}
