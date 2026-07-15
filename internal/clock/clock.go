// SPDX-License-Identifier: AGPL-3.0-only

// Package clock provides deterministic time to durable workers.
package clock

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

type Real struct{}

func (Real) Now() time.Time                                { return time.Now().UTC() }
func (Real) After(duration time.Duration) <-chan time.Time { return time.After(duration) }

type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

type waiter struct {
	at time.Time
	ch chan time.Time
}

func NewFake(now time.Time) *Fake { return &Fake{now: now.UTC()} }
func (f *Fake) Now() time.Time    { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
func (f *Fake) After(duration time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	f.waiters = append(f.waiters, waiter{at: f.now.Add(duration), ch: ch})
	return ch
}
func (f *Fake) Advance(duration time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(duration)
	remaining := f.waiters[:0]
	for _, waiter := range f.waiters {
		if !waiter.at.After(f.now) {
			waiter.ch <- f.now
			close(waiter.ch)
			continue
		}
		remaining = append(remaining, waiter)
	}
	f.waiters = remaining
}
