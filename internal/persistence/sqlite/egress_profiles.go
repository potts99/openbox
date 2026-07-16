// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

// EnsureSystemEgressProfiles inserts the seeded system profiles if missing.
func (s *Store) EnsureSystemEgressProfiles(ctx context.Context) error {
	now := time.Now().UTC()
	for _, profile := range []domain.EgressProfile{
		{
			ID: domain.EgressProfileIDStandard, Name: domain.EgressProfileNameStandard,
			Mode: domain.EgressStandard, AllowedDestinationsJSON: []byte("[]"),
			DNSPolicy: domain.DNSPolicyHostResolve, System: true,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: domain.EgressProfileIDRestricted, Name: domain.EgressProfileNameRestricted,
			Mode: domain.EgressRestricted, AllowedDestinationsJSON: []byte("[]"),
			DNSPolicy: domain.DNSPolicyHostResolve, System: true,
			CreatedAt: now, UpdatedAt: now,
		},
	} {
		_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO egress_profiles(
			id, name, mode, allowed_destinations_json, dns_policy, system, created_at, updated_at
		) VALUES(?,?,?,?,?,?,?,?)`,
			profile.ID, profile.Name, profile.Mode, profile.AllowedDestinationsJSON, profile.DNSPolicy,
			boolToInt(profile.System), formatTime(profile.CreatedAt), formatTime(profile.UpdatedAt))
		if err != nil {
			return mapWriteError(err)
		}
	}
	return nil
}

func (s *Store) CreateEgressProfile(ctx context.Context, profile domain.EgressProfile) (domain.EgressProfile, error) {
	if err := profile.Validate(); err != nil {
		return domain.EgressProfile{}, err
	}
	if profile.System {
		return domain.EgressProfile{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "system"}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO egress_profiles(
		id, name, mode, allowed_destinations_json, dns_policy, system, created_at, updated_at
	) VALUES(?,?,?,?,?,?,?,?)`,
		profile.ID, profile.Name, profile.Mode, profile.AllowedDestinationsJSON, profile.DNSPolicy,
		boolToInt(profile.System), formatTime(profile.CreatedAt), formatTime(profile.UpdatedAt))
	if err != nil {
		return domain.EgressProfile{}, mapWriteError(err)
	}
	return s.GetEgressProfile(ctx, profile.ID)
}

func (s *Store) GetEgressProfile(ctx context.Context, id domain.EgressProfileID) (domain.EgressProfile, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, mode, allowed_destinations_json, dns_policy, system, created_at, updated_at
		FROM egress_profiles WHERE id=?`, id)
	return scanEgressProfile(row)
}

func (s *Store) GetEgressProfileByName(ctx context.Context, name string) (domain.EgressProfile, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, mode, allowed_destinations_json, dns_policy, system, created_at, updated_at
		FROM egress_profiles WHERE name=?`, name)
	return scanEgressProfile(row)
}

func (s *Store) ListEgressProfiles(ctx context.Context) ([]domain.EgressProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, mode, allowed_destinations_json, dns_policy, system, created_at, updated_at
		FROM egress_profiles ORDER BY system DESC, name`)
	if err != nil {
		return nil, fmt.Errorf("list egress profiles: %w", err)
	}
	defer rows.Close()
	result := make([]domain.EgressProfile, 0)
	for rows.Next() {
		profile, scanErr := scanEgressProfile(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, profile)
	}
	return result, rows.Err()
}

func (s *Store) UpdateEgressProfile(ctx context.Context, profile domain.EgressProfile) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	existing, err := s.GetEgressProfile(ctx, profile.ID)
	if err != nil {
		return err
	}
	if existing.System && profile.Name != existing.Name {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "name"}
	}
	result, err := s.db.ExecContext(ctx, `UPDATE egress_profiles SET name=?, mode=?, allowed_destinations_json=?, dns_policy=?, updated_at=?
		WHERE id=?`,
		profile.Name, profile.Mode, profile.AllowedDestinationsJSON, profile.DNSPolicy, formatTime(profile.UpdatedAt), profile.ID)
	if err != nil {
		return mapWriteError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return &domain.Error{Code: domain.CodeNotFound, Field: "egress_profile"}
	}
	return nil
}

func (s *Store) DeleteEgressProfile(ctx context.Context, id domain.EgressProfileID) error {
	profile, err := s.GetEgressProfile(ctx, id)
	if err != nil {
		return err
	}
	if profile.System {
		return &domain.Error{Code: domain.CodeConflict, Field: "egress_profile"}
	}
	count, err := s.CountInstancesWithEgressProfile(ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return &domain.Error{Code: domain.CodeConflict, Field: "egress_profile"}
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM egress_profiles WHERE id=? AND system=0`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return &domain.Error{Code: domain.CodeNotFound, Field: "egress_profile"}
	}
	return nil
}

func (s *Store) CountInstancesWithEgressProfile(ctx context.Context, id domain.EgressProfileID) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM instances WHERE egress_profile_id=? AND deleted_at IS NULL`, id).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count instances with egress profile: %w", err)
	}
	return count, nil
}

// ListInstancesWithEgressProfile returns non-tombstoned instances attached to a profile.
func (s *Store) ListInstancesWithEgressProfile(ctx context.Context, id domain.EgressProfileID) ([]domain.Instance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+instanceColumns+` FROM instances WHERE egress_profile_id=? AND deleted_at IS NULL ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("list instances with egress profile: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Instance, 0)
	for rows.Next() {
		instance, scanErr := scanInstance(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, instance)
	}
	return result, rows.Err()
}

// UpdateInstanceEgressProfile sets the attached profile and denormalized mode.
func (s *Store) UpdateInstanceEgressProfile(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, profileID domain.EgressProfileID, mode domain.EgressMode, updatedAt time.Time) error {
	if profileID == "" {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "egress_profile_id"}
	}
	switch mode {
	case domain.EgressStandard, domain.EgressRestricted:
	default:
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "egress_mode"}
	}
	if _, err := s.GetEgressProfile(ctx, profileID); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE instances SET egress_profile_id=?, egress_mode=?, updated_at=?
		WHERE owner_id=? AND id=? AND deleted_at IS NULL`,
		profileID, mode, formatTime(updatedAt), ownerID, id)
	if err != nil {
		return mapWriteError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	}
	return nil
}

func scanEgressProfile(row rowScanner) (domain.EgressProfile, error) {
	var profile domain.EgressProfile
	var system int
	var created, updated string
	err := row.Scan(&profile.ID, &profile.Name, &profile.Mode, &profile.AllowedDestinationsJSON,
		&profile.DNSPolicy, &system, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.EgressProfile{}, &domain.Error{Code: domain.CodeNotFound, Field: "egress_profile"}
	}
	if err != nil {
		return domain.EgressProfile{}, fmt.Errorf("scan egress profile: %w", err)
	}
	profile.System = system != 0
	if profile.CreatedAt, err = parseTime(created); err != nil {
		return domain.EgressProfile{}, err
	}
	if profile.UpdatedAt, err = parseTime(updated); err != nil {
		return domain.EgressProfile{}, err
	}
	if err := profile.Validate(); err != nil {
		return domain.EgressProfile{}, &domain.Error{Code: domain.CodePersistenceCorruption, Field: "egress_profile", Cause: err}
	}
	return profile, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
