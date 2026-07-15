// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/domain"
)

func TestConcurrentBootstrapConsumeCreatesExactlyOneCredential(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/bootstrap-race.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	if err := store.CreateOwner(ctx, domain.Owner{ID: "owner-local", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	manager, err := auth.New(store)
	if err != nil {
		t.Fatal(err)
	}
	manager.WithClock(func() time.Time { return now })
	secret, err := manager.EnsureBootstrap(ctx)
	if err != nil {
		t.Fatal(err)
	}

	const contenders = 4
	start := make(chan struct{})
	var winners atomic.Int32
	var wg sync.WaitGroup
	errorsSeen := make(chan error, contenders)
	for range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := manager.Bootstrap(ctx, secret, "a sufficiently long password")
			if err == nil {
				winners.Add(1)
				return
			}
			errorsSeen <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errorsSeen)
	if winners.Load() != 1 {
		t.Fatalf("bootstrap winners=%d, want 1", winners.Load())
	}
	for err := range errorsSeen {
		if !errors.Is(err, auth.ErrBootstrapUnavailable) {
			t.Fatalf("loser error=%v", err)
		}
	}
	var credentialCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM owner_credentials`).Scan(&credentialCount); err != nil {
		t.Fatal(err)
	}
	if credentialCount != 1 {
		t.Fatalf("credential count=%d, want 1", credentialCount)
	}
	if _, _, err := manager.Bootstrap(ctx, secret, "a sufficiently long password"); !errors.Is(err, auth.ErrBootstrapUnavailable) {
		t.Fatalf("repeat consume error=%v", err)
	}
}
