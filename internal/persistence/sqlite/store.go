// SPDX-License-Identifier: AGPL-3.0-only

// Package sqlite persists OpenBox metadata in a local SQLite database.
package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/persistence/migrations"
	_ "modernc.org/sqlite"
)

type Store struct {
	db        *sql.DB
	writeGate chan struct{}
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, &domain.Error{Code: domain.CodeInvalidArgument, Field: "path"}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	dsn := "file:" + url.PathEscape(abs) + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetConnMaxLifetime(0)
	store := &Store{db: db, writeGate: make(chan struct{}, 1)}
	store.writeGate <- struct{}{}
	if err := store.migrate(ctx); err != nil {
		db.Close()
		if isCorruptionError(err) {
			return nil, &domain.Error{Code: domain.CodePersistenceCorruption, Field: "database", Cause: err}
		}
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

// acquireWrite serializes read-then-write transactions within openboxd. SQLite
// still supplies cross-process locking; this gate avoids WAL snapshot promotion
// conflicts and remains cancellable while a caller waits.
func (s *Store) acquireWrite(ctx context.Context) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.writeGate:
		return func() { s.writeGate <- struct{}{} }, nil
	}
}

func (s *Store) migrate(ctx context.Context) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migration connection: %w", err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if _, err = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	entries, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)
	for _, name := range entries {
		body, readErr := migrations.Files.ReadFile(name)
		if readErr != nil {
			return fmt.Errorf("read migration %s: %w", name, readErr)
		}
		version := strings.TrimSuffix(filepath.Base(name), ".sql")
		checksum := fmt.Sprintf("%x", sha256.Sum256(body))
		var existing string
		scanErr := conn.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version = ?`, version).Scan(&existing)
		switch {
		case scanErr == nil && existing != checksum:
			return &domain.Error{Code: domain.CodePersistenceCorruption, Field: "migration_checksum"}
		case scanErr == nil:
			continue
		case !errors.Is(scanErr, sql.ErrNoRows):
			return fmt.Errorf("read migration %s: %w", version, scanErr)
		}
		if _, err = conn.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		if _, err = conn.ExecContext(ctx, `INSERT INTO schema_migrations(version, checksum, applied_at) VALUES(?,?,?)`, version, checksum, formatTime(time.Now())); err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	committed = true
	return nil
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func parseTime(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, &domain.Error{Code: domain.CodePersistenceCorruption, Field: "timestamp", Cause: err}
	}
	return t.UTC(), nil
}

func parseNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	t, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func isCorruptionError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "file is not a database") || strings.Contains(message, "database disk image is malformed")
}
