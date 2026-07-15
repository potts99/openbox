// SPDX-License-Identifier: AGPL-3.0-only

package pi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

// Repository persists Pi profiles and their immutable version history.
type Repository interface {
	CreatePiProfile(context.Context, domain.PiProfile) error
	GetPiProfile(context.Context, domain.OwnerID, domain.PiProfileID) (domain.PiProfile, error)
	ListPiProfiles(context.Context, domain.OwnerID) ([]domain.PiProfile, error)
	UpdatePiProfile(context.Context, domain.PiProfile) error
	InsertPiProfileVersion(context.Context, VersionRecord) error
	ListPiProfileVersions(context.Context, domain.OwnerID, domain.PiProfileID) ([]VersionRecord, error)
	GetPiProfileVersion(context.Context, domain.OwnerID, domain.PiProfileID, int) (VersionRecord, error)
}

// VersionRecord is one immutable snapshot of a profile's settings.
type VersionRecord struct {
	ProfileID    domain.PiProfileID
	OwnerID      domain.OwnerID
	Version      int
	SettingsJSON []byte
	CreatedAt    time.Time
}

// CreateInput creates a named owner profile at version 1.
type CreateInput struct {
	Name     string
	Settings Settings
}

// UpdateInput replaces settings and bumps the profile version.
type UpdateInput struct {
	Settings Settings
}

// Options configures clocks and ID generation.
type Options struct {
	Now   func() time.Time
	NewID func() string
}

// Service owns Pi profile create/get/update and version history.
type Service struct {
	repo  Repository
	now   func() time.Time
	newID func() string
}

// New constructs a Pi profile Service.
func New(repo Repository, options Options) (*Service, error) {
	if repo == nil {
		return nil, &domain.Error{Code: domain.CodeInvalidArgument, Field: "repository"}
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.NewID == nil {
		options.NewID = randomProfileID
	}
	return &Service{repo: repo, now: options.Now, newID: options.NewID}, nil
}

// List returns owner-scoped Pi profiles.
func (s *Service) List(ctx context.Context, ownerID domain.OwnerID) ([]domain.PiProfile, error) {
	return s.repo.ListPiProfiles(ctx, ownerID)
}

// Create stores a new owner-level profile at version 1.
func (s *Service) Create(ctx context.Context, ownerID domain.OwnerID, input CreateInput) (domain.PiProfile, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return domain.PiProfile{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "name"}
	}
	raw, err := input.Settings.Marshal()
	if err != nil {
		return domain.PiProfile{}, err
	}
	now := s.now().UTC()
	profile := domain.PiProfile{
		ID:           domain.PiProfileID(s.newID()),
		OwnerID:      ownerID,
		Name:         name,
		Version:      1,
		SettingsJSON: raw,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.repo.CreatePiProfile(ctx, profile); err != nil {
		return domain.PiProfile{}, err
	}
	if err := s.repo.InsertPiProfileVersion(ctx, VersionRecord{
		ProfileID: profile.ID, OwnerID: ownerID, Version: 1, SettingsJSON: append([]byte(nil), raw...), CreatedAt: now,
	}); err != nil {
		return domain.PiProfile{}, err
	}
	return profile, nil
}

// CreateFromJSON validates raw settings JSON then creates a profile.
func (s *Service) CreateFromJSON(ctx context.Context, ownerID domain.OwnerID, name string, raw []byte) (domain.PiProfile, error) {
	settings, err := ParseSettings(raw)
	if err != nil {
		return domain.PiProfile{}, err
	}
	return s.Create(ctx, ownerID, CreateInput{Name: name, Settings: settings})
}

// Get returns the current profile.
func (s *Service) Get(ctx context.Context, ownerID domain.OwnerID, id domain.PiProfileID) (domain.PiProfile, error) {
	return s.repo.GetPiProfile(ctx, ownerID, id)
}

// Update replaces settings, increments version, and appends history.
func (s *Service) Update(ctx context.Context, ownerID domain.OwnerID, id domain.PiProfileID, input UpdateInput) (domain.PiProfile, error) {
	cur, err := s.repo.GetPiProfile(ctx, ownerID, id)
	if err != nil {
		return domain.PiProfile{}, err
	}
	raw, err := input.Settings.Marshal()
	if err != nil {
		return domain.PiProfile{}, err
	}
	now := s.now().UTC()
	cur.Version++
	cur.SettingsJSON = raw
	cur.UpdatedAt = now
	if err := s.repo.UpdatePiProfile(ctx, cur); err != nil {
		return domain.PiProfile{}, err
	}
	if err := s.repo.InsertPiProfileVersion(ctx, VersionRecord{
		ProfileID: cur.ID, OwnerID: ownerID, Version: cur.Version, SettingsJSON: append([]byte(nil), raw...), CreatedAt: now,
	}); err != nil {
		return domain.PiProfile{}, err
	}
	return cur, nil
}

// ListHistory returns immutable versions in ascending order.
func (s *Service) ListHistory(ctx context.Context, ownerID domain.OwnerID, id domain.PiProfileID) ([]VersionRecord, error) {
	return s.repo.ListPiProfileVersions(ctx, ownerID, id)
}

// GetVersion returns one historical settings snapshot.
func (s *Service) GetVersion(ctx context.Context, ownerID domain.OwnerID, id domain.PiProfileID, version int) (VersionRecord, error) {
	if version < 1 {
		return VersionRecord{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "version"}
	}
	return s.repo.GetPiProfileVersion(ctx, ownerID, id, version)
}

// Rollback restores settings from a prior version by creating a new version.
func (s *Service) Rollback(ctx context.Context, ownerID domain.OwnerID, id domain.PiProfileID, version int) (domain.PiProfile, error) {
	rec, err := s.GetVersion(ctx, ownerID, id, version)
	if err != nil {
		return domain.PiProfile{}, err
	}
	settings, err := ParseSettings(rec.SettingsJSON)
	if err != nil {
		return domain.PiProfile{}, err
	}
	return s.Update(ctx, ownerID, id, UpdateInput{Settings: settings})
}

// Preview returns the current settings decoded for display.
func (s *Service) Preview(ctx context.Context, ownerID domain.OwnerID, id domain.PiProfileID) (Settings, error) {
	profile, err := s.Get(ctx, ownerID, id)
	if err != nil {
		return Settings{}, err
	}
	return ParseSettings(profile.SettingsJSON)
}

func randomProfileID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
