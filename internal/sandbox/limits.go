// SPDX-License-Identifier: AGPL-3.0-only

package sandbox

import (
	"sync"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/execstream"
)

const (
	DefaultMaxConcurrentExecsPerInstance = 2
	DefaultMaxOutputBytesPerWindow       = 1 << 20 // 1 MiB
	DefaultOutputRateWindow              = time.Second
)

// ExecGate caps concurrent execs per instance.
type ExecGate struct {
	maxPerInstance int

	mu     sync.Mutex
	active map[string]int
}

// NewExecGate constructs a per-instance concurrency gate.
func NewExecGate(maxPerInstance int) *ExecGate {
	if maxPerInstance <= 0 {
		maxPerInstance = DefaultMaxConcurrentExecsPerInstance
	}
	return &ExecGate{maxPerInstance: maxPerInstance, active: make(map[string]int)}
}

// Acquire reserves one exec slot for instanceID. The release func is idempotent.
func (g *ExecGate) Acquire(instanceID string) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active[instanceID] >= g.maxPerInstance {
		return nil, &domain.Error{Code: domain.CodeBusy, Field: "exec"}
	}
	g.active[instanceID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			if n := g.active[instanceID] - 1; n <= 0 {
				delete(g.active, instanceID)
			} else {
				g.active[instanceID] = n
			}
		})
	}, nil
}

// RateLimitedSink bounds outbound framed bytes inside a sliding window.
type RateLimitedSink struct {
	inner     FrameSink
	maxBytes  int
	window    time.Duration
	now       func() time.Time
	mu        sync.Mutex
	windowAt  time.Time
	bytesUsed int
}

// NewRateLimitedSink wraps sink with an output byte rate limit.
func NewRateLimitedSink(inner FrameSink, maxBytes int, window time.Duration, now func() time.Time) *RateLimitedSink {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxOutputBytesPerWindow
	}
	if window <= 0 {
		window = DefaultOutputRateWindow
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &RateLimitedSink{inner: inner, maxBytes: maxBytes, window: window, now: now}
}

// Emit forwards the frame when the byte budget allows. Exit and error frames
// always pass so clients still learn the terminal status.
func (s *RateLimitedSink) Emit(frame execstream.Frame) error {
	switch frame.(type) {
	case execstream.ExitFrame, execstream.ErrorFrame:
		return s.inner.Emit(frame)
	}
	size := framePayloadBytes(frame)
	s.mu.Lock()
	now := s.now()
	if s.windowAt.IsZero() || now.Sub(s.windowAt) >= s.window {
		s.windowAt = now
		s.bytesUsed = 0
	}
	if s.bytesUsed+size > s.maxBytes {
		s.mu.Unlock()
		return &domain.Error{Code: domain.CodeRateLimited, Field: "exec_output"}
	}
	s.bytesUsed += size
	s.mu.Unlock()
	return s.inner.Emit(frame)
}

func framePayloadBytes(frame execstream.Frame) int {
	switch f := frame.(type) {
	case execstream.StdoutFrame:
		return len(f.Data)
	case execstream.StderrFrame:
		return len(f.Data)
	default:
		return 0
	}
}
