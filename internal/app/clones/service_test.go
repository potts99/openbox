// SPDX-License-Identifier: AGPL-3.0-only

package clones_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/clones"
	"github.com/openbox-dev/openbox/internal/domain"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
)

func TestSubmitCopyRecordsProvenanceAndCreatesIndependentRuntime(t *testing.T) {
	t.Parallel()
	svc, runtime, repo := newTestService(t)
	source := seedRunning(t, repo, runtime, "inst-source", "base", "sha256:ubuntu")

	clone, op, err := svc.SubmitCopy(context.Background(), clones.CopyInput{
		OwnerID: source.OwnerID, Source: "base", Destination: "feature", IdempotencyKey: "cp-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.Type != "instance.copy" || clone.Name != "feature" || clone.ID == source.ID {
		t.Fatalf("clone=%+v op=%+v", clone, op)
	}
	if clone.CloneSourceInstanceID != source.ID || clone.CloneSourceImageID != source.ImageID {
		t.Fatalf("provenance missing: %+v", clone)
	}
	if err := svc.RecoverOperation(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	reloaded, err := repo.GetInstance(context.Background(), clone.OwnerID, clone.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.RuntimeRef == "" || reloaded.RuntimeRef == source.RuntimeRef {
		t.Fatalf("clone runtime ref not independent: %q source=%q", reloaded.RuntimeRef, source.RuntimeRef)
	}
	if _, err := runtime.InspectInstance(context.Background(), reloaded.RuntimeRef); err != nil {
		t.Fatal(err)
	}
	if reloaded.CloneSourceInstanceID != source.ID || reloaded.CloneSourceImageID != "sha256:ubuntu" {
		t.Fatalf("persisted provenance=%+v", reloaded)
	}
}

func TestSubmitCopyResolvesSourceByID(t *testing.T) {
	t.Parallel()
	svc, runtime, repo := newTestService(t)
	source := seedRunning(t, repo, runtime, "inst-source", "base", "sha256:ubuntu")
	clone, _, err := svc.SubmitCopy(context.Background(), clones.CopyInput{
		OwnerID: source.OwnerID, Source: string(source.ID), Destination: "copy-by-id", IdempotencyKey: "cp-2",
	})
	if err != nil || clone.Name != "copy-by-id" {
		t.Fatalf("clone=%+v err=%v", clone, err)
	}
}

type memoryRepo struct {
	instances map[domain.InstanceID]domain.Instance
	ops       map[string]domain.Operation
}

func newTestService(t *testing.T) (*clones.Service, *fake.Runtime, *memoryRepo) {
	t.Helper()
	runtime := fake.New(runtimeapi.Capabilities{Architecture: "x86_64", Containers: true})
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

func seedRunning(t *testing.T, repo *memoryRepo, runtime *fake.Runtime, id, name, image string) domain.Instance {
	t.Helper()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	instance := domain.Instance{
		ID: domain.InstanceID(id), OwnerID: "owner-1", Name: name, Kind: domain.KindDevbox,
		ImageID: domain.ImageID(image), RequestedIsolation: domain.IsolationStandard, ActualIsolation: domain.IsolationContainer,
		DesiredState: domain.DesiredRunning, ObservedState: domain.ObservedRunning,
		RuntimeRef: "openbox-" + id, CreatedAt: now, UpdatedAt: now,
	}
	repo.instances[instance.ID] = instance
	runtime.AddImage(runtimeapi.Image{Fingerprint: image, Aliases: []string{"ubuntu"}, Architecture: "x86_64", Type: "container", CloudInit: true})
	if _, err := runtime.CreateInstance(context.Background(), runtimeapi.CreateRequest{
		Ref: instance.RuntimeRef, Image: image, Unprivileged: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.StartInstance(context.Background(), instance.RuntimeRef); err != nil {
		t.Fatal(err)
	}
	return instance
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
