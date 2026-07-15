// SPDX-License-Identifier: AGPL-3.0-only

package clones_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/clones"
	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/domain"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
)

func TestSubmitCopyRecordsProvenanceAndVerifiesIdentity(t *testing.T) {
	t.Parallel()
	svc, runtime, repo := newTestService(t, runtimeapi.Capabilities{Architecture: "x86_64", Containers: true, StorageDrivers: []string{"zfs"}})
	source := seedRunning(t, repo, runtime, "inst-source", "base", "sha256:ubuntu", true)

	result, err := svc.SubmitCopy(context.Background(), clones.CopyInput{
		OwnerID: source.OwnerID, Source: "base", Destination: "feature", IdempotencyKey: "cp-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation.Type != "instance.copy" || result.Instance.Name != "feature" || result.Instance.ID == source.ID {
		t.Fatalf("result=%+v", result)
	}
	if result.Instance.CloneSourceInstanceID != source.ID || result.Instance.CloneSourceImageID != source.ImageID {
		t.Fatalf("provenance missing: %+v", result.Instance)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("unexpected warnings for protected zfs source: %v", result.Warnings)
	}
	if err := svc.RecoverOperation(context.Background(), result.Operation); err != nil {
		t.Fatal(err)
	}
	reloaded, err := repo.GetInstance(context.Background(), result.Instance.OwnerID, result.Instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.RuntimeRef == "" || reloaded.RuntimeRef == source.RuntimeRef {
		t.Fatalf("clone runtime ref not independent: %q source=%q", reloaded.RuntimeRef, source.RuntimeRef)
	}
	copied, err := runtime.InspectInstance(context.Background(), reloaded.RuntimeRef)
	if err != nil {
		t.Fatal(err)
	}
	if err := instances.VerifyRuntimeIdentity(copied, reloaded.ID, reloaded.ActualIsolation); err != nil {
		t.Fatal(err)
	}
	if reloaded.ObservedState != domain.ObservedRunning {
		t.Fatalf("observed=%s", reloaded.ObservedState)
	}
}

func TestSubmitCopyWarnsForFullCopyAndPersonalDevboxSecrets(t *testing.T) {
	t.Parallel()
	svc, runtime, repo := newTestService(t, runtimeapi.Capabilities{Architecture: "x86_64", Containers: true, StorageDrivers: []string{"dir"}})
	source := seedRunning(t, repo, runtime, "inst-source", "personal", "sha256:ubuntu", false)

	result, err := svc.SubmitCopy(context.Background(), clones.CopyInput{
		OwnerID: source.OwnerID, Source: "personal", Destination: "scratch", IdempotencyKey: "cp-warn",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsWarning(result.Warnings, clones.WarningFullCopy) || !containsWarning(result.Warnings, clones.WarningSecrets) {
		t.Fatalf("warnings=%v", result.Warnings)
	}
}

func TestDeletingSourceDoesNotInvalidateCompletedClone(t *testing.T) {
	t.Parallel()
	svc, runtime, repo := newTestService(t, runtimeapi.Capabilities{Architecture: "x86_64", Containers: true, StorageDrivers: []string{"zfs"}})
	source := seedRunning(t, repo, runtime, "inst-source", "base", "sha256:ubuntu", true)
	if err := runtime.CreateSnapshot(context.Background(), source.RuntimeRef, "ready"); err != nil {
		t.Fatal(err)
	}
	result, err := svc.SubmitCopy(context.Background(), clones.CopyInput{
		OwnerID: source.OwnerID, Source: "base", Destination: "feature", IdempotencyKey: "cp-indep",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RecoverOperation(context.Background(), result.Operation); err != nil {
		t.Fatal(err)
	}
	if err := runtime.DeleteSnapshot(context.Background(), source.RuntimeRef, "ready"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.DeleteInstance(context.Background(), source.RuntimeRef); err != nil {
		t.Fatal(err)
	}
	delete(repo.instances, source.ID)

	clone, err := repo.GetInstance(context.Background(), result.Instance.OwnerID, result.Instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.InspectInstance(context.Background(), clone.RuntimeRef); err != nil {
		t.Fatalf("clone invalidated after source/snapshot delete: %v", err)
	}
	if err := runtime.StopInstance(context.Background(), clone.RuntimeRef); err != nil {
		t.Fatal(err)
	}
	if err := runtime.StartInstance(context.Background(), clone.RuntimeRef); err != nil {
		t.Fatal(err)
	}
	if clone.CloneSourceInstanceID != source.ID {
		t.Fatalf("provenance lost: %+v", clone)
	}
}

func TestRecoverCopyRejectsWrongRuntimeIdentity(t *testing.T) {
	t.Parallel()
	svc, runtime, repo := newTestService(t, runtimeapi.Capabilities{Architecture: "x86_64", Containers: true, StorageDrivers: []string{"zfs"}})
	source := seedRunning(t, repo, runtime, "inst-source", "base", "sha256:ubuntu", true)
	result, err := svc.SubmitCopy(context.Background(), clones.CopyInput{
		OwnerID: source.OwnerID, Source: "base", Destination: "feature", IdempotencyKey: "cp-bad-id",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pre-create target with source identity metadata so verification must fail.
	if _, err := runtime.CopyInstance(context.Background(), runtimeapi.CopyRequest{
		SourceRef: source.RuntimeRef, TargetRef: result.Instance.RuntimeRef,
		Metadata: map[string]string{
			instances.MetadataManaged: "true", instances.MetadataResource: "instance",
			instances.MetadataInstanceID: string(source.ID), instances.MetadataOwnerID: string(source.OwnerID),
		},
	}); err != nil {
		t.Fatal(err)
	}
	err = svc.RecoverOperation(context.Background(), result.Operation)
	var identity *instances.IdentityConflictError
	if !errors.As(err, &identity) {
		t.Fatalf("err=%v", err)
	}
}

func TestStorageEfficientCopyDrivers(t *testing.T) {
	t.Parallel()
	if clones.StorageEfficientCopy([]string{"dir"}) {
		t.Fatal("dir must not claim CoW")
	}
	if !clones.StorageEfficientCopy([]string{"dir", "zfs"}) {
		t.Fatal("zfs should claim CoW")
	}
}

type memoryRepo struct {
	instances map[domain.InstanceID]domain.Instance
	ops       map[string]domain.Operation
}

func newTestService(t *testing.T, capabilities runtimeapi.Capabilities) (*clones.Service, *fake.Runtime, *memoryRepo) {
	t.Helper()
	runtime := fake.New(capabilities)
	repo := &memoryRepo{instances: map[domain.InstanceID]domain.Instance{}, ops: map[string]domain.Operation{}}
	n := 0
	svc, err := clones.New(runtime, repo, clones.Options{
		Now: func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) },
		NewID: func() string {
			n++
			return fmt.Sprintf("gen-%d", n)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, runtime, repo
}

func seedRunning(t *testing.T, repo *memoryRepo, runtime *fake.Runtime, id, name, image string, protected bool) domain.Instance {
	t.Helper()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	instance := domain.Instance{
		ID: domain.InstanceID(id), OwnerID: "owner-1", Name: name, Kind: domain.KindVPS,
		ImageID: domain.ImageID(image), RequestedIsolation: domain.IsolationStandard, ActualIsolation: domain.IsolationContainer,
		DesiredState: domain.DesiredRunning, ObservedState: domain.ObservedRunning, Protected: protected,
		RuntimeRef: "openbox-" + id, CreatedAt: now, UpdatedAt: now,
	}
	repo.instances[instance.ID] = instance
	runtime.AddImage(runtimeapi.Image{Fingerprint: image, Aliases: []string{"ubuntu"}, Architecture: "x86_64", Type: "container", CloudInit: true})
	if _, err := runtime.CreateInstance(context.Background(), runtimeapi.CreateRequest{
		Ref: instance.RuntimeRef, Image: image, Unprivileged: true,
		Metadata: map[string]string{
			instances.MetadataManaged: "true", instances.MetadataResource: "instance",
			instances.MetadataInstanceID: string(instance.ID), instances.MetadataOwnerID: string(instance.OwnerID),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.StartInstance(context.Background(), instance.RuntimeRef); err != nil {
		t.Fatal(err)
	}
	return instance
}

func containsWarning(warnings []string, wanted string) bool {
	for _, warning := range warnings {
		if warning == wanted {
			return true
		}
	}
	return false
}

func (m *memoryRepo) ListInstancesByOwner(_ context.Context, owner domain.OwnerID, _ int) ([]domain.Instance, error) {
	out := make([]domain.Instance, 0)
	for _, instance := range m.instances {
		if instance.OwnerID == owner && instance.DeletedAt == nil {
			out = append(out, instance)
		}
	}
	return out, nil
}
func (m *memoryRepo) GetInstance(_ context.Context, owner domain.OwnerID, id domain.InstanceID) (domain.Instance, error) {
	instance, ok := m.instances[id]
	if !ok || instance.OwnerID != owner {
		return domain.Instance{}, &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	}
	return instance, nil
}
func (m *memoryRepo) CreateInstance(_ context.Context, instance domain.Instance, operation domain.Operation) (domain.Operation, bool, error) {
	for _, existing := range m.instances {
		if existing.OwnerID == instance.OwnerID && existing.Name == instance.Name && existing.DeletedAt == nil {
			return domain.Operation{}, false, &domain.Error{Code: domain.CodeConflict, Field: "name"}
		}
	}
	for _, op := range m.ops {
		if op.OwnerID == operation.OwnerID && op.IdempotencyKey == operation.IdempotencyKey {
			return op, true, nil
		}
	}
	m.instances[instance.ID] = instance
	m.ops[string(operation.ID)] = operation
	return operation, false, nil
}
func (m *memoryRepo) UpdateInstanceObservation(_ context.Context, owner domain.OwnerID, id domain.InstanceID, runtimeRef string, actual domain.IsolationType, observed domain.ObservedState, code domain.ErrorCode, at time.Time) error {
	instance, ok := m.instances[id]
	if !ok || instance.OwnerID != owner {
		return &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	}
	instance.RuntimeRef, instance.ActualIsolation, instance.ObservedState, instance.ErrorCode, instance.UpdatedAt = runtimeRef, actual, observed, code, at
	m.instances[id] = instance
	return nil
}
func (m *memoryRepo) GetOperationByIdempotency(_ context.Context, owner domain.OwnerID, key string) (domain.Operation, bool, error) {
	for _, op := range m.ops {
		if op.OwnerID == owner && op.IdempotencyKey == key {
			return op, true, nil
		}
	}
	return domain.Operation{}, false, nil
}
func (m *memoryRepo) CompleteOperation(_ context.Context, owner domain.OwnerID, id domain.OperationID, at time.Time) error {
	op, ok := m.ops[string(id)]
	if !ok || op.OwnerID != owner {
		return &domain.Error{Code: domain.CodeNotFound, Field: "operation"}
	}
	op.Status = domain.OperationSucceeded
	op.UpdatedAt = at
	m.ops[string(id)] = op
	return nil
}
func (m *memoryRepo) UpdateOperationStage(_ context.Context, owner domain.OwnerID, id domain.OperationID, stage string, progress int, at time.Time) error {
	op, ok := m.ops[string(id)]
	if !ok || op.OwnerID != owner {
		return &domain.Error{Code: domain.CodeNotFound, Field: "operation"}
	}
	op.Stage, op.Progress, op.UpdatedAt = stage, progress, at
	m.ops[string(id)] = op
	return nil
}
