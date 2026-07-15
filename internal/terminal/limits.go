// SPDX-License-Identifier: AGPL-3.0-only

package terminal

import (
	"errors"
	"sync"
	"time"
)

// Session and connection limit defaults. Values are intentionally conservative
// for a single-owner self-hosted control plane; tune via Limits if needed.
const (
	// DefaultMaxInboundFramesPerWindow caps inbound WebSocket frames per
	// connection inside RateWindow (keystroke/paste storms).
	DefaultMaxInboundFramesPerWindow = 120

	// DefaultMaxInboundBytesPerWindow caps inbound encoded payload bytes per
	// connection inside RateWindow (~paste / bulk input).
	DefaultMaxInboundBytesPerWindow = 512 << 10 // 512 KiB

	// DefaultRateWindow is the sliding window for inbound frame/byte caps.
	DefaultRateWindow = time.Second

	// DefaultMaxSessionsPerOwner caps concurrent terminal WebSockets per owner.
	DefaultMaxSessionsPerOwner = 8

	// DefaultMaxSessionsPerInstance caps concurrent terminals per instance.
	DefaultMaxSessionsPerInstance = 4

	// DefaultIdleTimeout closes a session after this long with no traffic.
	DefaultIdleTimeout = 10 * time.Minute
)

// Limit errors returned by helpers and the HTTP bridge.
var (
	ErrFrameTooLarge = errors.New("terminal frame too large")
	ErrRateLimited   = errors.New("terminal inbound rate exceeded")
	ErrSessionLimit  = errors.New("terminal session limit exceeded")
	ErrIdleTimeout   = errors.New("terminal idle timeout")
)

// Limits bundles enforceable terminal session bounds.
type Limits struct {
	MaxFrameBytes             int
	MaxInboundFramesPerWindow int
	MaxInboundBytesPerWindow  int
	RateWindow                time.Duration
	MaxSessionsPerOwner       int
	MaxSessionsPerInstance    int
	IdleTimeout               time.Duration
}

// DefaultLimits returns production defaults (see constants above).
func DefaultLimits() Limits {
	return Limits{
		MaxFrameBytes:             MaxFrameBytes,
		MaxInboundFramesPerWindow: DefaultMaxInboundFramesPerWindow,
		MaxInboundBytesPerWindow:  DefaultMaxInboundBytesPerWindow,
		RateWindow:                DefaultRateWindow,
		MaxSessionsPerOwner:       DefaultMaxSessionsPerOwner,
		MaxSessionsPerInstance:    DefaultMaxSessionsPerInstance,
		IdleTimeout:               DefaultIdleTimeout,
	}
}

// WithDefaults fills zero-valued fields from DefaultLimits.
func (l Limits) WithDefaults() Limits {
	d := DefaultLimits()
	if l.MaxFrameBytes <= 0 {
		l.MaxFrameBytes = d.MaxFrameBytes
	}
	if l.MaxInboundFramesPerWindow <= 0 {
		l.MaxInboundFramesPerWindow = d.MaxInboundFramesPerWindow
	}
	if l.MaxInboundBytesPerWindow <= 0 {
		l.MaxInboundBytesPerWindow = d.MaxInboundBytesPerWindow
	}
	if l.RateWindow <= 0 {
		l.RateWindow = d.RateWindow
	}
	if l.MaxSessionsPerOwner <= 0 {
		l.MaxSessionsPerOwner = d.MaxSessionsPerOwner
	}
	if l.MaxSessionsPerInstance <= 0 {
		l.MaxSessionsPerInstance = d.MaxSessionsPerInstance
	}
	if l.IdleTimeout <= 0 {
		l.IdleTimeout = d.IdleTimeout
	}
	return l
}

// CheckFrameSize rejects encoded frames larger than maxBytes.
func CheckFrameSize(size, maxBytes int) error {
	if maxBytes <= 0 {
		maxBytes = MaxFrameBytes
	}
	if size > maxBytes {
		return ErrFrameTooLarge
	}
	return nil
}

// InboundLimiter bounds frames and bytes per connection inside a window.
type InboundLimiter struct {
	maxFrames int
	maxBytes  int
	window    time.Duration

	mu          sync.Mutex
	windowStart time.Time
	frames      int
	bytes       int
}

