// SPDX-License-Identifier: AGPL-3.0-only

package software

import (
	"fmt"

	"github.com/openbox-dev/openbox/internal/images"
)

// piPackage builds the curated Pi coding agent catalog entry from the
// checked-in Devbox pin definition (exact versions only).
func piPackage() Package {
	def, err := images.LoadDevboxDefinition()
	if err != nil {
		panic(fmt.Sprintf("software catalog: load pi pins: %v", err))
	}
	pins := make([]Pin, 0, len(def.Packages))
	var tmuxVer, piVer string
	for _, p := range def.Packages {
		pins = append(pins, Pin{Manager: p.Manager, Name: p.Name, Version: p.Version})
		switch {
		case p.Manager == "apt" && p.Name == "tmux":
			tmuxVer = p.Version
		case p.Manager == "npm" && p.Name == images.PiPackageName:
			piVer = p.Version
		}
	}
	if tmuxVer == "" || piVer == "" {
		panic("software catalog: pi package missing tmux or pi pin")
	}

	pkg := Package{
		ID:          "pi",
		Name:        "Pi coding agent",
		Description: "Installs the Pi CLI and tmux so you can run an agent session from the instance terminal.",
		Pins:        pins,
		Install: [][]string{
			{"apt-get", "install", "-y", "tmux=" + tmuxVer},
			{"npm", "install", "-g", images.PiPackageName + "@" + piVer},
		},
		Verify: [][]string{
			{"pi", "--version"},
			{"tmux", "-V"},
		},
	}
	if err := pkg.Validate(); err != nil {
		panic(fmt.Sprintf("software catalog: invalid pi package: %v", err))
	}
	return pkg
}
