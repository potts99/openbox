// SPDX-License-Identifier: AGPL-3.0-only

package networkpolicy_test

import (
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
