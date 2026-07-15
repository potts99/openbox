// SPDX-License-Identifier: AGPL-3.0-only

// Package doctor turns runtime capability discovery into actionable operator checks.
package doctor

import (
	"context"
	"fmt"
	"sort"
	"strings"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

type Status string

const (
	StatusPass        Status = "pass"
	StatusWarning     Status = "warning"
	StatusUnavailable Status = "unavailable"
	StatusFatal       Status = "fatal"
)

type Check struct {
	Name     string `json:"name"`
	Status   Status `json:"status"`
	Message  string `json:"message"`
	Guidance string `json:"guidance,omitempty"`
}

type Report struct {
	Checks       []Check                 `json:"checks"`
	Capabilities runtimeapi.Capabilities `json:"capabilities"`
}

func (r Report) HasFatal() bool {
	for _, check := range r.Checks {
		if check.Status == StatusFatal {
			return true
		}
	}
	return false
}

type Discoverer interface {
	DiscoverCapabilities(context.Context) (runtimeapi.Capabilities, error)
}

func Run(ctx context.Context, discoverer Discoverer) Report {
	capabilities, err := discoverer.DiscoverCapabilities(ctx)
	if err != nil {
		return Report{Checks: []Check{{
			Name: "incus", Status: StatusFatal, Message: "cannot reach the local Incus daemon",
			Guidance: fmt.Sprintf("verify the Unix socket path and permissions: %v", err),
		}}}
	}
	report := Report{Capabilities: capabilities}
	report.Checks = append(report.Checks, Check{
		Name: "incus", Status: StatusPass,
		Message: fmt.Sprintf("local Incus %s is reachable on %s", valueOr(capabilities.IncusVersion, "unknown version"), valueOr(capabilities.Architecture, "unknown architecture")),
	})

	missingNamespaces := missingMapKeys(capabilities.Namespaces, []string{"mnt", "net", "pid", "user"})
	if len(missingNamespaces) > 0 {
		report.Checks = append(report.Checks, Check{Name: "namespaces", Status: StatusFatal, Message: "required Linux namespaces are unavailable", Guidance: "enable: " + strings.Join(missingNamespaces, ", ")})
	} else {
		report.Checks = append(report.Checks, Check{Name: "namespaces", Status: StatusPass, Message: "required Linux namespaces are available"})
	}
	if capabilities.Cgroups {
		report.Checks = append(report.Checks, Check{Name: "cgroups", Status: StatusPass, Message: "cgroups are available"})
	} else {
		report.Checks = append(report.Checks, Check{Name: "cgroups", Status: StatusFatal, Message: "cgroups are unavailable", Guidance: "use a Linux host with cgroups enabled"})
	}
	if len(capabilities.StorageDrivers) == 0 {
		report.Checks = append(report.Checks, Check{Name: "storage", Status: StatusFatal, Message: "Incus reports no supported storage driver", Guidance: "install and configure a supported Incus storage backend"})
	} else {
		report.Checks = append(report.Checks, Check{Name: "storage", Status: StatusPass, Message: "supported storage drivers: " + strings.Join(capabilities.StorageDrivers, ", ")})
	}
	missingTools := missingMapKeys(capabilities.NetworkTools, []string{"dnsmasq", "ip", "nft"})
	if len(missingTools) > 0 {
		report.Checks = append(report.Checks, Check{Name: "network-tooling", Status: StatusWarning, Message: "some host network tools were not found", Guidance: "install before managed networking: " + strings.Join(missingTools, ", ")})
	} else {
		report.Checks = append(report.Checks, Check{Name: "network-tooling", Status: StatusPass, Message: "required network tools are available"})
	}
	if capabilities.Containers && len(missingNamespaces) == 0 && capabilities.Cgroups {
		report.Checks = append(report.Checks, Check{Name: "standard-isolation", Status: StatusPass, Message: "container isolation is available"})
	} else {
		report.Checks = append(report.Checks, Check{Name: "standard-isolation", Status: StatusFatal, Message: "container isolation is unavailable", Guidance: "resolve namespace, cgroup, and Incus errors above"})
	}
	if capabilities.VirtualMachines {
		report.Checks = append(report.Checks, Check{Name: "strong-isolation", Status: StatusPass, Message: "KVM-backed virtual machines are available"})
	} else {
		guidance := "install on a host exposing accessible /dev/kvm to enable strong isolation"
		switch capabilities.VMAvailability {
		case runtimeapi.VMUnavailableKVMPermission:
			guidance = "grant the OpenBox/Incus service permission to open /dev/kvm"
		case runtimeapi.VMUnavailableNestedVirtualization:
			guidance = "enable nested virtualization in the parent hypervisor or use container isolation"
		case runtimeapi.VMUnavailableIncus:
			guidance = "upgrade or configure Incus with virtual-machine support"
		}
		if capabilities.VMReason != "" {
			guidance += ": " + capabilities.VMReason
		}
		report.Checks = append(report.Checks, Check{Name: "strong-isolation", Status: StatusUnavailable, Message: "strong isolation is unavailable; container mode remains supported", Guidance: guidance})
	}
	return report
}

func FormatHuman(report Report) string {
	var output strings.Builder
	for _, check := range report.Checks {
		fmt.Fprintf(&output, "[%-11s] %-20s %s\n", strings.ToUpper(string(check.Status)), check.Name, check.Message)
		if check.Guidance != "" {
			fmt.Fprintf(&output, "              %s\n", check.Guidance)
		}
	}
	return output.String()
}

func missingMapKeys(values map[string]bool, required []string) []string {
	var missing []string
	for _, key := range required {
		if !values[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	return missing
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
