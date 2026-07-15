// SPDX-License-Identifier: AGPL-3.0-only

package pi_test

import (
	"context"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	pi "github.com/openbox-dev/openbox/internal/profiles/pi"
)

func TestCreateGetUpdateBumpsVersionAndKeepsHistory(t *testing.T) {
	t.Parallel()
	repo := newMemoryRepo()
	svc, err := pi.New(repo, pi.Options{
		Now:   func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) },
		NewID: func() string { return "profile-1" },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	owner := domain.OwnerID("owner-1")

	created, err := svc.Create(ctx, owner, pi.CreateInput{
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
	if created.Version != 1 {
		t.Fatalf("version=%d, want 1", created.Version)
	}

	updated, err := svc.Update(ctx, owner, created.ID, pi.UpdateInput{
		Settings: pi.Settings{
			Theme:           "light",
			DefaultProvider: "openai",
			DefaultModel:    "gpt-4o",
			Packages:        []pi.PackageRef{{Source: "pi-skills"}},
			Skills:          []string{"./skills"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 2 {
		t.Fatalf("version=%d, want 2", updated.Version)
	}

	got, err := svc.Get(ctx, owner, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := pi.ParseSettings(got.SettingsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Theme != "light" || settings.DefaultProvider != "openai" {
		t.Fatalf("current settings=%+v", settings)
	}

	history, err := svc.ListHistory(ctx, owner, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history len=%d, want 2", len(history))
	}
	if history[0].Version != 1 || history[1].Version != 2 {
		t.Fatalf("history versions=%+v", history)
	}

	v1, err := svc.GetVersion(ctx, owner, created.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	old, err := pi.ParseSettings(v1.SettingsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if old.Theme != "dark" || old.DefaultProvider != "anthropic" {
		t.Fatalf("v1 settings=%+v", old)
	}
}

func TestCreateRejectsSecretSettings(t *testing.T) {
	t.Parallel()
	svc, err := pi.New(newMemoryRepo(), pi.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateFromJSON(context.Background(), "owner-1", "default", []byte(`{"apiKey":"sk-secret"}`))
	if err == nil {
		t.Fatal("expected secret rejection")
	}
}

func TestCreateRequiresName(t *testing.T) {
	t.Parallel()
	svc, err := pi.New(newMemoryRepo(), pi.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Create(context.Background(), "owner-1", pi.CreateInput{Settings: pi.Settings{Theme: "dark"}})
	if err == nil {
		t.Fatal("expected name required")
	}
}

type memoryRepo struct {
	profiles map[domain.PiProfileID]domain.PiProfile
	history  map[domain.PiProfileID][]pi.VersionRecord
	byName   map[string]domain.PiProfileID
}

func newMemoryRepo() *memoryRepo {
	return &memoryRepo{
		profiles: map[domain.PiProfileID]domain.PiProfile{},
		history:  map[domain.PiProfileID][]pi.VersionRecord{},
		byName:   map[string]domain.PiProfileID{},
	}
}

func (m *memoryRepo) CreatePiProfile(_ context.Context, profile domain.PiProfile) error {
	key := string(profile.OwnerID) + "/" + profile.Name
	if _, ok := m.byName[key]; ok {
		return &domain.Error{Code: domain.CodeConflict, Field: "name"}
	}
	m.profiles[profile.ID] = profile
	m.byName[key] = profile.ID
	return nil
}

func (m *memoryRepo) GetPiProfile(_ context.Context, owner domain.OwnerID, id domain.PiProfileID) (domain.PiProfile, error) {
	p, ok := m.profiles[id]
	if !ok || p.OwnerID != owner {
		return domain.PiProfile{}, &domain.Error{Code: domain.CodeNotFound, Field: "pi_profile"}
	}
	return p, nil
}

func (m *memoryRepo) UpdatePiProfile(_ context.Context, profile domain.PiProfile) error {
	cur, ok := m.profiles[profile.ID]
	if !ok || cur.OwnerID != profile.OwnerID {
		return &domain.Error{Code: domain.CodeNotFound, Field: "pi_profile"}
	}
	m.profiles[profile.ID] = profile
	return nil
}

func (m *memoryRepo) InsertPiProfileVersion(_ context.Context, rec pi.VersionRecord) error {
	m.history[rec.ProfileID] = append(m.history[rec.ProfileID], rec)
	return nil
}

func (m *memoryRepo) ListPiProfileVersions(_ context.Context, owner domain.OwnerID, id domain.PiProfileID) ([]pi.VersionRecord, error) {
	p, ok := m.profiles[id]
	if !ok || p.OwnerID != owner {
		return nil, &domain.Error{Code: domain.CodeNotFound, Field: "pi_profile"}
	}
	out := append([]pi.VersionRecord(nil), m.history[id]...)
	return out, nil
}

func (m *memoryRepo) GetPiProfileVersion(_ context.Context, owner domain.OwnerID, id domain.PiProfileID, version int) (pi.VersionRecord, error) {
	p, ok := m.profiles[id]
	if !ok || p.OwnerID != owner {
		return pi.VersionRecord{}, &domain.Error{Code: domain.CodeNotFound, Field: "pi_profile"}
	}
	for _, rec := range m.history[id] {
		if rec.Version == version {
			return rec, nil
		}
	}
	return pi.VersionRecord{}, &domain.Error{Code: domain.CodeNotFound, Field: "version"}
}
