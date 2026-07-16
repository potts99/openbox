// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

func TestEgressProfileCRUDAndDeleteGuards(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := store.EnsureSystemEgressProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	restricted, err := store.GetEgressProfileByName(ctx, domain.EgressProfileNameRestricted)
	if err != nil {
		t.Fatal(err)
	}
	if !restricted.System || restricted.ID != domain.EgressProfileIDRestricted {
		t.Fatalf("seeded restricted: %#v", restricted)
	}
	if err := store.DeleteEgressProfile(ctx, restricted.ID); err == nil {
		t.Fatal("expected delete of system profile to fail")
	} else {
		var domainErr *domain.Error
		if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeConflict {
			t.Fatalf("delete system err=%v", err)
		}
	}

	custom := domain.EgressProfile{
		ID:                      domain.EgressProfileID("egress-custom"),
		Name:                    "allow-npm",
		Mode:                    domain.EgressRestricted,
		AllowedDestinationsJSON: []byte(`["registry.npmjs.org"]`),
		DNSPolicy:               domain.DNSPolicyHostResolve,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	created, err := store.CreateEgressProfile(ctx, custom)
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "allow-npm" {
		t.Fatalf("created=%#v", created)
	}

	createOwner(t, store, now)
	instance, err := domain.NewInstance("instance-1", "owner-1", "sandbox-1", domain.KindSandbox, now)
	if err != nil {
		t.Fatal(err)
	}
	instance.EgressProfileID = created.ID
	instance.EgressMode = created.Mode
	if _, _, err := store.CreateInstance(ctx, instance, operation("op-1", "key-1", "hash-1", now)); err != nil {
		t.Fatal(err)
	}
	count, err := store.CountInstancesWithEgressProfile(ctx, created.ID)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := store.DeleteEgressProfile(ctx, created.ID); err == nil {
		t.Fatal("expected delete of in-use profile to fail")
	}

	if err := store.UpdateInstanceEgressProfile(ctx, "owner-1", "instance-1", domain.EgressProfileIDRestricted, domain.EgressRestricted, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetInstance(ctx, "owner-1", "instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.EgressProfileID != domain.EgressProfileIDRestricted || got.EgressMode != domain.EgressRestricted {
		t.Fatalf("instance after reattach=%#v", got)
	}
	if err := store.DeleteEgressProfile(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationSeedsSystemEgressProfiles(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	profiles, err := store.ListEgressProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) < 2 {
		t.Fatalf("profiles=%d", len(profiles))
	}
	standard, err := store.GetEgressProfile(ctx, domain.EgressProfileIDStandard)
	if err != nil || !standard.System || standard.Mode != domain.EgressStandard {
		t.Fatalf("standard=%#v err=%v", standard, err)
	}
}
