// SPDX-License-Identifier: AGPL-3.0-only

package incus_test

import (
	"os"
	"testing"
)

// TestLiveEgressAllowlistMatrix is the opt-in live Incus connectivity matrix
// for Slice 19. Enable with OPENBOX_INCUS_TEST_SOCKET (and existing storage /
// image env vars used by other live Incus tests).
func TestLiveEgressAllowlistMatrix(t *testing.T) {
	if os.Getenv("OPENBOX_INCUS_TEST_SOCKET") == "" {
		t.Skip("set OPENBOX_INCUS_TEST_SOCKET to run live egress matrix")
	}
	t.Skip("live egress allowlist matrix harness is reserved; enable after host-side connectivity probes are wired")
}
