// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	_ "modernc.org/sqlite"
)

func TestOpenMigratesOnDiskAndReopens(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "openbox.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	assertPragma(t, store.db, "foreign_keys", "1")
	assertPragma(t, store.db, "journal_mode", "wal")
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 7 {
		t.Fatalf("migration count=%d, want 7", count)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 7 {
		t.Fatalf("migration count after reopen=%d, want 7", count)
	}
}

func TestMigrationChecksumCorruptionIsDetected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "openbox.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE schema_migrations SET checksum='tampered'`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Open(ctx, path)
	assertCode(t, err, domain.CodePersistenceCorruption)
}

func TestSchemaHasNoDeferredProductTables(t *testing.T) {
	store := openStore(t)
	rows, err := store.db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name == "billing" || name == "nodes" || name == "hosts" || name == "schedulers" {
			t.Fatalf("deferred table exists: %s", name)
		}
	}
}

func TestCreateInstanceIsTransactionalAndIdempotent(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	createOwner(t, store, now)
	i, err := domain.NewInstance("instance-1", "owner-1", "project", domain.KindDevbox, now)
	if err != nil {
		t.Fatal(err)
	}
	op := operation("operation-1", "create-project", "hash-1", now)
	got, replay, err := store.CreateInstance(ctx, i, op)
	if err != nil || replay || got.ID != op.ID {
		t.Fatalf("create got=%+v replay=%v err=%v", got, replay, err)
	}
	got, replay, err = store.CreateInstance(ctx, i, operation("operation-2", "create-project", "hash-1", now))
	if err != nil || !replay || got.ID != op.ID {
		t.Fatalf("replay got=%+v replay=%v err=%v", got, replay, err)
	}
	_, _, err = store.CreateInstance(ctx, i, operation("operation-3", "create-project", "different", now))
	assertCode(t, err, domain.CodeIdempotencyConflict)
	var instances, operations int
	if err := store.db.QueryRow(`SELECT count(*) FROM instances`).Scan(&instances); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT count(*) FROM operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if instances != 1 || operations != 1 {
		t.Fatalf("instances=%d operations=%d", instances, operations)
	}
}

func TestCreateRollsBackOperationWhenTargetFails(t *testing.T) {
	store := openStore(t)
	now := time.Now().UTC()
	createOwner(t, store, now)
	i, _ := domain.NewInstance("instance-1", "owner-1", "same", domain.KindVPS, now)
	if _, _, err := store.CreateInstance(context.Background(), i, operation("op-1", "key-1", "hash-1", now)); err != nil {
		t.Fatal(err)
	}
	i.ID = "instance-2"
	op := operation("op-2", "key-2", "hash-2", now)
	op.TargetID = "instance-2"
	_, _, err := store.CreateInstance(context.Background(), i, op)
	assertCode(t, err, domain.CodeConflict)
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM operations WHERE id='op-2'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed target left its operation behind")
	}
}

func TestInvalidStateTransitionCannotBePersisted(t *testing.T) {
	store := openStore(t)
	now := time.Now().UTC()
	createOwner(t, store, now)
	i, _ := domain.NewInstance("instance-1", "owner-1", "project", domain.KindVPS, now)
	if _, _, err := store.CreateInstance(context.Background(), i, operation("op-1", "key-1", "hash-1", now)); err != nil {
		t.Fatal(err)
	}
	err := store.UpdateInstanceState(context.Background(), "owner-1", "instance-1", domain.DesiredRunning, domain.ObservedDeleted, now.Add(time.Second), operation("op-2", "key-2", "hash-2", now))
	assertCode(t, err, domain.CodeInvalidTransition)
	stored, err := store.GetInstance(context.Background(), "owner-1", "instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.ObservedState != domain.ObservedPending {
		t.Fatalf("state changed to %s", stored.ObservedState)
	}
}

