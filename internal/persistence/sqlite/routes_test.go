// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/routes"
)

func TestRouteCRUDRoundTrip(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	createOwner(t, store, now)
	instance := createManagedInstance(t, store, "instance-1", "dev", "incus-ref-1", now)

	route, err := routes.NewRoute("route-1", "owner-1", instance.ID, "dev.openbox.example", 3000, now)
	if err != nil {
		t.Fatal(err)
	}
	route.TLSState = routes.TLSStateNone
	if err := store.CreateRoute(ctx, route); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetRoute(ctx, "owner-1", "route-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Visibility != domain.RoutePrivate {
		t.Fatalf("visibility=%q, want private", got.Visibility)
	}
	if got.Hostname != "dev.openbox.example" || got.TargetPort != 3000 || got.TLSState != routes.TLSStateNone {
		t.Fatalf("unexpected route: %+v", got)
	}

	listed, err := store.ListRoutes(ctx, "owner-1")
	if err != nil || len(listed) != 1 || listed[0].ID != "route-1" {
		t.Fatalf("list=%+v err=%v", listed, err)
	}

	got.Hostname = "app.openbox.example"
	got.TargetPort = 8080
	got.Visibility = domain.RoutePublic
	got.UpdatedAt = now.Add(time.Minute)
	if err := store.UpdateRoute(ctx, got); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetRoute(ctx, "owner-1", "route-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Hostname != "app.openbox.example" || updated.TargetPort != 8080 || updated.Visibility != domain.RoutePublic {
		t.Fatalf("updated=%+v", updated)
	}

	if err := store.DeleteRoute(ctx, "owner-1", "route-1"); err != nil {
		t.Fatal(err)
	}
	_, err = store.GetRoute(ctx, "owner-1", "route-1")
	assertCode(t, err, domain.CodeNotFound)
}

func TestCreateRouteEnforcesHostnameUniquenessPerOwner(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	createOwner(t, store, now)
	instance := createManagedInstance(t, store, "instance-1", "dev", "incus-ref-1", now)

	first, err := routes.NewRoute("route-1", "owner-1", instance.ID, "shared.openbox.example", 3000, now)
	if err != nil {
		t.Fatal(err)
	}
	first.TLSState = routes.TLSStateNone
	if err := store.CreateRoute(ctx, first); err != nil {
		t.Fatal(err)
	}
	second, err := routes.NewRoute("route-2", "owner-1", instance.ID, "shared.openbox.example", 3001, now)
	if err != nil {
		t.Fatal(err)
	}
	second.TLSState = routes.TLSStateNone
	assertCode(t, store.CreateRoute(ctx, second), domain.CodeConflict)
}

func TestDeleteRouteMissingIsNotFound(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	createOwner(t, store, time.Now().UTC())
	assertCode(t, store.DeleteRoute(context.Background(), "owner-1", "missing"), domain.CodeNotFound)
}

func createManagedInstance(t *testing.T, store *Store, id, name, runtimeRef string, now time.Time) domain.Instance {
	t.Helper()
	instance, err := domain.NewInstance(domain.InstanceID(id), "owner-1", name, domain.KindVPS, now)
	if err != nil {
		t.Fatal(err)
	}
	instance.RuntimeRef = runtimeRef
	op := operation(domain.OperationID("op-"+id), "key-"+id, "hash-"+id, now)
	op.TargetID = id
	if _, _, err := store.CreateInstance(context.Background(), instance, op); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetInstance(context.Background(), "owner-1", instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
