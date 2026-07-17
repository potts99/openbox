// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"testing"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/networkpolicy"
)

func TestNICACLsMatchEvaluatePublicInternetMatrix(t *testing.T) {
	t.Parallel()
	restrictedName := networkpolicy.RestrictedACLName("inst-contract")

	standard := NICACLs(domain.EgressStandard)
	if !networkpolicy.AllowsPublicInternet(domain.EgressStandard) {
		t.Fatal("standard mode must allow public internet per Evaluate")
	}
	if len(standard) != 2 || standard[1] != StandardEgressACLName {
		t.Fatalf("standard ACLs=%v", standard)
	}

	restricted := NICACLs(domain.EgressRestricted, restrictedName)
	if networkpolicy.AllowsPublicInternet(domain.EgressRestricted) {
		t.Fatal("restricted mode must deny public internet per Evaluate")
	}
	for _, name := range restricted {
		if name == StandardEgressACLName {
			t.Fatalf("restricted ACLs must not include %s: %v", StandardEgressACLName, restricted)
		}
	}
	if len(restricted) != 2 || restricted[1] != restrictedName {
		t.Fatalf("restricted ACLs=%v", restricted)
	}
}
