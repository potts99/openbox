// SPDX-License-Identifier: AGPL-3.0-only

package sshgateway_test

import (
	"testing"

	"github.com/openbox-dev/openbox/internal/sshgateway"
)

func TestOpenBoxSSHGatewayDefaultPortIsNotHostSSH(t *testing.T) {
	t.Parallel()
	// Host recovery SSH stays on port 22; OpenBox never takes it by default.
	if sshgateway.DefaultAddress == ":22" || sshgateway.DefaultAddress == "0.0.0.0:22" {
		t.Fatalf("OpenBox gateway default %q must not claim host SSH port 22", sshgateway.DefaultAddress)
	}
	if sshgateway.DefaultAddress != ":2222" {
		t.Fatalf("DefaultAddress=%q, want :2222", sshgateway.DefaultAddress)
	}
}
