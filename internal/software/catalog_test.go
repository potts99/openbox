// SPDX-License-Identifier: AGPL-3.0-only

package software_test

import (
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
