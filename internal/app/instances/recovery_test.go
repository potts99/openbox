// SPDX-License-Identifier: AGPL-3.0-only

package instances

import (
	"context"
	"errors"
	"testing"

	"github.com/openbox-dev/openbox/internal/domain"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestRecoverCreateAfterCrashAtStartBoundary(t *testing.T) {
	service, runtime, store := newTestService(t, nil)
	runtime.FailNext("instance.start", errors.New("daemon crashed"))
	if _, err := service.Create(context.Background(), createInput()); err == nil {
		t.Fatal("create unexpectedly completed")
	}
	op, found, err := store.GetOperationByIdempotency(context.Background(), "owner-1", "create-key")
	if err != nil || !found || op.Stage != "starting_container" {
		t.Fatalf("operation=%+v found=%v err=%v", op, found, err)
	}
	if err := service.RecoverOperation(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	stored, _, _ := store.GetOperationByIdempotency(context.Background(), "owner-1", "create-key")
	if stored.Status != domain.OperationSucceeded || len(runtime.CreateRequests()) != 1 {
		t.Fatalf("stored=%+v creates=%d", stored, len(runtime.CreateRequests()))
	}
}

func TestRecoverCreateNeverRecreatesMissingPersistentRuntime(t *testing.T) {
	service, runtime, store := newTestService(t, nil)
	runtime.FailNext("instance.start", errors.New("daemon crashed"))
	if _, err := service.Create(context.Background(), createInput()); err == nil {
		t.Fatal("create unexpectedly completed")
	}
	op, _, _ := store.GetOperationByIdempotency(context.Background(), "owner-1", "create-key")
	instance, err := store.GetInstance(context.Background(), "owner-1", domain.InstanceID(op.TargetID))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.DeleteInstance(context.Background(), instance.RuntimeRef); err != nil {
		t.Fatal(err)
	}
	err = service.RecoverOperation(context.Background(), op)
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeRuntimeMissing {
		t.Fatalf("got %v, want runtime_missing", err)
	}
	if len(runtime.CreateRequests()) != 1 {
		t.Fatalf("missing persistent runtime was recreated: creates=%d", len(runtime.CreateRequests()))
	}
	if _, err := runtime.InspectInstance(context.Background(), instance.RuntimeRef); !errors.Is(err, runtimeapi.ErrNotFound) {
		t.Fatalf("runtime unexpectedly exists: %v", err)
	}
}

func TestRecoverLifecycleCrashBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		failureOp string
		prepare   func(*Service, domain.Instance) error
		invoke    func(*Service, domain.Instance, string) error
	}{
		{name: "start", key: "recover-start", failureOp: "instance.start", prepare: func(service *Service, instance domain.Instance) error {
			_, err := service.Stop(context.Background(), instance.OwnerID, instance.ID, "prepare-stop")
			return err
		}, invoke: func(service *Service, instance domain.Instance, key string) error {
			_, err := service.Start(context.Background(), instance.OwnerID, instance.ID, key)
			return err
		}},
		{name: "stop", key: "recover-stop", failureOp: "instance.stop", invoke: func(service *Service, instance domain.Instance, key string) error {
			_, err := service.Stop(context.Background(), instance.OwnerID, instance.ID, key)
			return err
		}},
		{name: "restart", key: "recover-restart", failureOp: "instance.start", invoke: func(service *Service, instance domain.Instance, key string) error {
			_, err := service.Restart(context.Background(), instance.OwnerID, instance.ID, key)
			return err
		}},
		{name: "delete", key: "recover-delete", failureOp: "instance.delete", invoke: func(service *Service, instance domain.Instance, key string) error {
			return service.Delete(context.Background(), instance.OwnerID, instance.ID, key)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, runtime, store := newTestService(t, nil)
			instance, err := service.Create(context.Background(), createInput())
			if err != nil {
				t.Fatal(err)
			}
			if test.prepare != nil {
				if err := test.prepare(service, instance); err != nil {
					t.Fatal(err)
				}
			}
			runtime.FailNext(test.failureOp, errors.New("daemon crashed"))
			if err := test.invoke(service, instance, test.key); err == nil {
				t.Fatal("lifecycle operation unexpectedly completed")
			}
			op, found, err := store.GetOperationByIdempotency(context.Background(), instance.OwnerID, test.key)
			if err != nil || !found || op.Status != domain.OperationRunning {
				t.Fatalf("operation=%+v found=%v err=%v", op, found, err)
			}
			if err := service.RecoverOperation(context.Background(), op); err != nil {
				t.Fatal(err)
			}
			stored, _, _ := store.GetOperationByIdempotency(context.Background(), instance.OwnerID, test.key)
			if stored.Status != domain.OperationSucceeded {
				t.Fatalf("stored=%+v", stored)
			}
		})
	}
}