func TestProtectedBaseCannotBeDeleted(t *testing.T) {
	store := openStore(t)
	now := time.Now().UTC()
	createOwner(t, store, now)
	i, _ := domain.NewInstance("instance-1", "owner-1", "base", domain.KindDevbox, now)
	i.Protected = true
	if _, _, err := store.CreateInstance(context.Background(), i, operation("op-1", "key-1", "hash-1", now)); err != nil {
		t.Fatal(err)
	}
	err := store.UpdateInstanceState(context.Background(), "owner-1", "instance-1", domain.DesiredDeleted, domain.ObservedDeleting, now, operation("op-2", "key-2", "hash-2", now))
	assertCode(t, err, domain.CodeProtectedBase)
}

func TestConcurrentIdempotentCreateProducesOneInstance(t *testing.T) {
	store := openStore(t)
	now := time.Now().UTC()
	createOwner(t, store, now)
	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	errs := make(chan error, workers)
	for n := 0; n < workers; n++ {
		go func(n int) {
			defer wg.Done()
			i, _ := domain.NewInstance(domain.InstanceID("instance-concurrent"), "owner-1", "concurrent", domain.KindVPS, now)
			op := operation(domain.OperationID("op-concurrent-"+string(rune('a'+n))), "same-key", "same-hash", now)
			op.TargetID = "instance-concurrent"
			_, _, err := store.CreateInstance(context.Background(), i, op)
			errs <- err
		}(n)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent create: %v", err)
		}
	}
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM instances WHERE name='concurrent'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("instances=%d, want 1", count)
	}
}

func TestContextCancellation(t *testing.T) {
	store := openStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.GetInstance(ctx, "owner-1", "missing")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context cancellation", err)
	}
}

func TestCorruptTimestampIsDetected(t *testing.T) {
	store := openStore(t)
	now := time.Now().UTC()
	createOwner(t, store, now)
	i, _ := domain.NewInstance("instance-1", "owner-1", "project", domain.KindVPS, now)
	if _, _, err := store.CreateInstance(context.Background(), i, operation("op-1", "key-1", "hash-1", now)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE instances SET updated_at='not-a-time' WHERE id='instance-1'`); err != nil {
		t.Fatal(err)
	}
	_, err := store.GetInstance(context.Background(), "owner-1", "instance-1")
	assertCode(t, err, domain.CodePersistenceCorruption)
}

func TestDatabaseRejectsNegativeResources(t *testing.T) {
	store := openStore(t)
	now := time.Now().UTC()
	createOwner(t, store, now)
	i, _ := domain.NewInstance("instance-1", "owner-1", "project", domain.KindVPS, now)
	if _, _, err := store.CreateInstance(context.Background(), i, operation("op-1", "key-1", "hash-1", now)); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE instances SET vcpus=-1 WHERE id='instance-1'`,
		`UPDATE instances SET memory_bytes=-1 WHERE id='instance-1'`,
		`UPDATE instances SET disk_bytes=-1 WHERE id='instance-1'`,
	} {
		if _, err := store.db.Exec(statement); err == nil {
			t.Fatalf("database accepted negative resource: %s", statement)
		}
	}
}

func TestCorruptDatabaseFileIsDetected(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "openbox.db")
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(context.Background(), path)
	assertCode(t, err, domain.CodePersistenceCorruption)
}

