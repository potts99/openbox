// SPDX-License-Identifier: AGPL-3.0-only

package sshgateway

import (
	"sync"
	"time"
)

type attemptWindow struct {
	mu         sync.Mutex
	now        func() time.Time
	window     time.Duration
	limitIP    int
	limitKey   int
	maxEntries int
	items      map[string][]time.Time
}

func (w *attemptWindow) allow(ip, key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	allowedIP := w.allowOne("ip:"+ip, now, w.limitIP)
	allowedKey := w.allowOne("key:"+key, now, w.limitKey)
	return allowedIP && allowedKey
}

func (w *attemptWindow) allowOne(key string, now time.Time, limit int) bool {
	if _, exists := w.items[key]; !exists && len(w.items) >= w.maxEntries {
		cutoff := now.Add(-w.window)
		for candidate, values := range w.items {
			if len(values) == 0 || !values[len(values)-1].After(cutoff) {
				delete(w.items, candidate)
			}
		}
		if len(w.items) >= w.maxEntries {
			return false
		}
	}
	values := w.items[key]
	cutoff := now.Add(-w.window)
	first := 0
	for first < len(values) && !values[first].After(cutoff) {
		first++
	}
	values = values[first:]
	if len(values) >= limit {
		w.items[key] = values
		return false
	}
	w.items[key] = append(values, now)
	return true
}

type connectionLimits struct {
	mu             sync.Mutex
	global, perKey int
	active         int
	keys           map[string]int
}

func (l *connectionLimits) acquire(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active >= l.global || l.keys[key] >= l.perKey {
		return false
	}
	l.active++
	l.keys[key]++
	return true
}

func (l *connectionLimits) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.keys[key] > 0 {
		l.keys[key]--
		l.active--
		if l.keys[key] == 0 {
			delete(l.keys, key)
		}
	}
}
