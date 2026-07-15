// SPDX-License-Identifier: AGPL-3.0-only

package instances

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/persistence/sqlite"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
)

func TestAuthorizedKeysIncludesSeparateGatewayCredential(t *testing.T) {
	if got := authorizedKeys("ssh-ed25519 owner", "ssh-ed25519 gateway"); got != "ssh-ed25519 owner\nssh-ed25519 gateway" {
		t.Fatalf("authorized keys=%q", got)
	}
	if got := authorizedKeys("ssh-ed25519 gateway", "ssh-ed25519 gateway"); got != "ssh-ed25519 gateway" {
		t.Fatalf("duplicate key not removed: %q", got)
	}
}

func TestNewRequiresNetworkPolicy(t *testing.T) {
	runtime := fake.New(testCapabilities())
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/openbox.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := New(runtime, store, Options{}); err == nil {
		t.Fatal("missing network policy was accepted")
	}
}

func TestContainerLifecycle(t *testing.T) {
	service, runtime, store := newTestService(t, nil)
	ctx := context.Background()
	created, err := service.Create(ctx, createInput())
	if err != nil {
		t.Fatal(err)
	}
	if created.ObservedState != domain.ObservedRunning || created.ActualIsolation != domain.IsolationContainer {
		t.Fatalf("created=%+v", created)
	}
	if created.RuntimeRef == created.Name || created.RuntimeRef == "" {
		t.Fatalf("runtime ref was not separate: %q", created.RuntimeRef)
	}
	callsBeforeReplay := len(runtime.Calls())
	replayed, err := service.Create(ctx, createInput())
	if err != nil || replayed.ID != created.ID {
		t.Fatalf("idempotent replay=%+v err=%v", replayed, err)
	}
	if callsAfterReplay := len(runtime.Calls()); callsAfterReplay != callsBeforeReplay {
		t.Fatalf("create replay called runtime: before=%d after=%d", callsBeforeReplay, callsAfterReplay)
	}
	createOperation, found, err := store.GetOperationByIdempotency(ctx, created.OwnerID, "create-key")
	if err != nil || !found || createOperation.Status != domain.OperationSucceeded || createOperation.Stage != "complete" || createOperation.Progress != 100 {
		t.Fatalf("create operation=%+v found=%v err=%v", createOperation, found, err)
	}
	runtimeInstance, err := runtime.InspectInstance(ctx, created.RuntimeRef)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeInstance.Image != "sha256:ubuntu" || runtimeInstance.IsVM || runtimeInstance.Privileged {
		t.Fatalf("runtime instance=%+v", runtimeInstance)
	}
	requests := runtime.CreateRequests()
	if len(requests) != 1 {
		t.Fatalf("create requests=%d", len(requests))
	}
	request := requests[0]
	if request.Image != "sha256:ubuntu" || request.OwnerPublicKey != "ssh-ed25519 owner" || !request.Unprivileged {
		t.Fatalf("request=%+v", request)
	}
	if request.Resources.VCPUs != 2 || request.Resources.MemoryBytes != 1024 || request.Resources.DiskBytes != 2048 {
		t.Fatalf("resources=%+v", request.Resources)
	}
	if request.Metadata[MetadataInstanceID] != string(created.ID) || request.Metadata[MetadataOwnerID] != string(created.OwnerID) {
		t.Fatalf("metadata=%v", request.Metadata)
	}

	stopped, err := service.Stop(ctx, created.OwnerID, created.ID, "stop-key")
	if err != nil || stopped.ObservedState != domain.ObservedStopped || stopped.DesiredState != domain.DesiredStopped {
		t.Fatalf("stop=%+v err=%v", stopped, err)
	}
	started, err := service.Start(ctx, created.OwnerID, created.ID, "start-key")
	if err != nil || started.ObservedState != domain.ObservedRunning {
		t.Fatalf("start=%+v err=%v", started, err)
	}
	restarted, err := service.Restart(ctx, created.OwnerID, created.ID, "restart-key")
	if err != nil || restarted.ObservedState != domain.ObservedRunning {
		t.Fatalf("restart=%+v err=%v", restarted, err)
	}
	inspected, err := service.Inspect(ctx, created.OwnerID, created.ID)
	if err != nil || inspected.RuntimeRef != created.RuntimeRef {
		t.Fatalf("inspect=%+v err=%v", inspected, err)
	}
	if err := service.Delete(ctx, created.OwnerID, created.ID, "delete-key"); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, created.OwnerID, created.ID, "delete-key"); err != nil {
		t.Fatalf("repeated delete: %v", err)
	}
	if _, err := runtime.InspectInstance(ctx, created.RuntimeRef); !errors.Is(err, runtimeapi.ErrNotFound) {
		t.Fatalf("runtime remains: %v", err)
	}
	if _, err := store.GetInstance(ctx, created.OwnerID, created.ID); err == nil {
		t.Fatal("active metadata remains")
	}
}

