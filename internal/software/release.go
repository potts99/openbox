// SPDX-License-Identifier: AGPL-3.0-only

package software

import (
	"fmt"
	"strings"
)

// ReleaseAsset is one architecture-specific binary for a github-release pin.
type ReleaseAsset struct {
	Arch     string // "x86_64" or "aarch64"
	Filename string
	SHA256   string // lowercase hex, no sha256: prefix
}

func validateGitHubReleasePin(pin Pin) error {
	if err := validateRepoName(pin.Name); err != nil {
		return err
	}
	if pin.Version == "" {
		return fmt.Errorf("package %q is missing a version", pin.Name)
	}
	if !exactVersion(pin.Version) {
		return fmt.Errorf("package %q version %q is not exact", pin.Name, pin.Version)
	}
	required := map[string]bool{"x86_64": false, "aarch64": false}
	for _, asset := range pin.Assets {
		if asset.Arch != "x86_64" && asset.Arch != "aarch64" {
			return fmt.Errorf("package %q has unsupported arch %q", pin.Name, asset.Arch)
		}
		if asset.Filename == "" {
			return fmt.Errorf("package %q arch %q is missing a filename", pin.Name, asset.Arch)
		}
		if !isSHA256Hex(asset.SHA256) {
			return fmt.Errorf("package %q arch %q has invalid sha256", pin.Name, asset.Arch)
		}
		required[asset.Arch] = true
	}
	for arch, present := range required {
		if !present {
			return fmt.Errorf("package %q is missing %s release asset", pin.Name, arch)
		}
	}
	return nil
}

func validateRepoName(name string) error {
	if name == "" {
		return fmt.Errorf("package pin is missing a name")
	}
	owner, repo, ok := strings.Cut(name, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") || strings.ContainsAny(name, " \t") {
		return fmt.Errorf("package %q must be owner/repo", name)
	}
	return nil
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func hasGitHubReleasePin(pins []Pin) bool {
	for _, pin := range pins {
		if pin.Manager == "github-release" {
			return true
		}
	}
	return false
}
