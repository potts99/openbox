// SPDX-License-Identifier: AGPL-3.0-only

package instances

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/operations"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestSubmitCreatePersistsBeforeRuntimeWorkAndReplaysOperation(t *testing.T) {
	service, runtime, store := newTestService(t, nil)
	instance, operation, err := service.SubmitCreate(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != domain.OperationPending || operation.Stage != "runtime" {
		t.Fatalf("operation=%+v", operation)
	}
	if len(runtime.CreateRequests()) != 0 || countCalls(runtime.Calls(), "instance.start") != 0 {
		t.Fatalf("submission performed runtime mutation: %v", runtime.Calls())
	}
	replayedInstance, replayedOperation, err := service.SubmitCreate(context.Background(), createInput())
	if err != nil || replayedInstance.ID != instance.ID || replayedOperation.ID != operation.ID {
		t.Fatalf("replay instance=%+v operation=%+v err=%v", replayedInstance, replayedOperation, err)
	}
	worker, err := operations.NewWorker(store, operationExecutor{service}, operations.Config{WorkerID: "test", Concurrency: 1, Lease: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	completed, err := store.GetOperation(context.Background(), instance.OwnerID, operation.ID)
	if err != nil || completed.Status != domain.OperationSucceeded {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	events, err := store.ListOperationEvents(context.Background(), instance.OwnerID, operation.ID)
	if err != nil || len(events) == 0 || events[len(events)-1].Progress != 100 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestSubmitMutationDoesNotTouchRuntimeAndLostResponseReplays(t *testing.T) {
	service, runtime, store := newTestService(t, nil)
	instance, err := service.Create(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	calls := len(runtime.Calls())
	operation, err := service.SubmitMutation(context.Background(), instance.OwnerID, instance.ID, MutationStop, "async-stop")
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != domain.OperationPending || len(runtime.Calls()) != calls {
		t.Fatalf("operation=%+v calls before=%d after=%d", operation, calls, len(runtime.Calls()))
	}
	replayed, err := service.SubmitMutation(context.Background(), instance.OwnerID, instance.ID, MutationStop, "async-stop")
	if err != nil || replayed.ID != operation.ID {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	conflict, err := service.SubmitMutation(context.Background(), instance.OwnerID, instance.ID, MutationStart, "async-stop")
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeIdempotencyConflict || conflict.ID != "" {
		t.Fatalf("conflict=%+v err=%v", conflict, err)
	}
	worker, _ := operations.NewWorker(store, operationExecutor{service}, operations.Config{WorkerID: "test", Concurrency: 1, Lease: time.Minute})
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	actual, err := runtime.InspectInstance(context.Background(), instance.RuntimeRef)
	if err != nil || actual.State != runtimeapi.StateStopped {
		t.Fatalf("actual=%+v err=%v", actual, err)
	}
}

func TestCancelPendingCreateIsDurableAndRemovesPlaceholder(t *testing.T) {
	service, runtime, store := newTestService(t, nil)
	instance, operation, err := service.SubmitCreate(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := service.CancelOperation(context.Background(), instance.OwnerID, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != domain.OperationFailed || canceled.Stage != "canceled" || canceled.ErrorCode != domain.CodeOperationCanceled {
		t.Fatalf("canceled=%+v", canceled)
	}
	replayed, err := service.CancelOperation(context.Background(), instance.OwnerID, operation.ID)
	if err != nil || replayed.ID != canceled.ID {
		t.Fatalf("cancel replay=%+v err=%v", replayed, err)
	}
	if _, err := store.GetInstance(context.Background(), instance.OwnerID, instance.ID); err == nil {
		t.Fatal("canceled create placeholder remains")
	}
	worker, _ := operations.NewWorker(store, operationExecutor{service}, operations.Config{WorkerID: "test", Concurrency: 1, Lease: time.Minute})
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runtime.CreateRequests()) != 0 {
		t.Fatal("canceled operation reached runtime")
	}
}

type operationExecutor struct{ service *Service }

func (e operationExecutor) Execute(ctx context.Context, operation domain.Operation) error {
	return e.service.RecoverOperation(ctx, operation)
}

func TestCancelRefusesClaimedOrIrreversibleOperation(t *testing.T) {
	service, _, store := newTestService(t, nil)
	instance, operation, err := service.SubmitCreate(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, _, err := store.ClaimOperation(context.Background(), operation.ID, "worker", "token", time.Now(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	_, err = service.CancelOperation(context.Background(), instance.OwnerID, operation.ID)
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeCancellationUnsafe {
		t.Fatalf("got %v", err)
	}
}

func TestCancelAndClaimRaceHasExactlyOneWinner(t *testing.T) {
	for iteration := 0; iteration < 20; iteration++ {
		t.Run(fmt.Sprintf("race-%d", iteration), func(t *testing.T) {
			service, _, store := newTestService(t, nil)
			instance, operation, err := service.SubmitCreate(context.Background(), createInput())
			if err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			var claimed bool
			var cancelErr error
			go func() {
				defer wg.Done()
				<-start
				_, claimed, _, _ = store.ClaimOperation(context.Background(), operation.ID, "worker", "token", time.Now(), time.Minute)
			}()
			go func() {
				defer wg.Done()
				<-start
				_, cancelErr = service.CancelOperation(context.Background(), instance.OwnerID, operation.ID)
			}()
			close(start)
			wg.Wait()
			canceled := cancelErr == nil
			if claimed == canceled {
				t.Fatalf("claimed=%v canceled=%v cancelErr=%v", claimed, canceled, cancelErr)
			}
		})
	}
}

func TestCancelPendingMutationRestoresPreviousDesiredState(t *testing.T) {
	service, _, store := newTestService(t, nil)
	instance, err := service.Create(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	operation, err := service.SubmitAction(context.Background(), instance.OwnerID, instance.ID, MutationStop, "cancel-stop")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.GetInstance(context.Background(), instance.OwnerID, instance.ID)
	if err != nil || pending.DesiredState != domain.DesiredStopped {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if _, err := service.CancelOperation(context.Background(), instance.OwnerID, operation.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := store.GetInstance(context.Background(), instance.OwnerID, instance.ID)
	if err != nil || restored.DesiredState != domain.DesiredRunning {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
}

func TestSubmissionRefusesDegradedModeWhileReadsRemainAvailable(t *testing.T) {
	service, _, _ := newTestService(t, nil)
	service.mode.SetDegraded(true)
	if _, err := service.ListInstances(context.Background(), "owner-1"); err != nil {
		t.Fatalf("metadata read in degraded mode: %v", err)
	}
	_, _, err := service.SubmitCreate(context.Background(), createInput())
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeUnavailable {
		t.Fatalf("got %v", err)
	}
}

func TestCreateReplayAndConflictRemainStableWhileDegraded(t *testing.T) {
	service, _, _ := newTestService(t, nil)
	instance, operation, err := service.SubmitCreate(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	service.mode.SetDegraded(true)
	replayedInstance, replayedOperation, err := service.SubmitCreate(context.Background(), createInput())
	if err != nil || replayedInstance.ID != instance.ID || replayedOperation.ID != operation.ID {
		t.Fatalf("instance=%+v operation=%+v err=%v", replayedInstance, replayedOperation, err)
	}
	conflicting := createInput()
	conflicting.Name = "different"
	_, _, err = service.SubmitCreate(context.Background(), conflicting)
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeIdempotencyConflict {
		t.Fatalf("got %v", err)
	}
}

func TestActionReplayAndConflictRemainStableWhileDegraded(t *testing.T) {
	service, _, _ := newTestService(t, nil)
	instance, err := service.Create(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	operation, err := service.SubmitAction(context.Background(), instance.OwnerID, instance.ID, MutationStop, "degraded-replay")
	if err != nil {
		t.Fatal(err)
	}
	service.mode.SetDegraded(true)
	replayed, err := service.SubmitAction(context.Background(), instance.OwnerID, instance.ID, MutationStop, "degraded-replay")
	if err != nil || replayed.ID != operation.ID {
		t.Fatalf("operation=%+v err=%v", replayed, err)
	}
	_, err = service.SubmitAction(context.Background(), instance.OwnerID, instance.ID, MutationStart, "degraded-replay")
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeIdempotencyConflict {
		t.Fatalf("got %v", err)
	}
}

func TestCancelOlderPendingActionCannotClobberNewerIntent(t *testing.T) {
	service, _, store := newTestService(t, nil)
	instance, err := service.Create(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	older, err := service.SubmitAction(context.Background(), instance.OwnerID, instance.ID, MutationStop, "older-stop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SubmitAction(context.Background(), instance.OwnerID, instance.ID, MutationDelete, "newer-delete"); err != nil {
		t.Fatal(err)
	}
	_, err = service.CancelOperation(context.Background(), instance.OwnerID, older.ID)
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeCancellationUnsafe {
		t.Fatalf("got %v", err)
	}
	stored, err := store.GetInstance(context.Background(), instance.OwnerID, instance.ID)
	if err != nil || stored.DesiredState != domain.DesiredDeleted || stored.ObservedState != domain.ObservedDeleting {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestCompletedDeleteSubmissionReplaysAfterTargetIsTombstoned(t *testing.T) {
	service, _, store := newTestService(t, nil)
	instance, err := service.Create(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	operation, err := service.SubmitAction(context.Background(), instance.OwnerID, instance.ID, MutationDelete, "async-delete")
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := operations.NewWorker(store, operationExecutor{service}, operations.Config{WorkerID: "test", Concurrency: 1, Lease: time.Minute})
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	replayed, err := service.SubmitAction(context.Background(), instance.OwnerID, instance.ID, MutationDelete, "async-delete")
	if err != nil || replayed.ID != operation.ID || replayed.Status != domain.OperationSucceeded {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
}