// NewInboundLimiter constructs a per-connection inbound rate limiter.
func NewInboundLimiter(maxFrames, maxBytes int, window time.Duration) *InboundLimiter {
	if maxFrames <= 0 {
		maxFrames = DefaultMaxInboundFramesPerWindow
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxInboundBytesPerWindow
	}
	if window <= 0 {
		window = DefaultRateWindow
	}
	return &InboundLimiter{maxFrames: maxFrames, maxBytes: maxBytes, window: window}
}

// Allow records one inbound frame of frameBytes. Returns ErrRateLimited when
// the frame or byte cap for the current window would be exceeded.
func (l *InboundLimiter) Allow(now time.Time, frameBytes int) error {
	if frameBytes < 0 {
		frameBytes = 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.windowStart.IsZero() || now.Sub(l.windowStart) >= l.window {
		l.windowStart = now
		l.frames = 0
		l.bytes = 0
	}
	if l.frames+1 > l.maxFrames || l.bytes+frameBytes > l.maxBytes {
		return ErrRateLimited
	}
	l.frames++
	l.bytes += frameBytes
	return nil
}

// SessionRegistry tracks concurrent terminal WebSockets per owner and instance.
type SessionRegistry struct {
	maxPerOwner    int
	maxPerInstance int

	mu         sync.Mutex
	byOwner    map[string]int
	byInstance map[string]int
	nextID     uint64
	active     map[uint64]sessionKeys
}

type sessionKeys struct {
	owner    string
	instance string
}

// NewSessionRegistry constructs a registry with the given caps.
func NewSessionRegistry(maxPerOwner, maxPerInstance int) *SessionRegistry {
	if maxPerOwner <= 0 {
		maxPerOwner = DefaultMaxSessionsPerOwner
	}
	if maxPerInstance <= 0 {
		maxPerInstance = DefaultMaxSessionsPerInstance
	}
	return &SessionRegistry{
		maxPerOwner:    maxPerOwner,
		maxPerInstance: maxPerInstance,
		byOwner:        make(map[string]int),
		byInstance:     make(map[string]int),
		active:         make(map[uint64]sessionKeys),
	}
}

// Acquire reserves a session slot. The returned release function is idempotent.
func (r *SessionRegistry) Acquire(ownerID, instanceID string) (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byOwner[ownerID] >= r.maxPerOwner {
		return nil, ErrSessionLimit
	}
	instKey := ownerID + "\x00" + instanceID
	if r.byInstance[instKey] >= r.maxPerInstance {
		return nil, ErrSessionLimit
	}
	r.nextID++
	id := r.nextID
	r.byOwner[ownerID]++
	r.byInstance[instKey]++
	r.active[id] = sessionKeys{owner: ownerID, instance: instKey}

	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			keys, ok := r.active[id]
			if !ok {
				return
			}
			delete(r.active, id)
			if n := r.byOwner[keys.owner] - 1; n <= 0 {
				delete(r.byOwner, keys.owner)
			} else {
				r.byOwner[keys.owner] = n
			}
			if n := r.byInstance[keys.instance] - 1; n <= 0 {
				delete(r.byInstance, keys.instance)
			} else {
				r.byInstance[keys.instance] = n
			}
		})
	}, nil
}

// Count returns the number of active sessions for ownerID (tests/diagnostics).
func (r *SessionRegistry) Count(ownerID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byOwner[ownerID]
}

// IdleWatch tracks last traffic time for idle timeout enforcement.
type IdleWatch struct {
	timeout time.Duration

	mu   sync.Mutex
	last time.Time
}

// NewIdleWatch constructs an idle tracker. Touch must be called when the
// session starts so the clock begins after the first activity marker.
func NewIdleWatch(timeout time.Duration) *IdleWatch {
	if timeout <= 0 {
		timeout = DefaultIdleTimeout
	}
	return &IdleWatch{timeout: timeout}
}

// Touch records traffic at now.
func (w *IdleWatch) Touch(now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.last = now
}

// Expired reports whether now is at or past last+timeout. A never-touched
// watch is not expired; callers should Touch on session start.
func (w *IdleWatch) Expired(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.last.IsZero() {
		return false
	}
	return !now.Before(w.last.Add(w.timeout))
}
