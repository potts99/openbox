// SPDX-License-Identifier: AGPL-3.0-only

package cloudinit_test

import (
	"strings"
	"testing"

	"github.com/openbox-dev/openbox/internal/cloudinit"
)

func TestOwnerKeyProducesRecognizableCloudConfig(t *testing.T) {
	data, err := cloudinit.OwnerKey("ssh-ed25519 AAAA owner@example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(data, "#cloud-config\n") {
		t.Fatalf("user data=%q", data)
	}
	if !strings.Contains(data, "ssh_authorized_keys:") || !strings.Contains(data, `"ssh-ed25519 AAAA owner@example"`) {
		t.Fatalf("owner key missing: %q", data)
	}
}

func TestOwnerKeyQuotesUntrustedTextAndRejectsEmpty(t *testing.T) {
	data, err := cloudinit.OwnerKey("ssh-ed25519 AAAA\npackages: [malicious]")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(data, "\npackages: [malicious]\n") {
		t.Fatalf("key escaped YAML scalar: %q", data)
	}
	if _, err := cloudinit.OwnerKey("  "); err == nil {
		t.Fatal("empty key accepted")
	}
}
