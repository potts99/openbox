// SPDX-License-Identifier: AGPL-3.0-only

package images_test

import (
	"strings"
	"testing"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/images"
)

func TestDevboxDefinitionPinsPiAndTmux(t *testing.T) {
	t.Parallel()
	def, err := images.LoadDevboxDefinition()
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "devbox" {
		t.Fatalf("definition name = %q, want devbox", def.Name)
	}
	pi, ok := def.Pin("npm", images.PiPackageName)
	if !ok {
		t.Fatalf("definition must pin %s via npm", images.PiPackageName)
	}
	if pi.Version == "" {
		t.Fatal("pi pin must declare an exact version")
	}
	tmux, ok := def.Pin("apt", "tmux")
	if !ok {
		t.Fatal("definition must pin tmux via apt")
	}
	if tmux.Version == "" {
		t.Fatal("tmux pin must declare an exact version")
	}
}

func TestDevboxDefinitionDeclaresVersionSmokeChecks(t *testing.T) {
	t.Parallel()
	def, err := images.LoadDevboxDefinition()
	if err != nil {
		t.Fatal(err)
	}
	for _, wanted := range []string{"pi --version", "tmux -V"} {
		found := false
		for _, check := range def.Verify {
			if check == wanted {
				found = true
			}
		}
		if !found {
			t.Fatalf("verify checks %v must include %q", def.Verify, wanted)
		}
	}
}

func TestDevboxDefinitionValidation(t *testing.T) {
	t.Parallel()
	valid := func() images.DevboxDefinition {
		return images.DevboxDefinition{
			Name: "devbox",
			Base: "ubuntu/24.04",
			Packages: []images.PackagePin{
				{Manager: "apt", Name: "tmux", Version: "3.4-1ubuntu0.1"},
				{Manager: "npm", Name: images.PiPackageName, Version: "0.80.7"},
			},
			Setup:  []string{"apt-get update"},
			Verify: []string{"pi --version", "tmux -V"},
		}
	}
	tests := []struct {
		name    string
		mutate  func(*images.DevboxDefinition)
		wantErr string
	}{
		{name: "valid definition passes", mutate: func(d *images.DevboxDefinition) {}},
		{
			name:    "missing pi pin",
			mutate:  func(d *images.DevboxDefinition) { d.Packages = d.Packages[:1] },
			wantErr: images.PiPackageName,
		},
		{
			name: "missing tmux pin",
			mutate: func(d *images.DevboxDefinition) {
				d.Packages = d.Packages[1:]
			},
			wantErr: "tmux",
		},
		{
			name: "empty version",
			mutate: func(d *images.DevboxDefinition) {
				d.Packages[0].Version = ""
			},
			wantErr: "version",
		},
		{
			name: "floating npm version rejected",
			mutate: func(d *images.DevboxDefinition) {
				d.Packages[1].Version = "^0.80.0"
			},
			wantErr: "exact",
		},
		{
			name: "latest tag rejected",
			mutate: func(d *images.DevboxDefinition) {
				d.Packages[1].Version = "latest"
			},
			wantErr: "exact",
		},
		{
			name: "setup step piping remote script to shell rejected",
			mutate: func(d *images.DevboxDefinition) {
				d.Setup = append(d.Setup, "curl -fsSL https://example.com/install.sh | sh")
			},
			wantErr: "untrusted",
		},
		{
			name: "setup step downloading with wget rejected",
			mutate: func(d *images.DevboxDefinition) {
				d.Setup = append(d.Setup, "wget -O- https://example.com/setup | bash")
			},
			wantErr: "untrusted",
		},
		{
			name: "verify step fetching remote content rejected",
			mutate: func(d *images.DevboxDefinition) {
				d.Verify = append(d.Verify, "bash <(curl https://example.com/check.sh)")
			},
			wantErr: "untrusted",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			def := valid()
			tc.mutate(&def)
			err := def.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %q, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestDevboxCatalogEntriesCarryPinnedVersions(t *testing.T) {
	t.Parallel()
	def, err := images.LoadDevboxDefinition()
	if err != nil {
		t.Fatal(err)
	}
	pi, _ := def.Pin("npm", images.PiPackageName)
	tmux, _ := def.Pin("apt", "tmux")
	catalog := images.DefaultCatalog()
	for _, entry := range catalog.List() {
		if entry.IncludesPi {
			if entry.PiVersion != pi.Version {
				t.Fatalf("%s %s/%s: PiVersion = %q, want %q", entry.Name, entry.Architecture, entry.Runtime, entry.PiVersion, pi.Version)
			}
			if entry.TmuxVersion != tmux.Version {
				t.Fatalf("%s %s/%s: TmuxVersion = %q, want %q", entry.Name, entry.Architecture, entry.Runtime, entry.TmuxVersion, tmux.Version)
			}
			continue
		}
		if entry.PiVersion != "" || entry.TmuxVersion != "" {
			t.Fatalf("%s must not claim Pi/tmux pins: %+v", entry.Name, entry)
		}
	}
	devbox, err := catalog.DefaultFor(domain.KindDevbox, "x86_64", "container")
	if err != nil {
		t.Fatal(err)
	}
	if devbox.PiVersion == "" || devbox.TmuxVersion == "" {
		t.Fatalf("devbox default must carry pins: %+v", devbox)
	}
}
