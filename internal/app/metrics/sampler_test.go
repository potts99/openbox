// SPDX-License-Identifier: AGPL-3.0-only

package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestSamplerDerivesRatesAndNormalizesCPU(t *testing.T) {
	t.Parallel()
	hub := NewHub(60, 10)
	id := domain.InstanceID("inst-1")
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	usages := []runtimeapi.UsageSnapshot{
		{CPUNanos: 1_000_000_000, MemoryBytes: 100, DiskBytes: 200, NetRxBytes: 1_000, NetTxBytes: 500},
		{CPUNanos: 3_000_000_000, MemoryBytes: 110, DiskBytes: 210, NetRxBytes: 3_000, NetTxBytes: 1_500},
	}
	idx := 0
	sampler := NewSampler(hub, func(context.Context) ([]Target, error) {
		return []Target{{
			ID: id, RuntimeRef: "ref-1",
			Limits: Limits{VCPUs: 2, MemoryBytes: 1 << 30, DiskBytes: 10 << 30},
		}}, nil
	}, func(context.Context, string) (runtimeapi.UsageSnapshot, error) {
		u := usages[idx]
		idx++
		return u, nil
	})
	sampler.Now = func() time.Time { return now }

	if err := sampler.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := hub.Snapshot(id).Samples
	if len(first) != 1 || first[0].CPUPercent != nil || first[0].NetRxBps != nil {
		t.Fatalf("first sample should omit rates: %+v", first)
	}

	now = now.Add(10 * time.Second)
	if err := sampler.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := hub.Snapshot(id).Samples
	if len(second) != 2 || second[1].CPUPercent == nil || second[1].NetRxBps == nil || second[1].NetTxBps == nil {
		t.Fatalf("second sample=%+v", second[1])
	}
	// 2e9 nanos over 10s = 0.2 cores; / 2 vCPUs * 100 = 10%
	if *second[1].CPUPercent < 9.9 || *second[1].CPUPercent > 10.1 {
		t.Fatalf("cpu=%v", *second[1].CPUPercent)
	}
	if *second[1].NetRxBps != 200 || *second[1].NetTxBps != 100 {
		t.Fatalf("net rx=%v tx=%v", *second[1].NetRxBps, *second[1].NetTxBps)
	}
	if second[1].MemoryBytes != 110 || second[1].DiskBytes != 210 {
		t.Fatalf("absolute=%+v", second[1])
	}
}

func TestSamplerResetsRatesOnCounterRewind(t *testing.T) {
	t.Parallel()
	hub := NewHub(60, 10)
	id := domain.InstanceID("inst-1")
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	usages := []runtimeapi.UsageSnapshot{
		{CPUNanos: 5_000_000_000, NetRxBytes: 9_000, NetTxBytes: 9_000},
		{CPUNanos: 1_000_000_000, NetRxBytes: 100, NetTxBytes: 100},
	}
	idx := 0
	sampler := NewSampler(hub, func(context.Context) ([]Target, error) {
		return []Target{{ID: id, RuntimeRef: "ref-1", Limits: Limits{VCPUs: 1}}}, nil
	}, func(context.Context, string) (runtimeapi.UsageSnapshot, error) {
		u := usages[idx]
		idx++
		return u, nil
	})
	sampler.Now = func() time.Time { return now }
	_ = sampler.RunOnce(context.Background())
	now = now.Add(10 * time.Second)
	_ = sampler.RunOnce(context.Background())
	sample := hub.Snapshot(id).Samples[1]
	if sample.CPUPercent != nil || sample.NetRxBps != nil {
		t.Fatalf("expected rate gap after rewind: %+v", sample)
	}
}
