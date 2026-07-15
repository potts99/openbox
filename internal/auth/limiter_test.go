// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestLimiterEvictionIsDeterministicWhenTimestampsTie(t *testing.T) {
	limiter := NewLimiter(2, time.Minute, 2)
	now := time.Unix(0, 0)
	if !limiter.Allow("oldest", now) || !limiter.Allow("oldest", now) {
		t.Fatal("failed to fill oldest counter")
	}
	if !limiter.Allow("newest", now) || !limiter.Allow("newest", now) {
		t.Fatal("failed to fill newest counter")
	}
	if !limiter.Allow("replacement", now) {
		t.Fatal("replacement was rejected")
	}
	limiter.mu.Lock()
	_, hasOldest := limiter.entries["oldest"]
	_, hasNewest := limiter.entries["newest"]
	_, hasReplacement := limiter.entries["replacement"]
	limiter.mu.Unlock()
	if hasOldest || !hasNewest || !hasReplacement {
		t.Fatalf("unexpected entries after tied eviction: oldest=%v newest=%v replacement=%v", hasOldest, hasNewest, hasReplacement)
	}
}

func TestLimiterCapacityRemainsBoundedUnderConcurrency(t *testing.T) {
	const capacity = 32
	limiter := NewLimiter(3, time.Minute, capacity)
	now := time.Unix(0, 0)
	var wg sync.WaitGroup
	for i := range 512 {
		wg.Add(1)
		go func() { defer wg.Done(); limiter.Allow(fmt.Sprintf("client-%d", i), now) }()
	}
	wg.Wait()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if len(limiter.entries) != capacity {
		t.Fatalf("entries=%d, want capacity %d", len(limiter.entries), capacity)
	}
}
