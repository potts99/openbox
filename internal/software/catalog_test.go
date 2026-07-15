// SPDX-License-Identifier: AGPL-3.0-only

package software_test

import (
	"strings"
	"testing"

	"github.com/openbox-dev/openbox/internal/images"
	"github.com/openbox-dev/openbox/internal/software"
)

func TestDefaultCatalogIncludesPiWithExactPins(t *testing.T) {
	t.Parallel()
	cat := software.DefaultCatalog()
	pkg, ok := cat.Get("pi")
	if !ok {
		t.Fatal("missing pi")
	}
	if err := pkg.Validate(); err != nil {
		t.Fatal(err)
	}
	if pkg.ID != "pi" || len(pkg.Install) == 0 || len(pkg.Verify) == 0 {
		t.Fatalf("%+v", pkg)
	}
}

func TestPiPinsMatchDevboxDefinition(t *testing.T) {
	t.Parallel()
	def, err := images.LoadDevboxDefinition()
	if err != nil {
		t.Fatal(err)
	}
	pkg, ok := software.DefaultCatalog().Get("pi")
	if !ok {
		t.Fatal("missing pi")
	}
	for _, want := range def.Packages {
		found := false
		for _, got := range pkg.Pins {
			if got.Manager == want.Manager && got.Name == want.Name && got.Version == want.Version {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("catalog pi missing pin %+v", want)
		}
	}
}

func TestPackageValidateRejectsRangeVersions(t *testing.T) {
	t.Parallel()
	pkg := software.Package{
		ID:          "bad",
		Name:        "Bad",
		Description: "test",
		Pins: []software.Pin{{
			Manager: "npm",
			Name:    "x",
			Version: "^1.0.0",
		}},
		Install: [][]string{{"true"}},
		Verify:  [][]string{{"true"}},
	}
	if err := pkg.Validate(); err == nil {
		t.Fatal("expected range version rejection")
	}
}

func TestValidateAcceptsGitHubReleasePins(t *testing.T) {
	t.Parallel()
	pkg := software.Package{
		ID:   "herdr",
		Name: "Herdr",
		Pins: []software.Pin{{
			Manager: "github-release",
			Name:    "ogulcancelik/herdr",
			Version: "0.7.4",
			Assets: []software.ReleaseAsset{
				{Arch: "x86_64", Filename: "herdr-linux-x86_64", SHA256: strings.Repeat("a", 64)},
				{Arch: "aarch64", Filename: "herdr-linux-aarch64", SHA256: strings.Repeat("b", 64)},
			},
		}},
		Verify: [][]string{{"herdr", "--version"}},
	}
	if err := pkg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsGitHubReleaseMissingArch(t *testing.T) {
	t.Parallel()
	pkg := software.Package{
		ID:   "herdr",
		Name: "Herdr",
		Pins: []software.Pin{{
			Manager: "github-release",
			Name:    "ogulcancelik/herdr",
			Version: "0.7.4",
			Assets: []software.ReleaseAsset{
				{Arch: "x86_64", Filename: "herdr-linux-x86_64", SHA256: strings.Repeat("a", 64)},
			},
		}},
		Verify: [][]string{{"herdr", "--version"}},
	}
	if err := pkg.Validate(); err == nil {
		t.Fatal("expected missing aarch64 rejection")
	}
}

func TestValidateStillRejectsRemoteScriptSteps(t *testing.T) {
	t.Parallel()
	pkg := software.Package{
		ID:   "bad",
		Name: "Bad",
		Pins: []software.Pin{{
			Manager: "apt",
			Name:    "tmux",
			Version: "3.4-1",
		}},
		Install: [][]string{{"curl", "-fsSL", "https://example.com/install.sh"}},
		Verify:  [][]string{{"true"}},
	}
	if err := pkg.Validate(); err == nil {
		t.Fatal("expected remote script rejection")
	}
}

func TestDefaultCatalogIncludesHerdr(t *testing.T) {
	t.Parallel()
	pkg, ok := software.DefaultCatalog().Get("herdr")
	if !ok {
		t.Fatal("missing herdr")
	}
	if err := pkg.Validate(); err != nil {
		t.Fatal(err)
	}
	if pkg.ID != "herdr" || len(pkg.Verify) == 0 {
		t.Fatalf("%+v", pkg)
	}
	pin := pkg.Pins[0]
	if pin.Manager != "github-release" || pin.Name != "ogulcancelik/herdr" || pin.Version != "0.7.4" {
		t.Fatalf("pin=%+v", pin)
	}
	want := map[string]string{
		"x86_64":  "bc0fc02d4ba500f9cac2353a43e67fe036785ecca6eb55378e050fac3c103059",
		"aarch64": "544e0002de42806d1ab64ccdef3a7e7414f24717b0b6b022bc9e57d2eefd26a2",
	}
	for arch, sum := range want {
		found := false
		for _, a := range pin.Assets {
			if a.Arch == arch && a.SHA256 == sum {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %s digest", arch)
		}
	}
}
