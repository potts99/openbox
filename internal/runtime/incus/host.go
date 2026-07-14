// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"os"
	"os/exec"
	goruntime "runtime"
)

type HostCapabilities struct {
	Architecture string
	Namespaces   map[string]bool
	Cgroups      bool
	NetworkTools map[string]bool
	KVM          bool
}

type HostProbe interface {
	Discover(context.Context) (HostCapabilities, error)
}

type OSHostProbe struct{}

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
	if file, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0); err == nil {
		capabilities.KVM = true
		_ = file.Close()
	}
	return capabilities, nil
}
