// SPDX-License-Identifier: AGPL-3.0-only

package images_test

import (
	"testing"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/images"
)

func TestCuratedManifestsCoverKindsAndArchitectures(t *testing.T) {
	t.Parallel()
	catalog := images.DefaultCatalog()
	for _, kind := range []domain.InstanceKind{domain.KindVPS, domain.KindSandbox, domain.KindDevbox} {
		for _, arch := range []string{"x86_64", "aarch64"} {
			for _, runtime := range []string{"container", "virtual-machine"} {
				entry, err := catalog.DefaultFor(kind, arch, runtime)
				if err != nil {
					t.Fatalf("kind=%s arch=%s runtime=%s: %v", kind, arch, runtime, err)
				}
				if entry.Alias == "" || entry.Architecture != arch || entry.Runtime != runtime {
					t.Fatalf("unexpected entry: %+v", entry)
				}
				if !entry.CloudInit {
					t.Fatalf("curated images must advertise cloud-init: %+v", entry)
				}
			}
		}
	}
}

func TestCuratedDefaultsDifferByWorkflow(t *testing.T) {
	t.Parallel()
	catalog := images.DefaultCatalog()
	general, err := catalog.DefaultFor(domain.KindVPS, "x86_64", "container")
	if err != nil {
		t.Fatal(err)
	}
	sandbox, err := catalog.DefaultFor(domain.KindSandbox, "x86_64", "container")
	if err != nil {
		t.Fatal(err)
	}
	devbox, err := catalog.DefaultFor(domain.KindDevbox, "x86_64", "container")
	if err != nil {
		t.Fatal(err)
	}
	if general.Alias == sandbox.Alias || general.Alias == devbox.Alias || sandbox.Alias == devbox.Alias {
		t.Fatalf("workflow defaults must be distinct: general=%q sandbox=%q devbox=%q", general.Alias, sandbox.Alias, devbox.Alias)
	}
	if !devbox.IncludesPi {
		t.Fatal("devbox curated image must include Pi")
	}
	if sandbox.IncludesPi || general.IncludesPi {
		t.Fatal("only the Devbox curated image includes Pi")
	}
}

func TestCatalogRejectsUnsupportedCombinations(t *testing.T) {
	t.Parallel()
	catalog := images.DefaultCatalog()
	if _, err := catalog.DefaultFor(domain.KindVPS, "riscv64", "container"); err == nil {
		t.Fatal("expected unsupported architecture error")
	}
	if _, err := catalog.DefaultFor(domain.InstanceKind("weird"), "x86_64", "container"); err == nil {
		t.Fatal("expected unsupported kind error")
	}
}
