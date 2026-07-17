// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

func TestArtifactMetadataQuotasAndGC(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	createOwner(t, store, now)
	instance := createManagedInstance(t, store, "instance-1", "dev", "incus-ref-1", now)

	for index := 0; index < 100; index++ {
		path := fmt.Sprintf("results/%03d.txt", index)
		_, _, _, err := store.PutArtifact(ctx, domain.Artifact{
			ID: domain.ArtifactID(fmt.Sprintf("artifact-%03d", index)), OwnerID: "owner-1", InstanceID: instance.ID,
			Path: path, SizeBytes: 1, ContentType: "text/plain", SHA256: fmt.Sprintf("%064d", index),
			CreatedAt: now, UpdatedAt: now,
		}, "")
		if err != nil {
			t.Fatalf("put %d: %v", index, err)
		}
	}
	if err := store.CheckArtifactUpload(ctx, "owner-1", instance.ID, "results/overflow.txt", 1); err == nil {
		t.Fatal("expected artifact count quota")
	} else {
		assertCode(t, err, domain.CodeQuotaExceeded)
	}
	if err := store.CheckArtifactUpload(ctx, "owner-1", instance.ID, "results/large.bin", int64(256<<20)+1); err == nil {
		t.Fatal("expected artifact size rejection")
	} else {
		assertCode(t, err, domain.CodeInvalidArgument)
	}
	items, err := store.ListArtifacts(ctx, "owner-1", instance.ID, "results/0")
	if err != nil || len(items) != 100 || items[0].Path != "results/000.txt" {
		t.Fatalf("list=%+v err=%v", items, err)
	}
	if err := store.DeleteInstanceArtifacts(ctx, "owner-1", instance.ID); err != nil {
		t.Fatal(err)
	}
	items, err = store.ListArtifacts(ctx, "owner-1", instance.ID, "")
	if err != nil || len(items) != 0 {
		t.Fatalf("after gc list=%+v err=%v", items, err)
	}
}
