// SPDX-License-Identifier: AGPL-3.0-only

package snapshots_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
	"github.com/openbox-dev/openbox/internal/snapshots"
)

func TestCreateListInspectDeleteSnapshot(t *testing.T) {
	t.Parallel()
	svc, runtime, repo := newTestService(t)
	instance := seedRunningInstance(t, repo, runtime, "inst-1", "dev")

	created, op, err := svc.Create(context.Background(), snapshots.CreateInput{
		OwnerID: instance.OwnerID, InstanceID: instance.ID, Name: "ready", IdempotencyKey: "snap-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.Type != "snapshot.create" || created.Name != "ready" {
		t.Fatalf("created=%+v op=%+v", created, op)
	}
	if err := svc.RecoverOperation(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(context.Background(), instance.OwnerID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RuntimeRef == "" || got.Name != "ready" {
		t.Fatalf("got=%+v", got)
	}
	listed, err := svc.List(context.Background(), instance.OwnerID, instance.ID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	delOp, err := svc.Delete(context.Background(), instance.OwnerID, created.ID, "snap-del-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RecoverOperation(context.Background(), delOp); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(context.Background(), instance.OwnerID, created.ID); err == nil {
		t.Fatal("snapshot still present after delete")
	}
}

func TestRestoreAsNewCreatesIndependentInstance(t *testing.T) {
	t.Parallel()
	svc, runtime, repo := newTestService(t)
	instance := seedRunningInstance(t, repo, runtime, "inst-1", "base")
	created, op, err := svc.Create(context.Background(), snapshots.CreateInput{
		OwnerID: instance.OwnerID, InstanceID: instance.ID, Name: "ready", IdempotencyKey: "snap-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RecoverOperation(context.Background(), op); err != nil {
		t.Fatal(err)
	}

	clone, restoreOp, err := svc.RestoreAsNew(context.Background(), snapshots.RestoreInput{
		OwnerID: instance.OwnerID, SnapshotID: created.ID, Name: "feature", IdempotencyKey: "restore-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RecoverOperation(context.Background(), restoreOp); err != nil {
		t.Fatal(err)
	}
	if clone.Name != "feature" || clone.ID == instance.ID {
		t.Fatalf("clone=%+v", clone)
	}
	reloaded, err := repo.GetInstance(context.Background(), clone.OwnerID, clone.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.RuntimeRef == "" || reloaded.RuntimeRef == instance.RuntimeRef {
		t.Fatalf("clone runtime ref not independent: %q source=%q", reloaded.RuntimeRef, instance.RuntimeRef)
	}
	if _, err := runtime.InspectInstance(context.Background(), reloaded.RuntimeRef); err != nil {
		t.Fatal(err)
	}
}

type memoryRepo struct {
	instances map[domain.InstanceID]domain.Instance
	snapshots map[domain.SnapshotID]domain.Snapshot
	ops       map[string]domain.Operation
}

func newTestService(t *testing.T) (*snapshots.Service, *fake.Runtime, *memoryRepo) {
	t.Helper()
	runtime := fake.New(runtimeapi.Capabilities{Architecture: "x86_64", Containers: true})
	repo := &memoryRepo{
		instances: map[domain.InstanceID]domain.Instance{},
		snapshots: map[domain.SnapshotID]domain.Snapshot{},
		ops:       map[string]domain.Operation{},
	}
	n := 0
	svc, err := snapshots.New(runtime, repo, snapshots.Options{
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

func seedRunningInstance(t *testing.T, repo *memoryRepo, runtime *fake.Runtime, id, name string) domain.Instance {
	t.Helper()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	instance := domain.Instance{
		ID: domain.InstanceID(id), OwnerID: "owner-1", Name: name, Kind: domain.KindDevbox,
		ImageID: "sha256:ubuntu", RequestedIsolation: domain.IsolationStandard, ActualIsolation: domain.IsolationContainer,
		DesiredState: domain.DesiredRunning, ObservedState: domain.ObservedRunning,
		RuntimeRef: "openbox-" + id, CreatedAt: now, UpdatedAt: now,
	}
	repo.instances[instance.ID] = instance
	runtime.AddImage(runtimeapi.Image{Fingerprint: "sha256:ubuntu", Aliases: []string{"ubuntu"}, Architecture: "x86_64", Type: "container", CloudInit: true})
	if _, err := runtime.CreateInstance(context.Background(), runtimeapi.CreateRequest{
		Ref: instance.RuntimeRef, Image: "sha256:ubuntu", Unprivileged: true,
		Metadata: map[string]string{"user.openbox.managed": "true", "user.openbox.resource": "instance", "user.openbox.instance_id": id, "user.openbox.owner_id": "owner-1"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.StartInstance(context.Background(), instance.RuntimeRef); err != nil {
		t.Fatal(err)
	}
	return instance
}

func (m *memoryRepo) GetInstance(_ context.Context, owner domain.OwnerID, id domain.InstanceID) (domain.Instance, error) {
	instance, ok := m.instances[id]
	if !ok || instance.OwnerID != owner {
		return domain.Instance{}, &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	}
	return instance, nil
}
func (m *memoryRepo) CreateInstance(_ context.Context, instance domain.Instance, operation domain.Operation) (domain.Operation, bool, error) {
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
func (m *memoryRepo) CreateSnapshotRecord(_ context.Context, snapshot domain.Snapshot, operation domain.Operation) (domain.Operation, bool, error) {
	for _, existing := range m.snapshots {
		if existing.InstanceID == snapshot.InstanceID && existing.Name == snapshot.Name {
			return domain.Operation{}, false, &domain.Error{Code: domain.CodeConflict, Field: "snapshot.name"}
		}
	}
	if existing, ok := m.ops[string(operation.ID)]; ok {
		return existing, true, nil
	}
	for _, op := range m.ops {
		if op.OwnerID == operation.OwnerID && op.IdempotencyKey == operation.IdempotencyKey {
			return op, true, nil
		}
	}
	m.snapshots[snapshot.ID] = snapshot
	m.ops[string(operation.ID)] = operation
	return operation, false, nil
}
func (m *memoryRepo) GetSnapshot(_ context.Context, owner domain.OwnerID, id domain.SnapshotID) (domain.Snapshot, error) {
	snapshot, ok := m.snapshots[id]
	if !ok || snapshot.OwnerID != owner {
		return domain.Snapshot{}, &domain.Error{Code: domain.CodeNotFound, Field: "snapshot"}
	}
	return snapshot, nil
}
func (m *memoryRepo) ListSnapshots(_ context.Context, owner domain.OwnerID, instanceID domain.InstanceID) ([]domain.Snapshot, error) {
	out := make([]domain.Snapshot, 0)
	for _, snapshot := range m.snapshots {
		if snapshot.OwnerID == owner && snapshot.InstanceID == instanceID {
			out = append(out, snapshot)
		}
	}
	return out, nil
}
func (m *memoryRepo) DeleteSnapshotRecord(_ context.Context, owner domain.OwnerID, id domain.SnapshotID) error {
	snapshot, ok := m.snapshots[id]
	if !ok || snapshot.OwnerID != owner {
		return &domain.Error{Code: domain.CodeNotFound, Field: "snapshot"}
	}
	delete(m.snapshots, id)
	return nil
}
func (m *memoryRepo) UpdateSnapshotRuntimeRef(_ context.Context, owner domain.OwnerID, id domain.SnapshotID, runtimeRef string, at time.Time) error {
	snapshot, ok := m.snapshots[id]
	if !ok || snapshot.OwnerID != owner {
		return &domain.Error{Code: domain.CodeNotFound, Field: "snapshot"}
	}
	snapshot.RuntimeRef = runtimeRef
	m.snapshots[id] = snapshot
	_ = at
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
func (m *memoryRepo) CreateDeleteOperation(_ context.Context, operation domain.Operation) (domain.Operation, bool, error) {
	for _, op := range m.ops {
		if op.OwnerID == operation.OwnerID && op.IdempotencyKey == operation.IdempotencyKey {
			return op, true, nil
		}
	}
	m.ops[string(operation.ID)] = operation
	return operation, false, nil
}
