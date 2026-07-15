// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

func TestOwnerScopedAPIQueriesDoNotLeakResources(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	for _, owner := range []domain.Owner{{ID: "owner-1", Name: "One"}, {ID: "owner-2", Name: "Two"}} {
		owner.CreatedAt, owner.UpdatedAt = now, now
		if err := store.CreateOwner(ctx, owner); err != nil {
			t.Fatal(err)
		}
	}
	for index, ownerID := range []domain.OwnerID{"owner-1", "owner-2"} {
		suffix := string(rune('1' + index))
		image := domain.Image{ID: domain.ImageID("image-" + suffix), OwnerID: ownerID, Alias: "ubuntu-" + suffix, Source: "test", Digest: "sha256:" + suffix, Architecture: "x86_64", Compatibility: "container", CreatedAt: now, UpdatedAt: now}
		if err := store.EnsureImage(ctx, image); err != nil {
			t.Fatal(err)
		}
		instance := domain.Instance{ID: domain.InstanceID("instance-" + suffix), OwnerID: ownerID, Name: "box-" + suffix, Kind: domain.KindVPS, ImageID: image.ID, RequestedIsolation: domain.IsolationStandard, ActualIsolation: domain.IsolationContainer, DesiredState: domain.DesiredRunning, ObservedState: domain.ObservedPending, RuntimeRef: "runtime-" + suffix, CreatedAt: now, UpdatedAt: now}
		operation := domain.Operation{ID: domain.OperationID("operation-" + suffix), OwnerID: ownerID, Type: "instance.create", TargetType: "instance", TargetID: string(instance.ID), Status: domain.OperationPending, Stage: "runtime", IdempotencyKey: "key-" + suffix, RequestHash: "hash-" + suffix, CreatedAt: now, UpdatedAt: now}
		if _, _, err := store.CreateInstance(ctx, instance, operation); err != nil {
			t.Fatal(err)
		}
	}
	operations, err := store.ListOperations(ctx, "owner-1", 100)
	if err != nil || len(operations) != 1 || operations[0].OwnerID != "owner-1" {
		t.Fatalf("operations=%+v err=%v", operations, err)
	}
	_, err = store.GetOperation(ctx, "owner-1", "operation-2")
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeNotFound {
		t.Fatalf("cross-owner operation lookup: %v", err)
	}
	if _, err := store.ListOperationEventsAfter(ctx, "owner-1", "operation-2", 0, 100); !errors.As(err, &domainErr) || domainErr.Code != domain.CodeNotFound {
		t.Fatalf("cross-owner event lookup: %v", err)
	}
}