func TestTombstoneRemovesActiveMetadataAndRetainsIdentity(t *testing.T) {
	store := openStore(t)
	now := time.Now().UTC()
	createOwner(t, store, now)
	i, _ := domain.NewInstance("instance-1", "owner-1", "temporary", domain.KindVPS, now)
	if _, _, err := store.CreateInstance(context.Background(), i, operation("op-1", "key-1", "hash-1", now)); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateInstanceState(context.Background(), "owner-1", "instance-1", domain.DesiredDeleted, domain.ObservedDeleting, now.Add(time.Second), operation("op-2", "key-2", "hash-2", now)); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateInstanceState(context.Background(), "owner-1", "instance-1", domain.DesiredDeleted, domain.ObservedDeleted, now.Add(2*time.Second), operation("op-3", "key-3", "hash-3", now)); err != nil {
		t.Fatal(err)
	}
	if err := store.TombstoneInstance(context.Background(), "owner-1", "instance-1", operation("op-4", "key-4", "hash-4", now), now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetInstance(context.Background(), "owner-1", "instance-1"); err == nil {
		t.Fatal("deleted instance remains active")
	}
	var name string
	if err := store.db.QueryRow(`SELECT name FROM instance_tombstones WHERE instance_id='instance-1'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "temporary" {
		t.Fatalf("tombstone name=%s", name)
	}
}

func TestInstanceNameCanBeReusedAndDeletedAgain(t *testing.T) {
	store := openStore(t)
	now := time.Now().UTC()
	createOwner(t, store, now)

	first, _ := domain.NewInstance("instance-1", "owner-1", "reusable", domain.KindVPS, now)
	if _, _, err := store.CreateInstance(context.Background(), first, operation("op-1", "key-1", "hash-1", now)); err != nil {
		t.Fatal(err)
	}
	deleteAndTombstone(t, store, first.ID, "first", now.Add(time.Second))

	second, _ := domain.NewInstance("instance-2", "owner-1", "reusable", domain.KindVPS, now.Add(5*time.Second))
	createSecond := operation("op-5", "key-5", "hash-5", now.Add(5*time.Second))
	createSecond.TargetID = string(second.ID)
	if _, _, err := store.CreateInstance(context.Background(), second, createSecond); err != nil {
		t.Fatalf("recreate same name: %v", err)
	}
	deleteAndTombstone(t, store, second.ID, "second", now.Add(6*time.Second))

	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM instance_tombstones WHERE owner_id='owner-1' AND name='reusable'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("tombstones=%d, want 2", count)
	}
}

func openStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "openbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return store
}

func createOwner(t *testing.T, store *Store, now time.Time) {
	t.Helper()
	if err := store.CreateOwner(context.Background(), domain.Owner{ID: "owner-1", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
}

func operation(id domain.OperationID, key, hash string, now time.Time) domain.Operation {
	return domain.Operation{ID: id, OwnerID: "owner-1", Type: "instance.create", TargetType: "instance", TargetID: "instance-1", Status: domain.OperationPending, Stage: "queued", IdempotencyKey: key, RequestHash: hash, CreatedAt: now, UpdatedAt: now}
}

func deleteAndTombstone(t *testing.T, store *Store, id domain.InstanceID, prefix string, now time.Time) {
	t.Helper()
	deleting := operation(domain.OperationID(prefix+"-deleting"), prefix+"-deleting-key", prefix+"-deleting-hash", now)
	deleting.TargetID = string(id)
	if err := store.UpdateInstanceState(context.Background(), "owner-1", id, domain.DesiredDeleted, domain.ObservedDeleting, now, deleting); err != nil {
		t.Fatal(err)
	}
	deleted := operation(domain.OperationID(prefix+"-deleted"), prefix+"-deleted-key", prefix+"-deleted-hash", now.Add(time.Second))
	deleted.TargetID = string(id)
	if err := store.UpdateInstanceState(context.Background(), "owner-1", id, domain.DesiredDeleted, domain.ObservedDeleted, now.Add(time.Second), deleted); err != nil {
		t.Fatal(err)
	}
	tombstone := operation(domain.OperationID(prefix+"-tombstone"), prefix+"-tombstone-key", prefix+"-tombstone-hash", now.Add(2*time.Second))
	tombstone.TargetID = string(id)
	if err := store.TombstoneInstance(context.Background(), "owner-1", id, tombstone, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
}

func assertCode(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != code {
		t.Fatalf("got %v, want code %s", err, code)
	}
}

func assertPragma(t *testing.T, db *sql.DB, name, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow(`PRAGMA ` + name).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("PRAGMA %s=%s, want %s", name, got, want)
	}
}
