//go:build linux

// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"golang.org/x/sys/unix"
)

const (
	kvmGetAPIVersion = 0xAE00
	kvmCreateVM      = 0xAE01
)

func probeKVM(path string) (runtimeapi.VMAvailability, string) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return runtimeapi.VMUnavailableKVMAbsent, path + " is absent"
		}
		if errors.Is(err, os.ErrPermission) {
			return runtimeapi.VMUnavailableKVMPermission, "permission denied while inspecting " + path
		}
		return runtimeapi.VMUnavailableNestedVirtualization, err.Error()
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return runtimeapi.VMUnavailableKVMPermission, "permission denied opening " + path
		}
		return runtimeapi.VMUnavailableNestedVirtualization, err.Error()
	}
	defer file.Close()
	version, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), kvmGetAPIVersion, 0)
	var probeErr error
	if errno != 0 {
		probeErr = errno
	}
	if availability, reason := classifyKVMAPIVersion(version, probeErr); availability != runtimeapi.VMAvailable {
		return availability, reason
	}
	vmFD, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), kvmCreateVM, 0)
	if errno != 0 {
		return runtimeapi.VMUnavailableNestedVirtualization, fmt.Sprintf("KVM cannot create a VM (nested virtualization may be unavailable): %v", errno)
	}
	_ = unix.Close(int(vmFD))
	return runtimeapi.VMAvailable, ""
}
