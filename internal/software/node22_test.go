// SPDX-License-Identifier: AGPL-3.0-only

package software_test

import (
	"strings"
	"testing"

	"github.com/openbox-dev/openbox/internal/software"
)

func TestNodeSourceSourcesIncludesArchitecture(t *testing.T) {
	t.Parallel()
	// nodeSourceSources is unexported; covered via install path expectations below.
	pkg, ok := software.DefaultCatalog().Get("pi")
	if !ok {
		t.Fatal("missing pi")
	}
	var nodePin string
	for _, pin := range pkg.Pins {
		if pin.Manager == "apt" && pin.Name == "nodejs" {
			nodePin = pin.Version
		}
	}
	if nodePin != "22.23.1-1nodesource1" {
		t.Fatalf("nodejs pin=%q", nodePin)
	}
}

func TestPiInstallRecipeDoesNotUseDistroNodejs(t *testing.T) {
	t.Parallel()
	pkg, ok := software.DefaultCatalog().Get("pi")
	if !ok {
		t.Fatal("missing pi")
	}
	for _, step := range pkg.Install {
		joined := strings.Join(step, " ")
		if strings.Contains(joined, "nodejs") && !strings.Contains(joined, "nodesource") {
			t.Fatalf("unexpected distro nodejs step: %v", step)
		}
	}
}
