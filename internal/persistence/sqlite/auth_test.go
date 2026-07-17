// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/persistence/migrations"
)

func TestConcurrentBootstrapConsumeCreatesExactlyOneCredential(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/bootstrap-race.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	if err := store.CreateOwner(ctx, domain.Owner{ID: "owner-local", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	manager, err := auth.New(store)
	if err != nil {
		t.Fatal(err)
	}
	manager.WithClock(func() time.Time { return now })
	secret, err := manager.EnsureBootstrap(ctx)
	if err != nil {
		t.Fatal(err)
	}

	const contenders = 4
	start := make(chan struct{})
	var winners atomic.Int32
	var wg sync.WaitGroup
	errorsSeen := make(chan error, contenders)
	for i := range contenders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _, err := manager.Bootstrap(ctx, fmt.Sprintf("contender-%d", i), secret, "a sufficiently long password")
			if err == nil {
				winners.Add(1)
				return
			}
			errorsSeen <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errorsSeen)
	if winners.Load() != 1 {
		t.Fatalf("bootstrap winners=%d, want 1", winners.Load())
	}
	for err := range errorsSeen {
		if !errors.Is(err, auth.ErrBootstrapUnavailable) {
			t.Fatalf("loser error=%v", err)
		}
	}
	var credentialCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM owner_credentials`).Scan(&credentialCount); err != nil {
		t.Fatal(err)
	}
	if credentialCount != 1 {
		t.Fatalf("credential count=%d, want 1", credentialCount)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_credentials`).Scan(&credentialCount); err != nil {
		t.Fatal(err)
	}
	if credentialCount != 1 {
		t.Fatalf("user credential count=%d, want 1", credentialCount)
	}
	if _, _, err := manager.Bootstrap(ctx, "loopback", secret, "a sufficiently long password"); !errors.Is(err, auth.ErrBootstrapUnavailable) {
		t.Fatalf("repeat consume error=%v", err)
	}
}

func TestAuthorizeSSHKeyResolvesOnlyRegisteredFingerprint(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/ssh-auth.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	if err := store.CreateOwner(ctx, domain.Owner{ID: "owner-local", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSSHKey(ctx, auth.SSHKey{ID: "key-1", Fingerprint: "SHA256:registered", PublicKey: "ssh-ed25519 AAAA", Label: "laptop", CreatedAt: now}, "owner-local"); err != nil {
		t.Fatal(err)
	}
	owner, allowed, err := store.AuthorizeSSHKey(ctx, "SHA256:registered")
	if err != nil || !allowed || owner != "owner-local" {
		t.Fatalf("owner=%q allowed=%v err=%v", owner, allowed, err)
	}
	owner, allowed, err = store.AuthorizeSSHKey(ctx, "SHA256:unknown")
	if err != nil || allowed || owner != "" {
		t.Fatalf("unknown owner=%q allowed=%v err=%v", owner, allowed, err)
	}
}

func TestMigrationBackfillsLegacyOwnerCredentialAsAdminUser(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range entries {
		if strings.HasPrefix(name, "016_") {
			continue
		}
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		version := strings.TrimSuffix(filepath.Base(name), ".sql")
		checksum := fmt.Sprintf("%x", sha256.Sum256(body))
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version,checksum,applied_at) VALUES(?,?,?)`, version, checksum, formatTime(now)); err != nil {
			t.Fatal(err)
		}
	}
	hash, err := auth.HashPassword("a sufficiently long password", auth.DefaultPasswordParams)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO owners(id,name,created_at,updated_at) VALUES(?,?,?,?)`, "owner-local", "Owner", formatTime(now), formatTime(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO owner_credentials(owner_id,password_hash,updated_at) VALUES(?,?,?)`, "owner-local", hash, formatTime(now)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var users, memberships int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM org_memberships WHERE owner_id=? AND role='admin'`, "owner-local").Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if users != 1 || memberships != 1 {
		t.Fatalf("users=%d memberships=%d, want one backfilled admin", users, memberships)
	}
	manager, err := auth.New(store)
	if err != nil {
		t.Fatal(err)
	}
	manager.WithClock(func() time.Time { return now })
	session, _, err := manager.Login(ctx, "loopback", "", "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	if session.OwnerID != "owner-local" || session.UserID != "usr_owner-local" || session.Role != "admin" {
		t.Fatalf("backfilled session=%+v", session)
	}
}
