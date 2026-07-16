// SPDX-License-Identifier: AGPL-3.0-only

// Package reuse holds shared checkpoint/clone safety helpers.
package reuse

import (
	"strings"

	"github.com/openbox-dev/openbox/internal/domain"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

const (
	// WarningFullCopy is reported when storage lacks CoW.
	WarningFullCopy = "storage does not provide copy-on-write; OpenBox will use a full copy and will not claim copy-on-write behavior"
	// WarningSecrets is reported when cloning/restoring an unprotected source
	// that has guest-persisted software which may retain secrets.
	WarningSecrets = "source has installed software that may retain secrets; cloned guest files may include them"
)

// StorageEfficiency is the public CoW vocabulary for derived instances.
type StorageEfficiency string

const (
	StorageConfirmed    StorageEfficiency = "confirmed"
	StorageNotSupported StorageEfficiency = "not_supported"
	StorageUnknown      StorageEfficiency = "unknown"
)

// ClassifyStorageDriver maps the configured copy storage pool to CoW status.
// Host capabilities can list multiple drivers, which does not establish the
// storage pool used by a copied instance.
func ClassifyStorageDriver(driver string) StorageEfficiency {
	if strings.TrimSpace(driver) == "" {
		return StorageUnknown
	}
	if StorageEfficientCopy([]string{driver}) {
		return StorageConfirmed
	}
	return StorageNotSupported
}

// StorageEfficientCopy reports whether any advertised driver supports CoW copies.
func StorageEfficientCopy(drivers []string) bool {
	for _, driver := range drivers {
		switch strings.ToLower(strings.TrimSpace(driver)) {
		case "btrfs", "zfs", "lvm", "ceph", "cephfs", "powerflex", "pure":
			return true
		}
	}
	return false
}

// ClassifyStorage maps runtime capability drivers to CoW status.
func ClassifyStorage(drivers []string) StorageEfficiency {
	if len(drivers) == 0 {
		return StorageUnknown
	}
	if StorageEfficientCopy(drivers) {
		return StorageConfirmed
	}
	return StorageNotSupported
}

// Preflight evaluates copy/restore warnings and CoW status for a source instance.
func Preflight(capabilities runtimeapi.Capabilities, source domain.Instance, software []domain.InstanceSoftware) (StorageEfficiency, []string) {
	efficiency := ClassifyStorage(capabilities.StorageDrivers)
	warnings := make([]string, 0, 2)
	if efficiency != StorageConfirmed {
		warnings = append(warnings, WarningFullCopy)
	}
	if !source.Protected {
		for _, row := range software {
			if row.PackageID == "pi" && (row.Status == domain.SoftwareInstalled || row.Status == domain.SoftwareFailed || row.Status == domain.SoftwarePending) {
				warnings = append(warnings, WarningSecrets)
				break
			}
		}
	}
	return efficiency, warnings
}
