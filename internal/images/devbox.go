// SPDX-License-Identifier: AGPL-3.0-only

package images

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openbox-dev/openbox/images/devbox"
)

// PiPackageName is the npm package that ships the Pi coding agent CLI.
const PiPackageName = "@earendil-works/pi-coding-agent"

// PackagePin declares one package a curated image installs at an exact version.
type PackagePin struct {
	Manager string `json:"manager"` // apt or npm
	Name    string `json:"name"`
	Version string `json:"version"` // exact version, never a range or tag
}

// DevboxDefinition is the checked-in Devbox image definition: base image,
// pinned packages, and the install/verify recipe metadata. It is data the
// image build consumes; OpenBox itself never executes the steps.
type DevboxDefinition struct {
	Name     string       `json:"name"`
	Base     string       `json:"base"`
	Packages []PackagePin `json:"packages"`
	Setup    []string     `json:"setup"`
	Verify   []string     `json:"verify"`
}

// LoadDevboxDefinition parses and validates the embedded Devbox definition.
func LoadDevboxDefinition() (DevboxDefinition, error) {
	var def DevboxDefinition
	if err := json.Unmarshal(devbox.Definition, &def); err != nil {
		return DevboxDefinition{}, fmt.Errorf("parse devbox definition: %w", err)
	}
	if err := def.Validate(); err != nil {
		return DevboxDefinition{}, err
	}
	return def, nil
}

// Pin returns the declared pin for a manager/name pair.
func (d DevboxDefinition) Pin(manager, name string) (PackagePin, bool) {
	for _, pin := range d.Packages {
		if pin.Manager == manager && pin.Name == name {
			return pin, true
		}
	}
	return PackagePin{}, false
}

// Validate enforces the Devbox pin contract: Pi and tmux are pinned to exact
// versions, and no setup or verify step executes untrusted remote scripts.
func (d DevboxDefinition) Validate() error {
	if d.Name != "devbox" {
		return fmt.Errorf("devbox definition name is %q, want devbox", d.Name)
	}
	if d.Base == "" {
		return fmt.Errorf("devbox definition must declare a base image")
	}
	for _, pin := range d.Packages {
		if err := validatePin(pin); err != nil {
			return err
		}
	}
	if _, ok := d.Pin("npm", PiPackageName); !ok {
		return fmt.Errorf("devbox definition must pin %s via npm", PiPackageName)
	}
	if _, ok := d.Pin("apt", "tmux"); !ok {
		return fmt.Errorf("devbox definition must pin tmux via apt")
	}
	for _, step := range append(append([]string{}, d.Setup...), d.Verify...) {
		if executesUntrustedRemoteScript(step) {
			return fmt.Errorf("step %q executes untrusted remote content", step)
		}
	}
	return nil
}

func validatePin(pin PackagePin) error {
	if pin.Manager != "apt" && pin.Manager != "npm" {
		return fmt.Errorf("package %q uses unsupported manager %q", pin.Name, pin.Manager)
	}
	if pin.Name == "" {
		return fmt.Errorf("package pin is missing a name")
	}
	if pin.Version == "" {
		return fmt.Errorf("package %q is missing a version", pin.Name)
	}
	if !exactVersion(pin.Version) {
		return fmt.Errorf("package %q version %q is not exact", pin.Name, pin.Version)
	}
	return nil
}

// exactVersion rejects dist-tags and range specifiers so pins stay reproducible.
func exactVersion(version string) bool {
	if version == "latest" || version == "*" {
		return false
	}
	if strings.ContainsAny(version, "^~<>= |") {
		return false
	}
	return version[0] >= '0' && version[0] <= '9'
}

// executesUntrustedRemoteScript flags recipe steps that would download and run
// remote content (for example curl|sh installers). Pins must come from package
// managers, never from arbitrary scripts fetched at build time.
func executesUntrustedRemoteScript(step string) bool {
	lowered := strings.ToLower(step)
	if strings.Contains(lowered, "http://") || strings.Contains(lowered, "https://") {
		return true
	}
	for _, downloader := range []string{"curl", "wget"} {
		if strings.Contains(lowered, downloader) {
			return true
		}
	}
	return false
}
