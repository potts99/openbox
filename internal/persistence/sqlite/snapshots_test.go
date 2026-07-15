// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

func TestSnapshotRecordCRUDAndIdempotency(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	createOwner(t, store, now)
	instance := createManagedInstance(t, store, "instance-1", "dev", "incus-ref-1", now)

	snapshot := domain.Snapshot{
		ID: "snap-1", OwnerID: "owner-1", InstanceID: instance.ID, Name: "ready", RuntimeRef: "ready", CreatedAt: now,
	}
	op := domain.Operation{
		ID: "op-snap-1", OwnerID: "owner-1", Type: "snapshot.create", TargetType: "snapshot", TargetID: "snap-1",
		Status: domain.OperationPending, Stage: "runtime", IdempotencyKey: "snap-create-1", RequestHash: "hash-snap-1",
		CreatedAt: now, UpdatedAt: now,
	}
	got, replay, err := store.CreateSnapshotRecord(ctx, snapshot, op)
	if err != nil || replay || got.ID != op.ID {
		t.Fatalf("create=%+v replay=%v err=%v", got, replay, err)
	}
	again, replay, err := store.CreateSnapshotRecord(ctx, snapshot, op)
	if err != nil || !replay || again.ID != op.ID {
		t.Fatalf("replay=%+v replay=%v err=%v", again, replay, err)
	}

	loaded, err := store.GetSnapshot(ctx, "owner-1", "snap-1")
	if err != nil || loaded.Name != "ready" || loaded.RuntimeRef != "ready" {
		t.Fatalf("get=%+v err=%v", loaded, err)
	}
	listed, err := store.ListSnapshots(ctx, "owner-1", instance.ID)
	if err != nil || len(listed) != 1 || listed[0].ID != "snap-1" {
		t.Fatalf("list=%+v err=%v", listed, err)
	}
	if err := store.UpdateSnapshotRuntimeRef(ctx, "owner-1", "snap-1", "ready", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	delOp := domain.Operation{
		ID: "op-snap-del-1", OwnerID: "owner-1", Type: "snapshot.delete", TargetType: "snapshot", TargetID: "snap-1",
		Status: domain.OperationPending, Stage: "runtime", IdempotencyKey: "snap-del-1", RequestHash: "hash-del-1",
		CreatedAt: now, UpdatedAt: now,
	}
	if _, _, err := store.CreateDeleteOperation(ctx, delOp); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSnapshotRecord(ctx, "owner-1", "snap-1"); err != nil {
		t.Fatal(err)
	}
	_, err = store.GetSnapshot(ctx, "owner-1", "snap-1")
	assertCode(t, err, domain.CodeNotFound)
}

func TestCreateSnapshotRecordRejectsDuplicateNames(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	createOwner(t, store, now)
	instance := createManagedInstance(t, store, "instance-1", "dev", "incus-ref-1", now)
	first := domain.Snapshot{ID: "snap-1", OwnerID: "owner-1", InstanceID: instance.ID, Name: "ready", RuntimeRef: "ready", CreatedAt: now}
	firstOp := domain.Operation{
		ID: "op-1", OwnerID: "owner-1", Type: "snapshot.create", TargetType: "snapshot", TargetID: "snap-1",
		Status: domain.OperationPending, Stage: "runtime", IdempotencyKey: "k1", RequestHash: "h1",
		CreatedAt: now, UpdatedAt: now,
	}
	if _, _, err := store.CreateSnapshotRecord(ctx, first, firstOp); err != nil {
		t.Fatal(err)
	}
	second := domain.Snapshot{ID: "snap-2", OwnerID: "owner-1", InstanceID: instance.ID, Name: "ready", RuntimeRef: "ready", CreatedAt: now}
	secondOp := domain.Operation{
		ID: "op-2", OwnerID: "owner-1", Type: "snapshot.create", TargetType: "snapshot", TargetID: "snap-2",
		Status: domain.OperationPending, Stage: "runtime", IdempotencyKey: "k2", RequestHash: "h2",
		CreatedAt: now, UpdatedAt: now,
	}
	_, _, err := store.CreateSnapshotRecord(ctx, second, secondOp)
	assertCode(t, err, domain.CodeConflict)
}
