// SPDX-License-Identifier: AGPL-3.0-only

package reuse_test

import (
	"testing"

	"github.com/openbox-dev/openbox/internal/app/reuse"
	"github.com/openbox-dev/openbox/internal/domain"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestClassifyStorage(t *testing.T) {
	t.Parallel()
	if got := reuse.ClassifyStorage(nil); got != reuse.StorageUnknown {
		t.Fatalf("got=%s", got)
	}
	if got := reuse.ClassifyStorage([]string{"dir"}); got != reuse.StorageNotSupported {
		t.Fatalf("got=%s", got)
	}
	if got := reuse.ClassifyStorage([]string{"dir", "zfs"}); got != reuse.StorageConfirmed {
		t.Fatalf("got=%s", got)
	}
}

func TestClassifyStorageDriverUsesConfiguredPool(t *testing.T) {
	t.Parallel()
	if got := reuse.ClassifyStorageDriver("dir"); got != reuse.StorageNotSupported {
		t.Fatalf("dir=%s", got)
	}
	if got := reuse.ClassifyStorageDriver("zfs"); got != reuse.StorageConfirmed {
		t.Fatalf("zfs=%s", got)
	}
	if got := reuse.ClassifyStorageDriver(""); got != reuse.StorageUnknown {
		t.Fatalf("empty=%s", got)
	}
}

func TestPreflightWarnings(t *testing.T) {
	t.Parallel()
	source := domain.Instance{Protected: false}
	software := []domain.InstanceSoftware{{PackageID: "pi", Status: domain.SoftwareInstalled}}
	efficiency, warnings := reuse.Preflight(runtimeapi.Capabilities{StorageDrivers: []string{"dir"}}, source, software)
	if efficiency != reuse.StorageNotSupported {
		t.Fatalf("efficiency=%s", efficiency)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings=%v", warnings)
	}
	protected := domain.Instance{Protected: true}
	efficiency, warnings = reuse.Preflight(runtimeapi.Capabilities{StorageDrivers: []string{"zfs"}}, protected, software)
	if efficiency != reuse.StorageConfirmed || len(warnings) != 0 {
		t.Fatalf("efficiency=%s warnings=%v", efficiency, warnings)
	}
}

func TestAuthorizedKeysPreservesGatewayAccess(t *testing.T) {
	t.Parallel()
	if got := reuse.AuthorizedKeys("ssh-ed25519 owner", "ssh-ed25519 gateway"); got != "ssh-ed25519 owner\nssh-ed25519 gateway" {
		t.Fatalf("keys=%q", got)
	}
}
