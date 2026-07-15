// SPDX-License-Identifier: AGPL-3.0-only

package networkpolicy_test

import (
	"reflect"
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
