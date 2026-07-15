// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	goruntime "runtime"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

type HostCapabilities struct {
	Architecture   string
	Namespaces     map[string]bool
	Cgroups        bool
	NetworkTools   map[string]bool
	KVM            bool
	VMAvailability runtimeapi.VMAvailability
	VMReason       string
}

type HostProbe interface {
	Discover(context.Context) (HostCapabilities, error)
}

type OSHostProbe struct{}

const kvmStableAPIVersion = 12

func (OSHostProbe) Discover(ctx context.Context) (HostCapabilities, error) {
	if err := ctx.Err(); err != nil {
		return HostCapabilities{}, err
	}
	capabilities := HostCapabilities{
		Architecture: goruntime.GOARCH,
		Namespaces:   map[string]bool{},
		NetworkTools: map[string]bool{},
	}
	for _, namespace := range []string{"mnt", "net", "pid", "user"} {
		_, err := os.Stat("/proc/self/ns/" + namespace)
		capabilities.Namespaces[namespace] = err == nil
	}
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		capabilities.Cgroups = true
	} else if _, err := os.Stat("/proc/cgroups"); err == nil {
		capabilities.Cgroups = true
	}
	for _, tool := range []string{"dnsmasq", "ip", "nft"} {
		_, err := exec.LookPath(tool)
		capabilities.NetworkTools[tool] = err == nil
	}
	capabilities.VMAvailability, capabilities.VMReason = probeKVM("/dev/kvm")
	if capabilities.VMAvailability == runtimeapi.VMAvailable {
		capabilities.KVM = true
	}
	return capabilities, nil
}

func classifyKVMAPIVersion(version uintptr, probeErr error) (runtimeapi.VMAvailability, string) {
	if probeErr != nil {
		return runtimeapi.VMUnavailableNestedVirtualization, "KVM API probe failed: " + probeErr.Error()
	}
	if version != kvmStableAPIVersion {
		return runtimeapi.VMUnavailableNestedVirtualization, fmt.Sprintf("KVM API version %d is incompatible; OpenBox requires %d", version, kvmStableAPIVersion)
	}
	return runtimeapi.VMAvailable, ""
}
