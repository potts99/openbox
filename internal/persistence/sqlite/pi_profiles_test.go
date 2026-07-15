// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	pi "github.com/openbox-dev/openbox/internal/profiles/pi"
)

func TestPiProfileCRUDAndVersionHistory(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	createOwner(t, store, now)

	svc, err := pi.New(store, pi.Options{
		Now:   func() time.Time { return now },
		NewID: func() string { return "profile-1" },
	})
	if err != nil {
		t.Fatal(err)
	}

	created, err := svc.Create(ctx, "owner-1", pi.CreateInput{
		Name: "default",
		Settings: pi.Settings{
			Theme:           "dark",
			DefaultProvider: "anthropic",
			DefaultModel:    "claude-sonnet-4-20250514",
			Packages:        []pi.PackageRef{{Source: "pi-skills"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Update(ctx, "owner-1", created.ID, pi.UpdateInput{
		Settings: pi.Settings{Theme: "light", DefaultProvider: "openai", DefaultModel: "gpt-4o"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.GetPiProfile(ctx, "owner-1", "profile-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 {
		t.Fatalf("version=%d, want 2", got.Version)
	}

	history, err := store.ListPiProfileVersions(ctx, "owner-1", "profile-1")
	if err != nil || len(history) != 2 {
		t.Fatalf("history=%+v err=%v", history, err)
	}

	dup, err := svc.Create(ctx, "owner-1", pi.CreateInput{Name: "default", Settings: pi.Settings{Theme: "dark"}})
	if err == nil {
		t.Fatalf("expected name conflict, got %+v", dup)
	}
	assertCode(t, err, domain.CodeConflict)
}
