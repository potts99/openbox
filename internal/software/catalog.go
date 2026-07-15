// SPDX-License-Identifier: AGPL-3.0-only

// Package software is the curated catalog of packages OpenBox can install
// into managed guest instances. Recipes are argv-only — never caller shell.
package software

import (
	"fmt"
	"strings"
)

// Pin declares one package a catalog recipe installs at an exact version.
type Pin struct {
	Manager string // apt or npm
	Name    string
	Version string // exact version, never a range or tag
}

// Package is one installable catalog entry with pins and guest argv recipes.
type Package struct {
	ID          string
	Name        string
	Description string
	Pins        []Pin
	Install     [][]string
	Verify      [][]string
}

// Catalog is the set of packages OpenBox offers.
type Catalog struct {
	packages map[string]Package
}

// DefaultCatalog returns the built-in curated catalog.
func DefaultCatalog() Catalog {
	pi := piPackage()
	return Catalog{packages: map[string]Package{pi.ID: pi}}
}

// Get looks up a package by ID.
func (c Catalog) Get(id string) (Package, bool) {
	pkg, ok := c.packages[id]
	return pkg, ok
}

// List returns all packages in stable ID order for API responses.
func (c Catalog) List() []Package {
	out := make([]Package, 0, len(c.packages))
	// Stable order: known IDs first, then any others by insertion via sorted keys.
	order := []string{"pi"}
	seen := map[string]bool{}
	for _, id := range order {
		if pkg, ok := c.packages[id]; ok {
			out = append(out, pkg)
			seen[id] = true
		}
	}
	for id, pkg := range c.packages {
		if !seen[id] {
			out = append(out, pkg)
		}
	}
	return out
}

// Validate enforces the pin and recipe contract for a catalog package.
func (p Package) Validate() error {
	if p.ID == "" {
		return fmt.Errorf("package id is required")
	}
	if p.Name == "" {
		return fmt.Errorf("package %q name is required", p.ID)
	}
	if len(p.Install) == 0 {
		return fmt.Errorf("package %q must declare install steps", p.ID)
	}
	if len(p.Verify) == 0 {
		return fmt.Errorf("package %q must declare verify steps", p.ID)
	}
	for _, pin := range p.Pins {
		if err := validatePin(pin); err != nil {
			return err
		}
	}
	for _, step := range append(append([][]string{}, p.Install...), p.Verify...) {
		if len(step) == 0 {
			return fmt.Errorf("package %q has an empty recipe step", p.ID)
		}
		joined := strings.Join(step, " ")
		if executesUntrustedRemoteScript(joined) {
			return fmt.Errorf("package %q step %q contains untrusted remote content", p.ID, joined)
		}
	}
	return nil
}

func validatePin(pin Pin) error {
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

func exactVersion(version string) bool {
	if version == "latest" || version == "*" {
		return false
	}
	if strings.ContainsAny(version, "^~<>= |") {
		return false
	}
	return version[0] >= '0' && version[0] <= '9'
}

func executesUntrustedRemoteScript(arg string) bool {
	lowered := strings.ToLower(arg)
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
