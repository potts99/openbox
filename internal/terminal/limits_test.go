// SPDX-License-Identifier: AGPL-3.0-only

package terminal

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestDefaultLimitsAreDocumentedAndPositive(t *testing.T) {
	t.Parallel()
	l := DefaultLimits()
	if l.MaxFrameBytes != MaxFrameBytes {
		t.Fatalf("MaxFrameBytes=%d want %d", l.MaxFrameBytes, MaxFrameBytes)
	}
	if l.MaxInboundFramesPerWindow <= 0 || l.MaxInboundBytesPerWindow <= 0 {
		t.Fatalf("inbound rate bounds must be positive: %+v", l)
	}
	if l.RateWindow <= 0 {
		t.Fatalf("RateWindow=%v", l.RateWindow)
	}
	if l.MaxSessionsPerOwner <= 0 || l.MaxSessionsPerInstance <= 0 {
		t.Fatalf("session caps must be positive: %+v", l)
	}
	if l.IdleTimeout <= 0 {
		t.Fatalf("IdleTimeout=%v", l.IdleTimeout)
	}
}

func TestInboundLimiterAllowsWithinWindowThenRejects(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	lim := NewInboundLimiter(2, 100, time.Second)

	if err := lim.Allow(now, 40); err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if err := lim.Allow(now.Add(100*time.Millisecond), 40); err != nil {
		t.Fatalf("second frame: %v", err)
	}
	if err := lim.Allow(now.Add(200*time.Millisecond), 1); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("third frame err=%v want ErrRateLimited", err)
	}
}

func TestInboundLimiterRejectsByteBurst(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	lim := NewInboundLimiter(100, 50, time.Second)

	if err := lim.Allow(now, 40); err != nil {
		t.Fatal(err)
	}
	if err := lim.Allow(now, 20); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err=%v want ErrRateLimited", err)
	}
}

func TestInboundLimiterResetsAfterWindow(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	lim := NewInboundLimiter(1, 100, time.Second)

	if err := lim.Allow(now, 10); err != nil {
		t.Fatal(err)
	}
	if err := lim.Allow(now.Add(500*time.Millisecond), 10); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err=%v want ErrRateLimited", err)
	}
	if err := lim.Allow(now.Add(time.Second), 10); err != nil {
		t.Fatalf("after window: %v", err)
	}
}

func TestSessionRegistryEnforcesOwnerAndInstanceCaps(t *testing.T) {
	t.Parallel()
	reg := NewSessionRegistry(2, 1)

	releaseA, err := reg.Acquire("owner-1", "inst-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Acquire("owner-1", "inst-a"); !errors.Is(err, ErrSessionLimit) {
		t.Fatalf("same instance second session: %v", err)
	}
	releaseB, err := reg.Acquire("owner-1", "inst-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Acquire("owner-1", "inst-c"); !errors.Is(err, ErrSessionLimit) {
		t.Fatalf("owner cap: %v", err)
	}
	releaseA()
	releaseB()

	releaseC, err := reg.Acquire("owner-1", "inst-c")
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	releaseC()
}

func TestSessionRegistryReleaseIsIdempotentAndConcurrentSafe(t *testing.T) {
	t.Parallel()
	reg := NewSessionRegistry(8, 4)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			owner := "owner"
			inst := fmt.Sprintf("inst-%d", i%4)
			release, err := reg.Acquire(owner, inst)
			if err != nil {
				return
			}
			release()
			release() // idempotent
		}(i)
	}
	wg.Wait()
	if got := reg.Count("owner"); got != 0 {
		t.Fatalf("owner count=%d want 0", got)
	}
}

func TestIdleWatchExpiresWithoutTraffic(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	watch := NewIdleWatch(5 * time.Second)
	watch.Touch(now)
	if watch.Expired(now.Add(4 * time.Second)) {
		t.Fatal("should not expire early")
	}
	if !watch.Expired(now.Add(5 * time.Second)) {
		t.Fatal("should expire at idle timeout")
	}
	watch.Touch(now.Add(6 * time.Second))
	if watch.Expired(now.Add(10 * time.Second)) {
		t.Fatal("touch should extend idle deadline")
	}
}

func TestCheckFrameSize(t *testing.T) {
	t.Parallel()
	if err := CheckFrameSize(MaxFrameBytes, MaxFrameBytes); err != nil {
		t.Fatal(err)
	}
	if err := CheckFrameSize(MaxFrameBytes+1, MaxFrameBytes); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err=%v want ErrFrameTooLarge", err)
	}
}
