// SPDX-License-Identifier: AGPL-3.0-only

package metrics

import (
	"sync"

	"github.com/openbox-dev/openbox/internal/domain"
)

const (
	DefaultIntervalSeconds = 10
	DefaultRetention       = 60 * 60 / DefaultIntervalSeconds // ~60 minutes
)

// Hub stores per-instance rings and fans out new samples to subscribers.
type Hub struct {
	mu       sync.Mutex
	capacity int
	interval int
	series   map[domain.InstanceID]*series
}

type series struct {
	limits Limits
	ring   []Sample
	subs   map[chan Sample]struct{}
}

// NewHub creates a metrics hub with the given ring capacity and interval metadata.
func NewHub(capacity, intervalSeconds int) *Hub {
	if capacity <= 0 {
		capacity = DefaultRetention
	}
	if intervalSeconds <= 0 {
		intervalSeconds = DefaultIntervalSeconds
	}
	return &Hub{
		capacity: capacity,
		interval: intervalSeconds,
		series:   map[domain.InstanceID]*series{},
	}
}

func (h *Hub) IntervalSeconds() int {
	return h.interval
}

// Publish appends a sample and notifies subscribers. Non-blocking; slow
// subscribers drop the sample for that tick.
func (h *Hub) Publish(id domain.InstanceID, limits Limits, sample Sample) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.series[id]
	if s == nil {
		s = &series{subs: map[chan Sample]struct{}{}}
		h.series[id] = s
	}
	s.limits = limits
	s.ring = append(s.ring, sample)
	if len(s.ring) > h.capacity {
		s.ring = append([]Sample(nil), s.ring[len(s.ring)-h.capacity:]...)
	}
	for ch := range s.subs {
		select {
		case ch <- sample:
		default:
		}
	}
}

// Snapshot returns the current ring (oldest→newest) and limits.
func (h *Hub) Snapshot(id domain.InstanceID) Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := Snapshot{
		InstanceID:      id,
		IntervalSeconds: h.interval,
		Samples:         nil,
	}
	s := h.series[id]
	if s == nil {
		return out
	}
	out.Limits = s.limits
	out.Samples = append([]Sample(nil), s.ring...)
	return out
}

// Subscribe receives future samples. Buffer size 1; caller must Unsubscribe.
func (h *Hub) Subscribe(id domain.InstanceID) <-chan Sample {
	ch := make(chan Sample, 1)
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.series[id]
	if s == nil {
		s = &series{subs: map[chan Sample]struct{}{}}
		h.series[id] = s
	}
	s.subs[ch] = struct{}{}
	return ch
}

// Unsubscribe stops delivery and closes the channel.
func (h *Hub) Unsubscribe(id domain.InstanceID, ch <-chan Sample) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.series[id]
	if s == nil {
		return
	}
	for sub := range s.subs {
		if sub == ch {
			delete(s.subs, sub)
			close(sub)
			return
		}
	}
}

// Remove drops the ring and closes subscribers for an instance.
func (h *Hub) Remove(id domain.InstanceID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.series[id]
	if s == nil {
		return
	}
	for sub := range s.subs {
		close(sub)
	}
	delete(h.series, id)
}
