// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/openbox-dev/openbox/internal/domain"
	pi "github.com/openbox-dev/openbox/internal/profiles/pi"
)

func (s *Store) ListPiProfiles(ctx context.Context, ownerID domain.OwnerID) ([]domain.PiProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,owner_id,name,version,settings_json,created_at,updated_at
		FROM pi_profiles WHERE owner_id=? ORDER BY name, id`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list pi profiles: %w", err)
	}
	defer rows.Close()
	out := make([]domain.PiProfile, 0)
	for rows.Next() {
		profile, scanErr := scanPiProfile(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, profile)
	}
	return out, rows.Err()
}

func (s *Store) CreatePiProfile(ctx context.Context, profile domain.PiProfile) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO pi_profiles(id,owner_id,name,version,settings_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?)`,
		profile.ID, profile.OwnerID, profile.Name, profile.Version, profile.SettingsJSON,
		formatTime(profile.CreatedAt), formatTime(profile.UpdatedAt))
	return mapWriteError(err)
}

func (s *Store) GetPiProfile(ctx context.Context, ownerID domain.OwnerID, id domain.PiProfileID) (domain.PiProfile, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,owner_id,name,version,settings_json,created_at,updated_at
		FROM pi_profiles WHERE owner_id=? AND id=?`, ownerID, id)
	return scanPiProfile(row)
}

func (s *Store) UpdatePiProfile(ctx context.Context, profile domain.PiProfile) error {
	result, err := s.db.ExecContext(ctx, `UPDATE pi_profiles SET version=?,settings_json=?,updated_at=?
		WHERE owner_id=? AND id=?`,
		profile.Version, profile.SettingsJSON, formatTime(profile.UpdatedAt), profile.OwnerID, profile.ID)
	if err != nil {
		return mapWriteError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return &domain.Error{Code: domain.CodeNotFound, Field: "pi_profile"}
	}
	return nil
}

func (s *Store) InsertPiProfileVersion(ctx context.Context, rec pi.VersionRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO pi_profile_versions(profile_id,owner_id,version,settings_json,created_at)
		VALUES(?,?,?,?,?)`,
		rec.ProfileID, rec.OwnerID, rec.Version, rec.SettingsJSON, formatTime(rec.CreatedAt))
	return mapWriteError(err)
}

func (s *Store) ListPiProfileVersions(ctx context.Context, ownerID domain.OwnerID, id domain.PiProfileID) ([]pi.VersionRecord, error) {
	if _, err := s.GetPiProfile(ctx, ownerID, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT profile_id,owner_id,version,settings_json,created_at
		FROM pi_profile_versions WHERE owner_id=? AND profile_id=? ORDER BY version`, ownerID, id)
	if err != nil {
		return nil, fmt.Errorf("list pi profile versions: %w", err)
	}
	defer rows.Close()
	out := make([]pi.VersionRecord, 0)
	for rows.Next() {
		rec, scanErr := scanPiProfileVersion(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) GetPiProfileVersion(ctx context.Context, ownerID domain.OwnerID, id domain.PiProfileID, version int) (pi.VersionRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT profile_id,owner_id,version,settings_json,created_at
		FROM pi_profile_versions WHERE owner_id=? AND profile_id=? AND version=?`, ownerID, id, version)
	return scanPiProfileVersion(row)
}

type piProfileScanner interface {
	Scan(dest ...any) error
}

func scanPiProfile(row piProfileScanner) (domain.PiProfile, error) {
	var profile domain.PiProfile
	var created, updated string
	err := row.Scan(&profile.ID, &profile.OwnerID, &profile.Name, &profile.Version, &profile.SettingsJSON, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PiProfile{}, &domain.Error{Code: domain.CodeNotFound, Field: "pi_profile"}
	}
	if err != nil {
		return domain.PiProfile{}, err
	}
	if profile.CreatedAt, err = parseTime(created); err != nil {
		return domain.PiProfile{}, err
	}
	if profile.UpdatedAt, err = parseTime(updated); err != nil {
		return domain.PiProfile{}, err
	}
	return profile, nil
}

func scanPiProfileVersion(row piProfileScanner) (pi.VersionRecord, error) {
	var rec pi.VersionRecord
	var created string
	err := row.Scan(&rec.ProfileID, &rec.OwnerID, &rec.Version, &rec.SettingsJSON, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return pi.VersionRecord{}, &domain.Error{Code: domain.CodeNotFound, Field: "version"}
	}
	if err != nil {
		return pi.VersionRecord{}, err
	}
	if rec.CreatedAt, err = parseTime(created); err != nil {
		return pi.VersionRecord{}, err
	}
	return rec, nil
}
