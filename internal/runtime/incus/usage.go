// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"net/http"
	"net/url"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// InstanceUsage returns cumulative resource counters from Incus instance state.
func (a *Adapter) InstanceUsage(ctx context.Context, ref string) (runtimeapi.UsageSnapshot, error) {
	if runtimeapi.IsHostConsoleTarget(ref) {
		return runtimeapi.UsageSnapshot{}, runtimeapi.ErrHostTarget
	}
	var state instanceStateRecord
	err := a.request(ctx, http.MethodGet, "/1.0/instances/"+url.PathEscape(ref)+"/state", url.Values{"project": {a.project}}, nil, &state)
	if isNotFound(err) {
		return runtimeapi.UsageSnapshot{}, runtimeapi.ErrNotFound
	}
	if err != nil {
		return runtimeapi.UsageSnapshot{}, err
	}
	return usageFromState(state), nil
}

func usageFromState(state instanceStateRecord) runtimeapi.UsageSnapshot {
	out := runtimeapi.UsageSnapshot{
		CPUNanos:    state.CPU.Usage,
		MemoryBytes: state.Memory.Usage,
		DiskBytes:   diskUsageBytes(state.Disk),
	}
	if status, err := incusState(state.Status); err == nil {
		out.Status = status
	}
	for name, network := range state.Network {
		if name == "lo" {
			continue
		}
		out.NetRxBytes += network.Counters.BytesReceived
		out.NetTxBytes += network.Counters.BytesSent
	}
	return out
}

func diskUsageBytes(disks map[string]instanceStateDisk) int64 {
	if len(disks) == 0 {
		return 0
	}
	if root, ok := disks["root"]; ok {
		return root.Usage
	}
	var total int64
	for _, disk := range disks {
		total += disk.Usage
	}
	return total
}
