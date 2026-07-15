// SPDX-License-Identifier: AGPL-3.0-only

package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// Lister returns running instances that should be sampled.
type Lister func(context.Context) ([]Target, error)

// UsageReader fetches cumulative counters for a runtime ref.
type UsageReader func(context.Context, string) (runtimeapi.UsageSnapshot, error)

// Sampler polls running instances and publishes derived samples to a Hub.
type Sampler struct {
	Hub    *Hub
	List   Lister
	Usage  UsageReader
	Now    func() time.Time
	Logger *slog.Logger

	prev map[domain.InstanceID]rawCounters
}

type rawCounters struct {
	at         time.Time
	cpuNanos   int64
	netRxBytes int64
	netTxBytes int64
}

func NewSampler(hub *Hub, list Lister, usage UsageReader) *Sampler {
	return &Sampler{
		Hub:   hub,
		List:  list,
		Usage: usage,
		Now:   time.Now,
		prev:  map[domain.InstanceID]rawCounters{},
	}
}

// RunOnce samples every listed target once.
func (s *Sampler) RunOnce(ctx context.Context) error {
	if s.List == nil || s.Usage == nil || s.Hub == nil {
		return nil
	}
	targets, err := s.List(ctx)
	if err != nil {
		return err
	}
	seen := make(map[domain.InstanceID]struct{}, len(targets))
	now := s.Now()
	for _, target := range targets {
		if target.RuntimeRef == "" {
			continue
		}
		seen[target.ID] = struct{}{}
		raw, err := s.Usage(ctx, target.RuntimeRef)
		if err != nil {
			if s.Logger != nil {
				s.Logger.Warn("instance usage sample failed", "instance_id", target.ID, "error", err)
			}
			delete(s.prev, target.ID)
			continue
		}
		sample := s.derive(target.ID, target.Limits.VCPUs, now, raw)
		s.Hub.Publish(target.ID, target.Limits, sample)
	}
	for id := range s.prev {
		if _, ok := seen[id]; !ok {
			delete(s.prev, id)
		}
	}
	return nil
}

func (s *Sampler) derive(id domain.InstanceID, vcpus int, now time.Time, raw runtimeapi.UsageSnapshot) Sample {
	sample := Sample{
		At:          now.UTC(),
		MemoryBytes: raw.MemoryBytes,
		DiskBytes:   raw.DiskBytes,
	}
	prev, ok := s.prev[id]
	s.prev[id] = rawCounters{
		at:         now,
		cpuNanos:   raw.CPUNanos,
		netRxBytes: raw.NetRxBytes,
		netTxBytes: raw.NetTxBytes,
	}
	if !ok || !now.After(prev.at) || raw.CPUNanos < prev.cpuNanos || raw.NetRxBytes < prev.netRxBytes || raw.NetTxBytes < prev.netTxBytes {
		return sample
	}
	elapsed := now.Sub(prev.at).Seconds()
	if elapsed <= 0 {
		return sample
	}
	// CPU nanos delta / wall seconds = cores used; / vCPUs → % of allocation.
	cores := (float64(raw.CPUNanos-prev.cpuNanos) / 1e9) / elapsed
	cpu := cores * 100
	if vcpus > 0 {
		cpu = cores / float64(vcpus) * 100
	}
	if cpu < 0 {
		cpu = 0
	}
	sample.CPUPercent = &cpu
	rx := float64(raw.NetRxBytes-prev.netRxBytes) / elapsed
	tx := float64(raw.NetTxBytes-prev.netTxBytes) / elapsed
	sample.NetRxBps = &rx
	sample.NetTxBps = &tx
	return sample
}
