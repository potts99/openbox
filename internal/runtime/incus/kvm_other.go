//go:build !linux

// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"errors"
	"os"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func probeKVM(path string) (runtimeapi.VMAvailability, string) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return runtimeapi.VMUnavailableKVMPermission, "permission denied while inspecting " + path
		}
		return runtimeapi.VMUnavailableKVMAbsent, path + " is unavailable on this host"
	}
	return runtimeapi.VMUnavailableNestedVirtualization, "KVM probing is supported only on Linux"
}
