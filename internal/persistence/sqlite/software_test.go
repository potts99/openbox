// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

func TestInstanceSoftwareUpsertAndList(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	createOwner(t, store, now)
	i, err := domain.NewInstance("instance-1", "owner-1", "project", domain.KindVPS, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateInstance(ctx, i, operation("op-1", "key-1", "hash-1", now)); err != nil {
		t.Fatal(err)
	}

	row := domain.InstanceSoftware{
		InstanceID: "instance-1",
		OwnerID:    "owner-1",
		PackageID:  "pi",
		Status:     domain.SoftwareInstalled,
		Version:    "0.80.7",
		UpdatedAt:  now,
	}
	if err := store.UpsertInstanceSoftware(ctx, row); err != nil {
		t.Fatal(err)
	}
	list, err := store.ListInstanceSoftware(ctx, "owner-1", "instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].PackageID != "pi" || list[0].Status != domain.SoftwareInstalled {
		t.Fatalf("list=%+v", list)
	}

	row.Status = domain.SoftwareFailed
	row.Error = "verify failed"
	row.UpdatedAt = now.Add(time.Minute)
	if err := store.UpsertInstanceSoftware(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetInstanceSoftware(ctx, "owner-1", "instance-1", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.SoftwareFailed || got.Error != "verify failed" {
		t.Fatalf("got=%+v", got)
	}
}
