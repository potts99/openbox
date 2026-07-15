// SPDX-License-Identifier: AGPL-3.0-only

package networkpolicy_test

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/networkpolicy"
)

func TestEvaluateConnectivityMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode domain.EgressMode
		dest networkpolicy.DestinationClass
		want networkpolicy.Decision
	}{
		{name: "standard denies host", mode: domain.EgressStandard, dest: networkpolicy.DestinationHost, want: networkpolicy.Deny},
		{name: "standard denies peer instance", mode: domain.EgressStandard, dest: networkpolicy.DestinationPeerInstance, want: networkpolicy.Deny},
		{name: "standard allows DNS", mode: domain.EgressStandard, dest: networkpolicy.DestinationDNS, want: networkpolicy.Allow},
		{name: "standard allows internet", mode: domain.EgressStandard, dest: networkpolicy.DestinationInternet, want: networkpolicy.Allow},
		{name: "standard allows LLM Gateway", mode: domain.EgressStandard, dest: networkpolicy.DestinationLLMGateway, want: networkpolicy.Allow},
		{name: "standard allows allowlist destination", mode: domain.EgressStandard, dest: networkpolicy.DestinationAllowlist, want: networkpolicy.Allow},
		{name: "restricted denies host", mode: domain.EgressRestricted, dest: networkpolicy.DestinationHost, want: networkpolicy.Deny},
		{name: "restricted denies peer instance", mode: domain.EgressRestricted, dest: networkpolicy.DestinationPeerInstance, want: networkpolicy.Deny},
		{name: "restricted allows DNS", mode: domain.EgressRestricted, dest: networkpolicy.DestinationDNS, want: networkpolicy.Allow},
		{name: "restricted denies internet", mode: domain.EgressRestricted, dest: networkpolicy.DestinationInternet, want: networkpolicy.Deny},
		{name: "restricted allows LLM Gateway", mode: domain.EgressRestricted, dest: networkpolicy.DestinationLLMGateway, want: networkpolicy.Allow},
		{name: "restricted allows allowlist destination", mode: domain.EgressRestricted, dest: networkpolicy.DestinationAllowlist, want: networkpolicy.Allow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := networkpolicy.Evaluate(tt.mode, tt.dest); got != tt.want {
				t.Fatalf("Evaluate(%q, %q) = %q, want %q", tt.mode, tt.dest, got, tt.want)
			}
		})
	}
}

func TestRestrictedACLNameUsesStableInstanceIdentity(t *testing.T) {
	t.Parallel()

	const instanceID = "instance_8e063b34-3194-42c1-a968-b9565b1b04f6"
	first := networkpolicy.RestrictedACLName(instanceID)
	second := networkpolicy.RestrictedACLName(instanceID)

	if first != second {
		t.Fatalf("RestrictedACLName(%q) is not stable: %q then %q", instanceID, first, second)
	}
	if first == networkpolicy.RestrictedACLName("instance_9e063b34-3194-42c1-a968-b9565b1b04f6") {
		t.Fatal("different instance IDs produced the same restricted ACL name")
	}
	if !regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{0,62}$`).MatchString(first) {
		t.Fatalf("RestrictedACLName(%q) = %q, want Incus-safe ACL name", instanceID, first)
	}
	if strings.HasSuffix(first, "-") {
		t.Fatalf("RestrictedACLName(%q) = %q, must not have a trailing dash", instanceID, first)
	}
	for _, guestIP := range []string{"10.42.0.17", "10.42.0.99"} {
		if strings.Contains(first, guestIP) {
			t.Fatalf("RestrictedACLName(%q) = %q contains guest IP %q", instanceID, first, guestIP)
		}
	}
}

func TestParseAllowedDestinationsNormalizesIPsAndCIDRs(t *testing.T) {
	t.Parallel()

	destinations, err := networkpolicy.ParseAllowedDestinations([]byte(`["203.0.113.9","198.51.100.29/24","2001:db8::8","2001:db8:1::5/64"]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"203.0.113.9", "198.51.100.0/24", "2001:db8::8", "2001:db8:1::/64"}
	if !reflect.DeepEqual(destinations, want) {
		t.Fatalf("destinations = %#v, want %#v", destinations, want)
	}
}

func TestParseAllowedDestinationsRejectsInvalidEntries(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		raw, errorText string
	}{
		{raw: `["packages.example.com"]`, errorText: "IP address or CIDR"},
		{raw: `["not an address"]`, errorText: "IP address or CIDR"},
		{raw: `{"destination":"203.0.113.9"}`, errorText: "parse allowed destinations"},
		{raw: `[42]`, errorText: "parse allowed destinations"},
	} {
		t.Run(test.raw, func(t *testing.T) {
			_, err := networkpolicy.ParseAllowedDestinations([]byte(test.raw))
			if err == nil || !strings.Contains(err.Error(), test.errorText) {
				t.Fatalf("ParseAllowedDestinations(%s) error = %v, want %q validation error", test.raw, err, test.errorText)
			}
		})
	}
}

func TestParseAllowlistHostnamesNormalizesExactNames(t *testing.T) {
	t.Parallel()

	hostnames, err := networkpolicy.ParseAllowlistHostnames([]byte(`["Packages.Example.COM.","api.example.com"]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"packages.example.com", "api.example.com"}
	if !reflect.DeepEqual(hostnames, want) {
		t.Fatalf("hostnames = %#v, want %#v", hostnames, want)
	}
}

func TestParseAllowlistHostnamesRejectsDangerousNames(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		`[""]`,
		`["*"]`,
		`["*.example.com"]`,
		`["not a hostname"]`,
		`["203.0.113.9"]`,
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := networkpolicy.ParseAllowlistHostnames([]byte(raw))
			if err == nil {
				t.Fatalf("ParseAllowlistHostnames(%s) unexpectedly succeeded", raw)
			}
		})
	}
}