func TestImageAliasChangesAffectFutureCreatesOnly(t *testing.T) {
	service, runtime, store := newTestService(t, nil)
	first, err := service.Create(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	runtime.RemoveImage("sha256:ubuntu")
	runtime.AddImage(runtimeapi.Image{Fingerprint: "sha256:new-ubuntu", Aliases: []string{"ubuntu"}, Architecture: "x86_64", Type: "container", CloudInit: true})
	secondInput := createInput()
	secondInput.Name = "project-two"
	secondInput.IdempotencyKey = "create-key-two"
	second, err := service.Create(context.Background(), secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.ImageID != "sha256:ubuntu" || second.ImageID != "sha256:new-ubuntu" {
		t.Fatalf("first=%s second=%s", first.ImageID, second.ImageID)
	}
	requests := runtime.CreateRequests()
	if len(requests) != 2 || requests[0].Image != "sha256:ubuntu" || requests[1].Image != "sha256:new-ubuntu" {
		t.Fatalf("requests=%+v", requests)
	}
	// Existing instance metadata stays pinned even after the alias moves.
	reloaded, err := store.GetInstance(context.Background(), first.OwnerID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ImageID != "sha256:ubuntu" {
		t.Fatalf("pinned image drifted to %q after alias update", reloaded.ImageID)
	}
}

func TestLifecycleFailureInjection(t *testing.T) {
	injected := errors.New("injected")
	tests := []struct {
		name      string
		operation string
		prepare   func(*Service, domain.Instance) error
		invoke    func(*Service, domain.Instance) error
	}{
		{name: "refresh inspect", operation: "instance.inspect", invoke: func(service *Service, instance domain.Instance) error {
			_, err := service.Refresh(context.Background(), instance.OwnerID, instance.ID)
			return err
		}},
		{name: "stop", operation: "instance.stop", invoke: func(service *Service, instance domain.Instance) error {
			_, err := service.Stop(context.Background(), instance.OwnerID, instance.ID, "stop-failure")
			return err
		}},
		{name: "start", operation: "instance.start", prepare: func(service *Service, instance domain.Instance) error {
			_, err := service.Stop(context.Background(), instance.OwnerID, instance.ID, "prepare-stop")
			return err
		}, invoke: func(service *Service, instance domain.Instance) error {
			_, err := service.Start(context.Background(), instance.OwnerID, instance.ID, "start-failure")
			return err
		}},
		{name: "restart stop", operation: "instance.stop", invoke: func(service *Service, instance domain.Instance) error {
			_, err := service.Restart(context.Background(), instance.OwnerID, instance.ID, "restart-stop-failure")
			return err
		}},
		{name: "delete", operation: "instance.delete", prepare: func(service *Service, instance domain.Instance) error {
			_, err := service.Stop(context.Background(), instance.OwnerID, instance.ID, "prepare-delete-stop")
			return err
		}, invoke: func(service *Service, instance domain.Instance) error {
			return service.Delete(context.Background(), instance.OwnerID, instance.ID, "delete-failure")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, runtime, _ := newTestService(t, nil)
			instance, err := service.Create(context.Background(), createInput())
			if err != nil {
				t.Fatal(err)
			}
			if tt.prepare != nil {
				if err := tt.prepare(service, instance); err != nil {
					t.Fatal(err)
				}
			}
			runtime.FailNext(tt.operation, injected)
			if err := tt.invoke(service, instance); !errors.Is(err, injected) {
				t.Fatalf("got %v, want injected failure", err)
			}
		})
	}
}

func TestDeleteVerificationFailureCanBeRetriedSafely(t *testing.T) {
	baseService, runtime, store := newTestService(t, nil)
	instance, err := baseService.Create(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	failing := &failureRuntime{ContainerRuntime: runtime, operation: "instance.inspect", nth: 2, failure: errors.New("verification unavailable")}
	retryService, err := New(failing, store, Options{Now: func() time.Time { return time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC) }, NewID: newIDs("delete-operation"), NetworkPolicy: testNetworkPolicy{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := retryService.Delete(context.Background(), instance.OwnerID, instance.ID, "delete-first"); err == nil {
		t.Fatal("verification failure was ignored")
	}
	firstOperation, found, err := store.GetOperationByIdempotency(context.Background(), instance.OwnerID, "delete-first")
	if err != nil || !found {
		t.Fatalf("delete operation missing: %+v %v", firstOperation, err)
	}
	finalService, err := New(runtime, store, Options{Now: func() time.Time { return time.Date(2026, 7, 15, 0, 0, 1, 0, time.UTC) }, NewID: newIDs("must-not-be-used"), NetworkPolicy: testNetworkPolicy{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := finalService.Delete(context.Background(), instance.OwnerID, instance.ID, "delete-first"); err != nil {
		t.Fatalf("safe retry: %v", err)
	}
	replayedOperation, found, err := store.GetOperationByIdempotency(context.Background(), instance.OwnerID, "delete-first")
	if err != nil || !found || replayedOperation.ID != firstOperation.ID || replayedOperation.Status != domain.OperationSucceeded || replayedOperation.Stage != "complete" {
		t.Fatalf("operation replay=%+v found=%v err=%v", replayedOperation, found, err)
	}
}

func TestCompletedMutationReplayCannotReapplyStaleEffects(t *testing.T) {
	tests := []struct {
		name        string
		first       func(*Service, domain.Instance) error
		newer       func(*Service, domain.Instance) error
		replay      func(*Service, domain.Instance) (domain.Instance, error)
		staleCall   string
		wantState   runtimeapi.InstanceState
		wantDesired domain.DesiredState
	}{
		{name: "old stop after start", staleCall: "instance.stop", wantState: runtimeapi.StateRunning, wantDesired: domain.DesiredRunning,
			first: func(s *Service, i domain.Instance) error {
				_, e := s.Stop(context.Background(), i.OwnerID, i.ID, "old-stop")
				return e
			},
			newer: func(s *Service, i domain.Instance) error {
				_, e := s.Start(context.Background(), i.OwnerID, i.ID, "new-start")
				return e
			},
			replay: func(s *Service, i domain.Instance) (domain.Instance, error) {
				return s.Stop(context.Background(), i.OwnerID, i.ID, "old-stop")
			}},
		{name: "old start after stop", staleCall: "instance.start", wantState: runtimeapi.StateStopped, wantDesired: domain.DesiredStopped,
			first: func(s *Service, i domain.Instance) error {
				_, e := s.Start(context.Background(), i.OwnerID, i.ID, "old-start")
				return e
			},
			newer: func(s *Service, i domain.Instance) error {
				_, e := s.Stop(context.Background(), i.OwnerID, i.ID, "new-stop")
				return e
			},
			replay: func(s *Service, i domain.Instance) (domain.Instance, error) {
				return s.Start(context.Background(), i.OwnerID, i.ID, "old-start")
			}},
		{name: "old restart after stop", staleCall: "instance.stop", wantState: runtimeapi.StateStopped, wantDesired: domain.DesiredStopped,
			first: func(s *Service, i domain.Instance) error {
				_, e := s.Restart(context.Background(), i.OwnerID, i.ID, "old-restart")
				return e
			},
			newer: func(s *Service, i domain.Instance) error {
				_, e := s.Stop(context.Background(), i.OwnerID, i.ID, "new-stop")
				return e
			},
			replay: func(s *Service, i domain.Instance) (domain.Instance, error) {
				return s.Restart(context.Background(), i.OwnerID, i.ID, "old-restart")
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, runtime, store := newTestService(t, nil)
			instance, err := service.Create(context.Background(), createInput())
			if err != nil {
				t.Fatal(err)
			}
			if err := tt.first(service, instance); err != nil {
				t.Fatal(err)
			}
			if err := tt.newer(service, instance); err != nil {
				t.Fatal(err)
			}
			before := countCalls(runtime.Calls(), tt.staleCall)
			result, err := tt.replay(service, instance)
			if err != nil {
				t.Fatal(err)
			}
			after := countCalls(runtime.Calls(), tt.staleCall)
			if after != before {
				t.Fatalf("stale replay repeated %s: before=%d after=%d", tt.staleCall, before, after)
			}
			runtimeInstance, err := runtime.InspectInstance(context.Background(), instance.RuntimeRef)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := store.GetInstance(context.Background(), instance.OwnerID, instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			if runtimeInstance.State != tt.wantState || stored.DesiredState != tt.wantDesired || result.DesiredState != tt.wantDesired {
				t.Fatalf("runtime=%s stored=%s result=%s", runtimeInstance.State, stored.DesiredState, result.DesiredState)
			}
		})
	}
}

func TestConcurrentIdenticalMutationUsesOneOperationAndRuntimeCall(t *testing.T) {
	service, runtime, store := newTestService(t, nil)
	instance, err := service.Create(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	const workers = 16
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for n := 0; n < workers; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.Stop(context.Background(), instance.OwnerID, instance.ID, "concurrent-stop")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent stop: %v", err)
		}
	}
	if calls := countCalls(runtime.Calls(), "instance.stop"); calls != 1 {
		t.Fatalf("runtime stop calls=%d, want 1", calls)
	}
	op, found, err := store.GetOperationByIdempotency(context.Background(), instance.OwnerID, "concurrent-stop")
	if err != nil || !found || op.Status != domain.OperationSucceeded || op.Stage != "complete" {
		t.Fatalf("operation=%+v found=%v err=%v", op, found, err)
	}
}

func TestMutationIdempotencyAndConflictingReuse(t *testing.T) {
	service, runtime, store := newTestService(t, nil)
	instance, err := service.Create(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Stop(context.Background(), instance.OwnerID, instance.ID, "shared-mutation-key"); err != nil {
		t.Fatal(err)
	}
	first, found, err := store.GetOperationByIdempotency(context.Background(), instance.OwnerID, "shared-mutation-key")
	if err != nil || !found {
		t.Fatalf("operation=%+v found=%v err=%v", first, found, err)
	}
	if _, err := service.Stop(context.Background(), instance.OwnerID, instance.ID, "shared-mutation-key"); err != nil {
		t.Fatalf("same mutation replay: %v", err)
	}
	replayed, found, err := store.GetOperationByIdempotency(context.Background(), instance.OwnerID, "shared-mutation-key")
	if err != nil || !found || replayed.ID != first.ID {
		t.Fatalf("replayed=%+v found=%v err=%v", replayed, found, err)
	}
	_, err = service.Start(context.Background(), instance.OwnerID, instance.ID, "shared-mutation-key")
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeIdempotencyConflict {
		t.Fatalf("mismatched reuse=%v", err)
	}

	if _, err := service.Start(context.Background(), instance.OwnerID, instance.ID, "start-before-restart"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Restart(context.Background(), instance.OwnerID, instance.ID, "restart-replay"); err != nil {
		t.Fatal(err)
	}
	callsBefore := countCalls(runtime.Calls(), "instance.stop") + countCalls(runtime.Calls(), "instance.start")
	if _, err := service.Restart(context.Background(), instance.OwnerID, instance.ID, "restart-replay"); err != nil {
		t.Fatal(err)
	}
	callsAfter := countCalls(runtime.Calls(), "instance.stop") + countCalls(runtime.Calls(), "instance.start")
	if callsAfter != callsBefore {
		t.Fatalf("completed restart replay repeated runtime mutation: before=%d after=%d", callsBefore, callsAfter)
	}
}

func TestCreateCapabilityErrors(t *testing.T) {
	tests := []struct {
		name       string
		caps       runtimeapi.Capabilities
		image      runtimeapi.Image
		mutate     func(*CreateInput)
		capability string
	}{
		{name: "containers unavailable", caps: runtimeapi.Capabilities{Architecture: "x86_64"}, image: testImage(), capability: "containers"},
		{name: "strong isolation", caps: testCapabilities(), image: testImage(), mutate: func(i *CreateInput) { i.RequestedIsolation = domain.IsolationStrong }, capability: "strong_isolation"},
		{name: "vm image", caps: testCapabilities(), image: runtimeapi.Image{Fingerprint: "sha256:ubuntu", Aliases: []string{"ubuntu"}, Architecture: "x86_64", Type: "virtual-machine"}, capability: "container_image"},
		{name: "architecture", caps: testCapabilities(), image: runtimeapi.Image{Fingerprint: "sha256:ubuntu", Aliases: []string{"ubuntu"}, Architecture: "aarch64", Type: "container"}, capability: "image_architecture"},
		{name: "cloud init", caps: testCapabilities(), image: runtimeapi.Image{Fingerprint: "sha256:ubuntu", Aliases: []string{"ubuntu"}, Architecture: "x86_64", Type: "container"}, capability: "image_cloud_init"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := fake.New(tt.caps)
			r.AddImage(tt.image)
			service, _, _ := newTestService(t, r)
			input := createInput()
			if tt.mutate != nil {
				tt.mutate(&input)
			}
			_, err := service.Create(context.Background(), input)
			var capabilityErr *CapabilityError
			if !errors.As(err, &capabilityErr) || capabilityErr.Capability != tt.capability {
				t.Fatalf("got %v", err)
			}
			if len(r.CreateRequests()) != 0 {
				t.Fatal("capability error occurred after runtime create")
			}
		})
	}
}

func TestCreateFailureInjectionAtEveryRuntimeCall(t *testing.T) {
	tests := []struct {
		operation string
		nth       int
	}{
		{"capabilities", 1}, {"images.list", 1}, {"instance.inspect", 1},
		{"instance.create", 1}, {"instance.start", 1}, {"instance.inspect", 2},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s-%d", tt.operation, tt.nth), func(t *testing.T) {
			base := fake.New(testCapabilities())
			base.AddImage(testImage())
			failing := &failureRuntime{ContainerRuntime: base, operation: tt.operation, nth: tt.nth, failure: errors.New("injected")}
			service, _, _ := newTestService(t, failing)
			if _, err := service.Create(context.Background(), createInput()); err == nil {
				t.Fatal("injected failure was ignored")
			}
		})
	}
}

func TestDeleteRefusesReplacementRuntimeIdentity(t *testing.T) {
	service, runtime, _ := newTestService(t, nil)
	ctx := context.Background()
	created, err := service.Create(ctx, createInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.DeleteInstance(ctx, created.RuntimeRef); err != nil {
		t.Fatal(err)
	}
	_, err = runtime.CreateInstance(ctx, runtimeapi.CreateRequest{
		Ref: created.RuntimeRef, Image: "sha256:ubuntu", Unprivileged: true,
		Metadata: managedMetadata(created.OwnerID, "replacement-id"),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = service.Delete(ctx, created.OwnerID, created.ID, "delete-key")
	var conflict *IdentityConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("got %v", err)
	}
	if _, err := runtime.InspectInstance(ctx, created.RuntimeRef); err != nil {
		t.Fatalf("replacement was touched: %v", err)
	}
}

func TestCreateRefusesUnmanagedRuntimeCollision(t *testing.T) {
	r := fake.New(testCapabilities())
	r.AddImage(testImage())
	ids := newIDs("predictable-instance")
	ref := runtimeReference("predictable-instance")
	if _, err := r.CreateInstance(context.Background(), runtimeapi.CreateRequest{Ref: ref, Image: "sha256:ubuntu", Unprivileged: true}); err != nil {
		t.Fatal(err)
	}
	service, _, store := newTestServiceWithIDs(t, r, ids)
	_, err := service.Create(context.Background(), createInput())
	var conflict *IdentityConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("got %v", err)
	}
	if _, err := store.GetInstance(context.Background(), "owner-1", "predictable-instance"); err == nil {
		t.Fatal("collision was adopted")
	}
}

func TestRefreshMissingRuntimeMarksIntegrityError(t *testing.T) {
	service, runtime, store := newTestService(t, nil)
	created, err := service.Create(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.DeleteInstance(context.Background(), created.RuntimeRef); err != nil {
		t.Fatal(err)
	}
	_, err = service.Refresh(context.Background(), created.OwnerID, created.ID)
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeRuntimeMissing {
		t.Fatalf("got %v", err)
	}
	stored, err := store.GetInstance(context.Background(), created.OwnerID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ObservedState != domain.ObservedError || stored.ErrorCode != domain.CodeRuntimeMissing {
		t.Fatalf("stored=%+v", stored)
	}
}

func TestMutationsRecordMissingRuntime(t *testing.T) {
	tests := []struct {
		name   string
		invoke func(*Service, domain.Instance) error
	}{
		{name: "start", invoke: func(service *Service, instance domain.Instance) error {
			_, err := service.Start(context.Background(), instance.OwnerID, instance.ID, "start-missing")
			return err
		}},
		{name: "stop", invoke: func(service *Service, instance domain.Instance) error {
			_, err := service.Stop(context.Background(), instance.OwnerID, instance.ID, "stop-missing")
			return err
		}},
		{name: "restart", invoke: func(service *Service, instance domain.Instance) error {
			_, err := service.Restart(context.Background(), instance.OwnerID, instance.ID, "restart-missing")
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, runtime, store := newTestService(t, nil)
			instance, err := service.Create(context.Background(), createInput())
			if err != nil {
				t.Fatal(err)
			}
			if err := runtime.DeleteInstance(context.Background(), instance.RuntimeRef); err != nil {
				t.Fatal(err)
			}
			err = tt.invoke(service, instance)
			var domainErr *domain.Error
			if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeRuntimeMissing {
				t.Fatalf("got %v", err)
			}
			stored, err := store.GetInstance(context.Background(), instance.OwnerID, instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.ObservedState != domain.ObservedError || stored.ErrorCode != domain.CodeRuntimeMissing {
				t.Fatalf("stored=%+v", stored)
			}
		})
	}
}

func newTestService(t *testing.T, supplied ContainerRuntime) (*Service, *fake.Runtime, *sqlite.Store) {
	t.Helper()
	var base *fake.Runtime
	if supplied == nil {
		base = fake.New(testCapabilities())
		base.AddImage(testImage())
		supplied = base
	}
	if typed, ok := supplied.(*fake.Runtime); ok {
		base = typed
	}
	return newTestServiceWithIDs(t, supplied, newIDs("instance-1", "operation-1", "operation-2", "operation-3", "operation-4", "operation-5", "operation-6", "operation-7"), base)
}

func newTestServiceWithIDs(t *testing.T, runtime ContainerRuntime, ids func() string, bases ...*fake.Runtime) (*Service, *fake.Runtime, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/openbox.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	if err := store.CreateOwner(context.Background(), domain.Owner{ID: "owner-1", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	service, err := New(runtime, store, Options{Now: func() time.Time { return now }, NewID: ids, NetworkPolicy: testNetworkPolicy{}})
	if err != nil {
		t.Fatal(err)
	}
	var base *fake.Runtime
	if len(bases) > 0 {
		base = bases[0]
	}
	return service, base, store
}

func newPolicyTestService(t *testing.T, runtime ContainerRuntime, policy *recordingNetworkPolicy) (*Service, *fake.Runtime, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/openbox.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	if err := store.CreateOwner(context.Background(), domain.Owner{ID: "owner-1", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	policy.observe = func(instance domain.Instance) {
		stored, err := store.GetInstance(context.Background(), instance.OwnerID, instance.ID)
		if err != nil {
			t.Errorf("read instance while applying policy: %v", err)
			return
		}
		policy.observedWhenApplied = stored.ObservedState
	}
	service, err := New(runtime, store, Options{
		Now:           func() time.Time { return now },
		NewID:         newIDs("instance-1", "operation-1", "operation-2"),
		NetworkPolicy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := runtime.(*fake.Runtime)
	return service, base, store
}

type recordingNetworkPolicy struct {
	runtime                *fake.Runtime
	applyErr               error
	verifyErr              error
	status                 domain.NetworkPolicyStatus
	calls                  []string
	observe                func(domain.Instance)
	verify                 func(domain.Instance)
	observedWhenApplied    domain.ObservedState
	runtimeGoneWhenRemoved bool
}

type testNetworkPolicy struct{}

func (testNetworkPolicy) ApplyNetworkPolicy(context.Context, domain.Instance) error { return nil }
func (testNetworkPolicy) RemoveNetworkPolicy(context.Context, domain.Instance) error {
	return nil
}

func (p *recordingNetworkPolicy) ApplyNetworkPolicy(_ context.Context, instance domain.Instance) error {
	p.calls = append(p.calls, "apply:"+string(instance.ID))
	if p.observe != nil {
		p.observe(instance)
	}
	return p.applyErr
}

func (p *recordingNetworkPolicy) VerifyNetworkPolicy(_ context.Context, instance domain.Instance) error {
	if p.verify != nil {
		p.verify(instance)
	}
	return p.verifyErr
}

func (p *recordingNetworkPolicy) RemoveNetworkPolicy(ctx context.Context, instance domain.Instance) error {
	p.calls = append(p.calls, "remove:"+string(instance.ID))
	_, err := p.runtime.InspectInstance(ctx, instance.RuntimeRef)
	p.runtimeGoneWhenRemoved = errors.Is(err, runtimeapi.ErrNotFound)
	return nil
}

func (p *recordingNetworkPolicy) NetworkPolicyStatus(domain.Instance) domain.NetworkPolicyStatus {
	return p.status
}

func createInput() CreateInput {
	return CreateInput{OwnerID: "owner-1", Name: "project", Kind: domain.KindDevbox, Image: "ubuntu", RequestedIsolation: domain.IsolationStandard,
		Resources: domain.Resources{VCPUs: 2, MemoryBytes: 1024, DiskBytes: 2048}, OwnerPublicKey: "ssh-ed25519 owner", IdempotencyKey: "create-key"}
}

func testCapabilities() runtimeapi.Capabilities {
	return runtimeapi.Capabilities{Architecture: "x86_64", Containers: true}
}
func testImage() runtimeapi.Image {
	return runtimeapi.Image{Fingerprint: "sha256:ubuntu", Aliases: []string{"ubuntu"}, Architecture: "x86_64", Type: "container", CloudInit: true}
}

func newIDs(values ...string) func() string {
	var mu sync.Mutex
	index := 0
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		if index >= len(values) {
			value := fmt.Sprintf("generated-%d", index)
			index++
			return value
		}
		value := values[index]
		index++
		return value
	}
}

func countCalls(calls []string, wanted string) int {
	count := 0
	for _, call := range calls {
		if call == wanted {
			count++
		}
	}
	return count
}

type failureRuntime struct {
	ContainerRuntime
	mu         sync.Mutex
	operation  string
	nth, calls int
	failure    error
}

type failingObservationRepository struct {
	Repository
	err error
}

func (r failingObservationRepository) UpdateInstanceObservation(context.Context, domain.OwnerID, domain.InstanceID, string, domain.IsolationType, domain.ObservedState, domain.ErrorCode, time.Time) error {
	return r.err
}

func (r *failureRuntime) fail(operation string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if operation != r.operation {
		return nil
	}
	r.calls++
	if r.calls == r.nth {
		return r.failure
	}
	return nil
}

func (r *failureRuntime) DiscoverCapabilities(ctx context.Context) (runtimeapi.Capabilities, error) {
	if err := r.fail("capabilities"); err != nil {
		return runtimeapi.Capabilities{}, err
	}
	return r.ContainerRuntime.DiscoverCapabilities(ctx)
}
func (r *failureRuntime) ListImages(ctx context.Context) ([]runtimeapi.Image, error) {
	if err := r.fail("images.list"); err != nil {
		return nil, err
	}
	return r.ContainerRuntime.ListImages(ctx)
}
func (r *failureRuntime) InspectInstance(ctx context.Context, ref string) (runtimeapi.Instance, error) {
	if err := r.fail("instance.inspect"); err != nil {
		return runtimeapi.Instance{}, err
	}
	return r.ContainerRuntime.InspectInstance(ctx, ref)
}
func (r *failureRuntime) CreateInstance(ctx context.Context, request runtimeapi.CreateRequest) (runtimeapi.Instance, error) {
	if err := r.fail("instance.create"); err != nil {
		return runtimeapi.Instance{}, err
	}
	return r.ContainerRuntime.CreateInstance(ctx, request)
}
func (r *failureRuntime) StartInstance(ctx context.Context, ref string) error {
	if err := r.fail("instance.start"); err != nil {
		return err
	}
	return r.ContainerRuntime.StartInstance(ctx, ref)
}

func TestProtectedDevboxBaseBlocksDeleteUntilRemoved(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(testImage())
	service, _, _ := newTestServiceWithIDs(t, runtime, newIDs(
		"instance-1", "operation-1", "operation-2", "operation-3", "operation-4",
	), runtime)
	ctx := context.Background()
	created, err := service.Create(ctx, createInput())
	if err != nil {
		t.Fatal(err)
	}
	protected, err := service.SetProtection(ctx, created.OwnerID, created.ID, true)
	if err != nil || !protected.Protected {
		t.Fatalf("protect=%+v err=%v", protected, err)
	}
	err = service.Delete(ctx, created.OwnerID, created.ID, "delete-protected")
	assertDomainCode(t, err, domain.CodeProtectedBase)
	_, err = service.SubmitAction(ctx, created.OwnerID, created.ID, MutationDelete, "submit-delete-protected")
	assertDomainCode(t, err, domain.CodeProtectedBase)
	cleared, err := service.SetProtection(ctx, created.OwnerID, created.ID, false)
	if err != nil || cleared.Protected {
		t.Fatalf("unprotect=%+v err=%v", cleared, err)
	}
	if err := service.Delete(ctx, created.OwnerID, created.ID, "delete-unprotected"); err != nil {
		t.Fatal(err)
	}
}

func TestProtectionIsDevboxOnly(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(testImage())
	service, _, _ := newTestServiceWithIDs(t, runtime, newIDs("instance-1", "operation-1"), runtime)
	ctx := context.Background()
	input := createInput()
	input.Kind = domain.KindVPS
	input.Name = "vps-1"
	created, err := service.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.SetProtection(ctx, created.OwnerID, created.ID, true)
	assertDomainCode(t, err, domain.CodeInvalidArgument)
}

func TestSandboxCreateAppliesKindDefaults(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(runtimeapi.Image{
		Fingerprint:  "sha256:sandbox",
		Aliases:      []string{"openbox:sandbox/ubuntu/24.04"},
		Architecture: "x86_64",
		Type:         "container",
		CloudInit:    true,
	})
	service, _, _ := newTestServiceWithIDs(t, runtime, newIDs("instance-1", "operation-1"), runtime)
	created, err := service.Create(context.Background(), CreateInput{
		OwnerID:        "owner-1",
		Name:           "agent-box",
		Kind:           domain.KindSandbox,
		OwnerPublicKey: "ssh-ed25519 owner",
		IdempotencyKey: "sandbox-create",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Kind != domain.KindSandbox {
		t.Fatalf("kind=%q", created.Kind)
	}
	if created.ImageID != "sha256:sandbox" {
		t.Fatalf("image_id=%q", created.ImageID)
	}
	if created.RequestedIsolation != domain.IsolationBestAvailable {
		t.Fatalf("requested_isolation=%q", created.RequestedIsolation)
	}
	want := domain.Resources{VCPUs: 2, MemoryBytes: 2 << 30, DiskBytes: 10 << 30}
	if created.Resources != want {
		t.Fatalf("resources=%+v want=%+v", created.Resources, want)
	}
	if created.ExpiresAt == nil || !created.ExpiresAt.Equal(created.CreatedAt.Add(domain.DefaultSandboxLifetime)) {
		t.Fatalf("expires_at=%v", created.ExpiresAt)
	}
}

func TestCreateAppliesNetworkPolicyBeforeReportingReady(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(testImage())
	policy := &recordingNetworkPolicy{}
	service, _, _ := newPolicyTestService(t, runtime, policy)

	created, err := service.Create(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	if created.EgressMode != domain.EgressStandard {
		t.Fatalf("egress mode=%q", created.EgressMode)
	}
	if got := policy.calls; len(got) != 1 || got[0] != "apply:"+string(created.ID) {
		t.Fatalf("policy calls=%v", got)
	}
	if policy.observedWhenApplied == domain.ObservedRunning {
		t.Fatalf("policy applied after readiness: observed=%q", policy.observedWhenApplied)
	}
}

func TestGetInstanceIncludesNetworkPolicyStatus(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(testImage())
	policy := &recordingNetworkPolicy{status: domain.NetworkPolicyStatus{
		EgressMode:  domain.EgressStandard,
		ACLs:        []string{"openbox-default-deny", "openbox-egress-standard"},
		DeniedFlows: 2,
		Resolution:  domain.AllowlistResolution{State: "idle", Pending: []string{}, Resolved: []string{}, Failed: []string{}},
	}}
	service, _, _ := newPolicyTestService(t, runtime, policy)
	created, err := service.Create(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}

	inspected, err := service.GetInstance(context.Background(), created.OwnerID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.NetworkPolicy.DeniedFlows != 2 {
		t.Fatalf("denied flows = %d, want 2", inspected.NetworkPolicy.DeniedFlows)
	}
	if got, want := inspected.NetworkPolicy.ACLs, policy.status.ACLs; !reflect.DeepEqual(got, want) {
		t.Fatalf("effective ACLs = %#v, want %#v", got, want)
	}
}

func TestSandboxCreateDefaultsToRestrictedNetworkPolicy(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(runtimeapi.Image{
		Fingerprint:  "sha256:sandbox",
		Aliases:      []string{"openbox:sandbox/ubuntu/24.04"},
		Architecture: "x86_64",
		Type:         "container",
		CloudInit:    true,
	})
	policy := &recordingNetworkPolicy{}
	service, _, store := newPolicyTestService(t, runtime, policy)

	created, err := service.Create(context.Background(), CreateInput{
		OwnerID: "owner-1", Name: "agent-box", Kind: domain.KindSandbox,
		OwnerPublicKey: "ssh-ed25519 owner", IdempotencyKey: "sandbox-policy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.EgressMode != domain.EgressRestricted {
		t.Fatalf("egress mode=%q", created.EgressMode)
	}
	reloaded, err := store.GetInstance(context.Background(), created.OwnerID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.EgressMode != domain.EgressRestricted {
		t.Fatalf("persisted egress mode=%q", reloaded.EgressMode)
	}
}

func TestCreateNetworkPolicyFailurePreventsReady(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(testImage())
	policy := &recordingNetworkPolicy{applyErr: errors.New("apply policy")}
	service, _, store := newPolicyTestService(t, runtime, policy)

	_, err := service.Create(context.Background(), createInput())
	if !errors.Is(err, policy.applyErr) {
		t.Fatalf("create error=%v", err)
	}
	stored, err := store.GetInstance(context.Background(), "owner-1", "instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.ObservedState == domain.ObservedRunning {
		t.Fatal("policy failure reported the instance ready")
	}
}

func TestPolicyVerificationFailurePreventsReadyAndMarksDriftOnInspect(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(testImage())
	policy := &recordingNetworkPolicy{verifyErr: errors.New("ACL mismatch")}
	service, fakeRuntime, store := newPolicyTestService(t, runtime, policy)

	if _, err := service.Create(context.Background(), createInput()); !errors.Is(err, policy.verifyErr) {
		t.Fatalf("create error=%v", err)
	}
	stored, err := store.GetInstance(context.Background(), "owner-1", "instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.ObservedState != domain.ObservedError {
		t.Fatalf("create policy drift left instance ready: %+v", stored)
	}

	policy.verifyErr = nil
	created, err := service.Create(context.Background(), CreateInput{
		OwnerID: "owner-1", Name: "second", Kind: domain.KindDevbox, Image: "ubuntu",
		RequestedIsolation: domain.IsolationStandard,
		Resources:          domain.Resources{VCPUs: 2, MemoryBytes: 1024, DiskBytes: 2048},
		OwnerPublicKey:     "ssh-ed25519 owner", IdempotencyKey: "second-create",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Stop(context.Background(), created.OwnerID, created.ID, "prepare-policy-drift"); err != nil {
		t.Fatal(err)
	}
	if err := fakeRuntime.StartInstance(context.Background(), created.RuntimeRef); err != nil {
		t.Fatal(err)
	}
	policy.verifyErr = errors.New("NIC ACLs drifted")
	policy.verify = func(instance domain.Instance) {
		stored, err := store.GetInstance(context.Background(), created.OwnerID, created.ID)
		if err != nil {
			t.Errorf("read instance while verifying policy: %v", err)
			return
		}
		if stored.ObservedState == domain.ObservedRunning {
			t.Error("policy verification ran after instance was persisted running")
		}
	}
	if _, err := service.Refresh(context.Background(), created.OwnerID, created.ID); !errors.Is(err, policy.verifyErr) {
		t.Fatalf("refresh error=%v", err)
	}
	stored, err = store.GetInstance(context.Background(), created.OwnerID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ObservedState != domain.ObservedError {
		t.Fatalf("inspect policy drift left instance ready: %+v", stored)
	}
}

func TestRefreshReturnsPersistenceFailureWhenRecordingPolicyDrift(t *testing.T) {
	service, runtime, store := newTestService(t, nil)
	created, err := service.Create(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	driftErr := errors.New("NIC ACLs drifted")
	writeErr := errors.New("database unavailable")
	policy := &recordingNetworkPolicy{verifyErr: driftErr}
	failingService, err := New(runtime, failingObservationRepository{Repository: store, err: writeErr}, Options{
		Now:           func() time.Time { return time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC) },
		NewID:         newIDs("unused"),
		NetworkPolicy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = failingService.Refresh(context.Background(), created.OwnerID, created.ID)
	if !errors.Is(err, driftErr) || !errors.Is(err, writeErr) {
		t.Fatalf("refresh error=%v, want policy and persistence failures", err)
	}
}

func TestRecoverCreateAppliesNetworkPolicyBeforeReportingReady(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(testImage())
	policy := &recordingNetworkPolicy{}
	service, _, _ := newPolicyTestService(t, runtime, policy)

	instance, operation, err := service.SubmitCreate(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RecoverOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if got := policy.calls; len(got) != 1 || got[0] != "apply:"+string(instance.ID) {
		t.Fatalf("policy calls=%v", got)
	}
	if policy.observedWhenApplied == domain.ObservedRunning {
		t.Fatalf("policy applied after readiness: observed=%q", policy.observedWhenApplied)
	}
}

func TestDeleteRemovesNetworkPolicyOnlyAfterRuntimeDisappears(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(testImage())
	policy := &recordingNetworkPolicy{runtime: runtime}
	service, _, _ := newPolicyTestService(t, runtime, policy)
	created, err := service.Create(context.Background(), createInput())
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Delete(context.Background(), created.OwnerID, created.ID, "delete-policy"); err != nil {
		t.Fatal(err)
	}
	if got := policy.calls; len(got) != 2 || got[1] != "remove:"+string(created.ID) {
		t.Fatalf("policy calls=%v", got)
	}
	if !policy.runtimeGoneWhenRemoved {
		t.Fatal("policy was removed before runtime deletion completed")
	}
}

func TestMarkExpiredSetsDesiredDeletedWithoutHidingRuntime(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(runtimeapi.Image{
		Fingerprint:  "sha256:sandbox",
		Aliases:      []string{"openbox:sandbox/ubuntu/24.04"},
		Architecture: "x86_64",
		Type:         "container",
		CloudInit:    true,
	})
	service, fakeRuntime, store := newTestServiceWithIDs(t, runtime, newIDs("instance-1", "operation-1", "operation-2", "operation-3"), runtime)
	ctx := context.Background()
	created, err := service.Create(ctx, CreateInput{
		OwnerID: "owner-1", Name: "agent-box", Kind: domain.KindSandbox,
		OwnerPublicKey: "ssh-ed25519 owner", IdempotencyKey: "sandbox-create",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.MarkExpired(ctx, created.OwnerID, created.ID); err != nil {
		t.Fatal(err)
	}
	marked, err := service.GetInstance(ctx, created.OwnerID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if marked.DesiredState != domain.DesiredDeleted || marked.ObservedState != domain.ObservedDeleting {
		t.Fatalf("marked=%+v", marked)
	}
	if _, err := fakeRuntime.InspectInstance(ctx, created.RuntimeRef); err != nil {
		t.Fatalf("runtime must still exist while observed deleting: %v", err)
	}
	tombstoned, err := store.IsInstanceTombstoned(ctx, created.OwnerID, created.ID)
	if err != nil || tombstoned {
		t.Fatalf("must not tombstone before runtime removal: tombstoned=%v err=%v", tombstoned, err)
	}
	if err := service.MarkExpired(ctx, created.OwnerID, created.ID); err != nil {
		t.Fatalf("idempotent remark: %v", err)
	}
	if err := service.Delete(ctx, created.OwnerID, created.ID, "expiry-cleanup"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetInstance(ctx, created.OwnerID, created.ID); err == nil {
		t.Fatal("expected not found after cleanup")
	}
	if _, err := fakeRuntime.InspectInstance(ctx, created.RuntimeRef); !errors.Is(err, runtimeapi.ErrNotFound) {
		t.Fatalf("runtime err=%v", err)
	}
}

func TestExtendExpiryPersistsAndRejectsAfterDeleteStarts(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(runtimeapi.Image{
		Fingerprint:  "sha256:sandbox",
		Aliases:      []string{"openbox:sandbox/ubuntu/24.04"},
		Architecture: "x86_64",
		Type:         "container",
		CloudInit:    true,
	})
	service, _, _ := newTestServiceWithIDs(t, runtime, newIDs("instance-1", "operation-1", "operation-2"), runtime)
	ctx := context.Background()
	created, err := service.Create(ctx, CreateInput{
		OwnerID: "owner-1", Name: "agent-box", Kind: domain.KindSandbox,
		OwnerPublicKey: "ssh-ed25519 owner", IdempotencyKey: "sandbox-create",
	})
	if err != nil {
		t.Fatal(err)
	}
	extended, err := service.ExtendExpiry(ctx, created.OwnerID, created.ID, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	want := created.ExpiresAt.Add(30 * time.Minute)
	if extended.ExpiresAt == nil || !extended.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at=%v want=%v", extended.ExpiresAt, want)
	}
	if err := service.MarkExpired(ctx, created.OwnerID, created.ID); err != nil {
		t.Fatal(err)
	}
	_, err = service.ExtendExpiry(ctx, created.OwnerID, created.ID, time.Minute)
	assertDomainCode(t, err, domain.CodeInvalidTransition)
}

func assertDomainCode(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != code {
		t.Fatalf("err=%v want code=%s", err, code)
	}
}
