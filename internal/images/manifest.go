// SPDX-License-Identifier: AGPL-3.0-only

package images

import (
	"fmt"

	"github.com/openbox-dev/openbox/internal/domain"
)

// Manifest describes a curated OpenBox image alias and its compatibility.
// Aliases are resolved to immutable fingerprints at create time (see Resolve).
type Manifest struct {
	Name         string // human label: general, sandbox, or devbox
	Alias        string // Incus alias OpenBox asks the runtime to resolve
	Architecture string // x86_64 or aarch64
	Runtime      string // container or virtual-machine
	CloudInit    bool
	IncludesPi   bool
	PiVersion    string // pinned Pi CLI version; set only when IncludesPi
	TmuxVersion  string // pinned tmux version; set only when IncludesPi
}

// Catalog is the curated set of workflow defaults for v0.1.
type Catalog struct {
	entries []Manifest
}

// DefaultCatalog returns the built-in general, sandbox, and Devbox manifests.
// Devbox entries carry the Pi and tmux versions from the checked-in definition.
func DefaultCatalog() Catalog {
	entries := make([]Manifest, len(curatedManifests))
	copy(entries, curatedManifests)
	def, err := LoadDevboxDefinition()
	if err != nil {
		// The definition is embedded at compile time; failing to load it is a
		// programmer error, not a runtime condition.
		panic("invalid embedded devbox definition: " + err.Error())
	}
	pi, _ := def.Pin("npm", PiPackageName)
	tmux, _ := def.Pin("apt", "tmux")
	for i := range entries {
		if !entries[i].IncludesPi {
			continue
		}
		entries[i].PiVersion = pi.Version
		entries[i].TmuxVersion = tmux.Version
	}
	return Catalog{entries: entries}
}

// DefaultFor selects the curated alias for a workflow kind, host architecture,
// and runtime image type.
func (c Catalog) DefaultFor(kind domain.InstanceKind, architecture, runtime string) (Manifest, error) {
	name, err := workflowName(kind)
	if err != nil {
		return Manifest{}, err
	}
	for _, entry := range c.entries {
		if entry.Name == name && entry.Architecture == architecture && entry.Runtime == runtime {
			return entry, nil
		}
	}
	return Manifest{}, fmt.Errorf("no curated %s image for architecture %q runtime %q", name, architecture, runtime)
}

// List returns a copy of curated manifests.
func (c Catalog) List() []Manifest {
	out := make([]Manifest, len(c.entries))
	copy(out, c.entries)
	return out
}

func workflowName(kind domain.InstanceKind) (string, error) {
	switch kind {
	case domain.KindVPS:
		return "general", nil
	case domain.KindSandbox:
		return "sandbox", nil
	default:
		return "", fmt.Errorf("unsupported instance kind %q", kind)
	}
}

// curatedManifests pins v0.1 workflow aliases. Fingerprints are not listed here
// because Incus aliases move; create-time Resolve persists the digest.
// Curated aliases: Incus image alias names are unique, so container and VM
// images cannot share one alias. VM entries use a "/vm" suffix; ResolveForType
// also accepts the base alias when a matching "/vm" image exists.
var curatedManifests = []Manifest{
	{Name: "general", Alias: "openbox:general/ubuntu/24.04", Architecture: "x86_64", Runtime: "container", CloudInit: true},
	{Name: "general", Alias: "openbox:general/ubuntu/24.04/vm", Architecture: "x86_64", Runtime: "virtual-machine", CloudInit: true},
	{Name: "general", Alias: "openbox:general/ubuntu/24.04", Architecture: "aarch64", Runtime: "container", CloudInit: true},
	{Name: "general", Alias: "openbox:general/ubuntu/24.04/vm", Architecture: "aarch64", Runtime: "virtual-machine", CloudInit: true},

	{Name: "sandbox", Alias: "openbox:sandbox/ubuntu/24.04", Architecture: "x86_64", Runtime: "container", CloudInit: true},
	{Name: "sandbox", Alias: "openbox:sandbox/ubuntu/24.04/vm", Architecture: "x86_64", Runtime: "virtual-machine", CloudInit: true},
	{Name: "sandbox", Alias: "openbox:sandbox/ubuntu/24.04", Architecture: "aarch64", Runtime: "container", CloudInit: true},
	{Name: "sandbox", Alias: "openbox:sandbox/ubuntu/24.04/vm", Architecture: "aarch64", Runtime: "virtual-machine", CloudInit: true},

	{Name: "devbox", Alias: "openbox:devbox/ubuntu/24.04", Architecture: "x86_64", Runtime: "container", CloudInit: true, IncludesPi: true},
	{Name: "devbox", Alias: "openbox:devbox/ubuntu/24.04/vm", Architecture: "x86_64", Runtime: "virtual-machine", CloudInit: true, IncludesPi: true},
	{Name: "devbox", Alias: "openbox:devbox/ubuntu/24.04", Architecture: "aarch64", Runtime: "container", CloudInit: true, IncludesPi: true},
	{Name: "devbox", Alias: "openbox:devbox/ubuntu/24.04/vm", Architecture: "aarch64", Runtime: "virtual-machine", CloudInit: true, IncludesPi: true},
}
