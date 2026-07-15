// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestUsageFromStateAggregatesCounters(t *testing.T) {
	t.Parallel()
	state := instanceStateRecord{
		Status: "Running",
		CPU:    instanceStateCPU{Usage: 5_000_000_000},
		Memory: instanceStateMemory{Usage: 256 << 20},
		Disk: map[string]instanceStateDisk{
			"root": {Usage: 10 << 30},
			"data": {Usage: 1 << 30},
		},
		Network: map[string]instanceStateNetwork{
			"lo": {
				Counters: instanceStateCounters{BytesReceived: 99, BytesSent: 99},
			},
			"eth0": {
				Counters: instanceStateCounters{BytesReceived: 1000, BytesSent: 2000},
			},
			"eth1": {
				Counters: instanceStateCounters{BytesReceived: 300, BytesSent: 400},
			},
		},
	}
	got := usageFromState(state)
	if got.Status != runtimeapi.StateRunning {
		t.Fatalf("status=%q", got.Status)
	}
	if got.CPUNanos != 5_000_000_000 || got.MemoryBytes != 256<<20 {
		t.Fatalf("cpu/memory=%+v", got)
	}
	if got.DiskBytes != 10<<30 {
		t.Fatalf("disk=%d want root only", got.DiskBytes)
	}
	if got.NetRxBytes != 1300 || got.NetTxBytes != 2400 {
		t.Fatalf("net rx=%d tx=%d", got.NetRxBytes, got.NetTxBytes)
	}
}

func TestUsageFromStateSumsDisksWithoutRoot(t *testing.T) {
	t.Parallel()
	got := usageFromState(instanceStateRecord{
		Disk: map[string]instanceStateDisk{
			"sda": {Usage: 100},
			"sdb": {Usage: 50},
		},
	})
	if got.DiskBytes != 150 {
		t.Fatalf("disk=%d", got.DiskBytes)
	}
}

func TestInstanceUsageFetchesState(t *testing.T) {
	t.Parallel()
	socket := serveUnixHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/1.0/instances/box/state" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("project") != "openbox" {
			t.Errorf("project=%q", r.URL.Query().Get("project"))
		}
		writeSync(w, instanceStateRecord{
			Status: "Running",
			CPU:    instanceStateCPU{Usage: 42},
			Memory: instanceStateMemory{Usage: 64},
			Disk:   map[string]instanceStateDisk{"root": {Usage: 128}},
			Network: map[string]instanceStateNetwork{
				"eth0": {Counters: instanceStateCounters{BytesReceived: 7, BytesSent: 8}},
			},
		})
	}))
	adapter, err := New(Options{SocketPath: socket, Project: "openbox"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := adapter.InstanceUsage(context.Background(), "box")
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUNanos != 42 || got.MemoryBytes != 64 || got.DiskBytes != 128 || got.NetRxBytes != 7 || got.NetTxBytes != 8 {
		t.Fatalf("got=%+v", got)
	}
}

func TestInstanceUsageRejectsHostTarget(t *testing.T) {
	t.Parallel()
	adapter, err := New(Options{SocketPath: filepath.Join(t.TempDir(), "missing.sock"), Project: "openbox"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.InstanceUsage(context.Background(), "host")
	if err != runtimeapi.ErrHostTarget {
		t.Fatalf("err=%v", err)
	}
}

func TestInstanceStateRecordKeepsAddresses(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"status":"Running",
		"cpu":{"usage":1},
		"memory":{"usage":2},
		"disk":{"root":{"usage":3}},
		"network":{
			"eth0":{
				"addresses":[{"family":"inet","address":"10.42.0.9","scope":"global"}],
				"counters":{"bytes_received":10,"bytes_sent":20}
			}
		}
	}`)
	var state instanceStateRecord
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	addr, ok := selectInstanceAddress(state, true)
	if !ok || addr != "10.42.0.9" {
		t.Fatalf("address=%q ok=%v", addr, ok)
	}
	usage := usageFromState(state)
	if usage.NetRxBytes != 10 || usage.DiskBytes != 3 {
		t.Fatalf("usage=%+v", usage)
	}
}
