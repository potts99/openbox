// SPDX-License-Identifier: AGPL-3.0-only

package operations_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/clock"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/operations"
	"github.com/openbox-dev/openbox/internal/persistence/sqlite"
)

func TestWorkerRetriesWithFakeClockAndStructuredEvents(t *testing.T) {
	store, now := operationStore(t)
	op := addOperation(t, store, "instance-1", "op-1", now)
	fakeClock := clock.NewFake(now)
	mode := &operations.Mode{}
	executor := &sequenceExecutor{errors: []error{operations.TransientError("incus_unavailable", errors.New("Incus down")), nil}}
	worker, err := operations.NewWorker(store, executor, operations.Config{WorkerID: "worker", Concurrency: 1, Clock: fakeClock, BaseBackoff: time.Second, MaxBackoff: 4 * time.Second, Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !mode.Degraded() {
		t.Fatal("runtime outage did not enter degraded mode")
	}
	stored, found, err := store.GetOperationByIdempotency(context.Background(), "owner-1", op.IdempotencyKey)
	if err != nil || !found {
		t.Fatal(err)
	}
	if stored.Status != domain.OperationPending || stored.Attempts != 1 || stored.NextAttemptAt == nil || !stored.NextAttemptAt.Equal(now.Add(time.Second)) {
		t.Fatalf("retry=%+v", stored)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if executor.calls() != 1 {
		t.Fatalf("backoff ignored, calls=%d", executor.calls())
	}
	fakeClock.Advance(time.Second)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mode.Degraded() {
		t.Fatal("successful runtime operation did not leave degraded mode")
	}
	stored, _, _ = store.GetOperationByIdempotency(context.Background(), "owner-1", op.IdempotencyKey)
	if stored.Status != domain.OperationSucceeded || stored.Attempts != 2 || stored.ErrorCode != "" || stored.ErrorClass != "" {
		t.Fatalf("completed=%+v", stored)
	}
	events, err := store.ListOperationEvents(context.Background(), "owner-1", op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0].Stage != "claimed" || events[1].Stage != "retry_scheduled" || events[3].Stage != "complete" {
		t.Fatalf("events=%+v", events)
	}
}

func TestWorkerRecoversExpiredAbandonedClaim(t *testing.T) {
	store, now := operationStore(t)
	op := addOperation(t, store, "instance-1", "op-1", now)
	fakeClock := clock.NewFake(now)
	if _, claimed, _, err := store.ClaimOperation(context.Background(), op.ID, "crashed", "claim-a", now, time.Second); err != nil || !claimed {
		t.Fatalf("initial claim=%v err=%v", claimed, err)
	}
	fakeClock.Advance(2 * time.Second)
	executor := &sequenceExecutor{}
	worker, _ := operations.NewWorker(store, executor, operations.Config{WorkerID: "replacement", Concurrency: 1, Clock: fakeClock})
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListOperationEvents(context.Background(), "owner-1", op.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Stage == "recovered_abandoned" {
			found = true
		}
	}
	if !found {
		t.Fatalf("abandonment event missing: %+v", events)
	}
}

func TestExpiredClaimCannotOverwriteReplacementClaim(t *testing.T) {
	store, now := operationStore(t)
	op := addOperation(t, store, "instance-1", "op-1", now)
	claimA, claimed, _, err := store.ClaimOperation(context.Background(), op.ID, "same-worker", "token-a", now, time.Second)
	if err != nil || !claimed {
		t.Fatalf("claim A=%v err=%v", claimed, err)
	}
	claimB, claimed, abandoned, err := store.ClaimOperation(context.Background(), op.ID, "same-worker", "token-b", now.Add(2*time.Second), time.Minute)
	if err != nil || !claimed || !abandoned {
		t.Fatalf("claim B=%v abandoned=%v err=%v", claimed, abandoned, err)
	}
	ctxA := operations.WithClaim(context.Background(), operations.Claim{OwnerID: op.OwnerID, OperationID: op.ID, WorkerID: "same-worker", Token: "token-a"})
	if err := store.UpdateOperationStage(ctxA, op.OwnerID, op.ID, "stale-stage", 50, now.Add(3*time.Second)); err == nil {
		t.Fatal("stale stage write succeeded")
	}
	if err := store.CompleteOperation(ctxA, op.OwnerID, op.ID, now.Add(3*time.Second)); err == nil {
		t.Fatal("stale completion succeeded")
	}
	if err := store.RetryOperation(context.Background(), op.OwnerID, op.ID, "same-worker", "token-a", string(operations.Transient), "stale", "stale A", now.Add(time.Minute), now.Add(3*time.Second)); err == nil {
		t.Fatal("stale retry succeeded")
	}
	stored, _, _ := store.GetOperationByIdempotency(context.Background(), op.OwnerID, op.IdempotencyKey)
	if stored.ClaimToken != "token-b" || stored.Attempts != claimB.Attempts || stored.Stage != claimB.Stage || claimA.ClaimToken != "token-a" {
		t.Fatalf("replacement claim overwritten: stored=%+v claimA=%+v claimB=%+v", stored, claimA, claimB)
	}
}

func TestClaimRenewalPreventsOverlappingTakeover(t *testing.T) {
	store, now := operationStore(t)
	op := addOperation(t, store, "instance-1", "op-1", now)
	if _, claimed, _, err := store.ClaimOperation(context.Background(), op.ID, "worker-a", "token-a", now, time.Second); err != nil || !claimed {
		t.Fatalf("claim A=%v err=%v", claimed, err)
	}
	if renewed, err := store.RenewClaim(context.Background(), op.ID, "worker-a", "token-a", now.Add(500*time.Millisecond), time.Second); err != nil || !renewed {
		t.Fatalf("renewed=%v err=%v", renewed, err)
	}
	if _, claimed, _, err := store.ClaimOperation(context.Background(), op.ID, "worker-b", "token-b", now.Add(1100*time.Millisecond), time.Second); err != nil || claimed {
		t.Fatalf("takeover before renewed lease expiry=%v err=%v", claimed, err)
	}
	if _, claimed, _, err := store.ClaimOperation(context.Background(), op.ID, "worker-b", "token-b", now.Add(1600*time.Millisecond), time.Second); err != nil || !claimed {
		t.Fatalf("takeover after renewed lease expiry=%v err=%v", claimed, err)
	}
}

func TestWorkerBoundsConcurrency(t *testing.T) {
	store, now := operationStore(t)
	for n := 0; n < 6; n++ {
		addOperation(t, store, fmt.Sprintf("instance-%d", n), fmt.Sprintf("op-%d", n), now)
	}
	executor := &concurrencyExecutor{}
	worker, _ := operations.NewWorker(store, executor, operations.Config{WorkerID: "worker", Concurrency: 2, Clock: clock.NewFake(now)})
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if executor.maximum.Load() > 2 {
		t.Fatalf("maximum concurrency=%d", executor.maximum.Load())
	}
	if executor.calls.Load() != 2 {
		t.Fatalf("first bounded batch calls=%d", executor.calls.Load())
	}
}

func TestIntegrityFailureIsTerminalWithoutRetry(t *testing.T) {
	store, now := operationStore(t)
	op := addOperation(t, store, "instance-1", "op-1", now)
	executor := &sequenceExecutor{errors: []error{operations.IntegrityError(domain.CodeRuntimeMissing, errors.New("storage disappeared"))}}
	worker, _ := operations.NewWorker(store, executor, operations.Config{WorkerID: "worker", Concurrency: 1, Clock: clock.NewFake(now)})
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, _, _ := store.GetOperationByIdempotency(context.Background(), "owner-1", op.IdempotencyKey)
	if stored.Status != domain.OperationFailed || stored.ErrorClass != string(operations.Integrity) || stored.ErrorCode != domain.CodeRuntimeMissing {
		t.Fatalf("failed=%+v", stored)
	}
}

type sequenceExecutor struct {
	mu     sync.Mutex
	errors []error
	count  int
}

func (e *sequenceExecutor) Execute(context.Context, domain.Operation) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.count++
	if len(e.errors) == 0 {
		return nil
	}
	err := e.errors[0]
	e.errors = e.errors[1:]
	return err
}
func (e *sequenceExecutor) calls() int { e.mu.Lock(); defer e.mu.Unlock(); return e.count }

type concurrencyExecutor struct{ current, maximum, calls atomic.Int32 }

func (e *concurrencyExecutor) Execute(context.Context, domain.Operation) error {
	current := e.current.Add(1)
	e.calls.Add(1)
	for {
		maximum := e.maximum.Load()
		if current <= maximum || e.maximum.CompareAndSwap(maximum, current) {
			break
		}
	}
	time.Sleep(10 * time.Millisecond)
	e.current.Add(-1)
	return nil
}

func operationStore(t *testing.T) (*sqlite.Store, time.Time) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/openbox.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 7, 15, 2, 0, 0, 0, time.UTC)
	if err := store.CreateOwner(context.Background(), domain.Owner{ID: "owner-1", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return store, now
}
func addOperation(t *testing.T, store *sqlite.Store, instanceID, operationID string, now time.Time) domain.Operation {
	t.Helper()
	instance, err := domain.NewInstance(domain.InstanceID(instanceID), "owner-1", instanceID, domain.KindVPS, now)
	if err != nil {
		t.Fatal(err)
	}
	op := domain.Operation{ID: domain.OperationID(operationID), OwnerID: "owner-1", Type: "instance.start", TargetType: "instance", TargetID: instanceID, Status: domain.OperationPending, Stage: "queued", IdempotencyKey: "key-" + operationID, RequestHash: "hash-" + operationID, CreatedAt: now, UpdatedAt: now}
	if _, _, err := store.CreateInstance(context.Background(), instance, op); err != nil {
		t.Fatal(err)
	}
	return op
}
