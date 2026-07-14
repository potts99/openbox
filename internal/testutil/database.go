// SPDX-License-Identifier: AGPL-3.0-only

// Package testutil contains test helpers shared by OpenBox internal packages.
package testutil

import (
	"context"
	"path/filepath"
	"testing"

	openboxsqlite "github.com/openbox-dev/openbox/internal/persistence/sqlite"
)

func OpenDatabase(t *testing.T) *openboxsqlite.Store {
	t.Helper()
	store, err := openboxsqlite.Open(context.Background(), filepath.Join(t.TempDir(), "openbox.db"))
	if err != nil {
		t.Fatalf("open temporary database: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return store
}
