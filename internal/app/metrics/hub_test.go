// SPDX-License-Identifier: AGPL-3.0-only

package metrics

import (
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

func TestHubRingEvictsOldest(t *testing.T) {
	t.Parallel()
	hub := NewHub(3, 10)
	id := domain.InstanceID("inst-1")
	limits := Limits{VCPUs: 2, MemoryBytes: 1 << 30, DiskBytes: 10 << 30}
	for i := 0; i < 5; i++ {
		hub.Publish(id, limits, Sample{At: time.Unix(int64(i), 0).UTC(), MemoryBytes: int64(i)})
	}
	snap := hub.Snapshot(id)
	if len(snap.Samples) != 3 {
		t.Fatalf("len=%d", len(snap.Samples))
	}
	if snap.Samples[0].MemoryBytes != 2 || snap.Samples[2].MemoryBytes != 4 {
		t.Fatalf("samples=%+v", snap.Samples)
	}
	if snap.Limits.VCPUs != 2 || snap.IntervalSeconds != 10 {
		t.Fatalf("meta=%+v", snap)
	}
}

func TestHubSubscribeReceivesPublish(t *testing.T) {
	t.Parallel()
	hub := NewHub(10, 10)
	id := domain.InstanceID("inst-1")
	ch := hub.Subscribe(id)
	hub.Publish(id, Limits{}, Sample{MemoryBytes: 42})
	select {
	case sample := <-ch:
		if sample.MemoryBytes != 42 {
			t.Fatalf("sample=%+v", sample)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for sample")
	}
	hub.Unsubscribe(id, ch)
	hub.Publish(id, Limits{}, Sample{MemoryBytes: 99})
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel")
		}
	default:
		t.Fatal("expected closed channel readable")
	}
}

func TestHubRemoveClosesSubscribers(t *testing.T) {
	t.Parallel()
	hub := NewHub(10, 10)
	id := domain.InstanceID("inst-1")
	ch := hub.Subscribe(id)
	hub.Publish(id, Limits{}, Sample{MemoryBytes: 1})
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timeout draining publish")
	}
	hub.Remove(id)
	if len(hub.Snapshot(id).Samples) != 0 {
		t.Fatal("expected empty snapshot after remove")
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
