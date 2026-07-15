// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"net/http"
	"sort"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

type serverInfo struct {
	APIExtensions []string `json:"api_extensions"`
	Environment   struct {
		KernelArchitecture      string `json:"kernel_architecture"`
		ServerVersion           string `json:"server_version"`
		StorageSupportedDrivers []struct {
			Name string `json:"name"`
		} `json:"storage_supported_drivers"`
	} `json:"environment"`
}

func (a *Adapter) DiscoverCapabilities(ctx context.Context) (runtimeapi.Capabilities, error) {
	host, err := a.hostProbe.Discover(ctx)
	if err != nil {
		return runtimeapi.Capabilities{}, err
	}
	var server serverInfo
	if err := a.request(ctx, http.MethodGet, "/1.0", nil, nil, &server); err != nil {
		return runtimeapi.Capabilities{}, err
	}
	drivers := make([]string, 0, len(server.Environment.StorageSupportedDrivers))
	for _, driver := range server.Environment.StorageSupportedDrivers {
		if driver.Name != "" {
			drivers = append(drivers, driver.Name)
		}
	}
	sort.Strings(drivers)
	architecture := server.Environment.KernelArchitecture
	if architecture == "" {
		architecture = host.Architecture
	}
	vmAPI := containsString(server.APIExtensions, "virtual-machines")
	availability := host.VMAvailability
	if availability == "" {
		if host.KVM {
			availability = runtimeapi.VMAvailable
		} else {
			availability = runtimeapi.VMUnavailableKVMAbsent
		}
	}
	vmReason := host.VMReason
	hostKVMAvailable := availability == runtimeapi.VMAvailable
	if availability == runtimeapi.VMAvailable && !vmAPI {
		availability = runtimeapi.VMUnavailableIncus
		vmReason = "the Incus daemon does not advertise virtual-machine support"
	}
	vmUsable := availability == runtimeapi.VMAvailable && vmAPI
	return runtimeapi.Capabilities{
		Architecture:    architecture,
		IncusVersion:    server.Environment.ServerVersion,
		Namespaces:      cloneBoolMap(host.Namespaces),
		Cgroups:         host.Cgroups,
		StorageDrivers:  drivers,
		NetworkTools:    cloneBoolMap(host.NetworkTools),
		KVM:             hostKVMAvailable,
		Containers:      true,
		VirtualMachines: vmUsable,
		VMAvailability:  availability,
		VMReason:        vmReason,
	}, nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func cloneBoolMap(value map[string]bool) map[string]bool {
	result := make(map[string]bool, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
