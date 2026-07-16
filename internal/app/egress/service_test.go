// SPDX-License-Identifier: AGPL-3.0-only

package egress_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/egress"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/persistence/sqlite"
)

type recordingApplicatorRuntime struct {
	failIDs map[domain.InstanceID]bool
	applied []domain.InstanceID
}

func (r *recordingApplicatorRuntime) ProgramNetworkPolicy(_ context.Context, apply egress.PolicyApply) error {
	r.applied = append(r.applied, apply.Instance.ID)
	if r.failIDs[apply.Instance.ID] {
		return errors.New("apply failed")
	}
	return nil
}

func (r *recordingApplicatorRuntime) SetAllowlistResolution(domain.InstanceID, domain.AllowlistResolution) {
}

func TestUpdateProfileFanOutKeepsProfileOnInstanceFailure(t *testing.T) {
	store := openEgressStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	if err := store.EnsureSystemEgressProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOwner(ctx, domain.Owner{ID: "owner-1", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	custom, err := store.CreateEgressProfile(ctx, domain.EgressProfile{
		ID: "egress-custom", Name: "allow-npm", Mode: domain.EgressRestricted,
		AllowedDestinationsJSON: []byte(`[]`), DNSPolicy: domain.DNSPolicyHostResolve,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	createBoundInstance(t, store, "inst-a", custom.ID, now)
	createBoundInstance(t, store, "inst-b", custom.ID, now.Add(time.Second))

	runtime := &recordingApplicatorRuntime{failIDs: map[domain.InstanceID]bool{"inst-a": true}}
	app := egress.NewApplicator(nil, runtime)
	service, err := egress.New(store, app, egress.Options{Now: func() time.Time { return now.Add(2 * time.Minute) }})
	if err != nil {
		t.Fatal(err)
	}

	destinations := []string{"203.0.113.10"}
	updated, applyErrors, err := service.Update(ctx, custom.ID, egress.UpdateProfileInput{AllowedDestinations: &destinations})
	if err != nil {
		t.Fatal(err)
	}
	if string(updated.AllowedDestinationsJSON) != `["203.0.113.10"]` {
		t.Fatalf("profile destinations=%s", updated.AllowedDestinationsJSON)
	}
	if len(applyErrors) != 1 || applyErrors[0].InstanceID != "inst-a" {
		t.Fatalf("applyErrors=%#v", applyErrors)
	}
	reloaded, err := store.GetEgressProfile(ctx, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(reloaded.AllowedDestinationsJSON) != `["203.0.113.10"]` {
		t.Fatal("profile was rolled back")
	}
}

func TestCreateInstanceAttachesDefaultRestrictedForSandbox(t *testing.T) {
	store := openEgressStore(t)
	ctx := context.Background()
	service, err := egress.New(store, nil, egress.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.EnsureSeeds(ctx); err != nil {
		t.Fatal(err)
	}
	profile, err := service.ResolveProfileForCreate(ctx, domain.KindSandbox, "")
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID != domain.EgressProfileIDRestricted || profile.Mode != domain.EgressRestricted {
		t.Fatalf("profile=%#v", profile)
	}
}

func openEgressStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/openbox.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func createBoundInstance(t *testing.T, store *sqlite.Store, id domain.InstanceID, profileID domain.EgressProfileID, now time.Time) {
	t.Helper()
	instance, err := domain.NewInstance(id, "owner-1", string(id), domain.KindSandbox, now)
	if err != nil {
		t.Fatal(err)
	}
	instance.EgressProfileID = profileID
	instance.EgressMode = domain.EgressRestricted
	instance.RuntimeRef = string(id)
	op := domain.Operation{
		ID: domain.OperationID("op-" + id), OwnerID: "owner-1", Type: "instance.create",
		TargetType: "instance", TargetID: string(id), Status: domain.OperationPending,
		Stage: "pending", Progress: 0, IdempotencyKey: "key-" + string(id), RequestHash: "hash-" + string(id),
		CreatedAt: now, UpdatedAt: now,
	}
	if _, _, err := store.CreateInstance(context.Background(), instance, op); err != nil {
		t.Fatal(err)
	}
}
