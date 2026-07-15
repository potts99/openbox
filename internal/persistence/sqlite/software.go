// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/openbox-dev/openbox/internal/domain"
)

// UpsertInstanceSoftware inserts or replaces a software row for an instance.
func (s *Store) UpsertInstanceSoftware(ctx context.Context, row domain.InstanceSoftware) error {
	if err := validateInstanceSoftware(row); err != nil {
		return err
	}
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	_, err = s.db.ExecContext(ctx, `INSERT INTO instance_software(
		instance_id, owner_id, package_id, status, version, error, updated_at
	) VALUES(?,?,?,?,?,?,?)
	ON CONFLICT(instance_id, package_id) DO UPDATE SET
		owner_id=excluded.owner_id,
		status=excluded.status,
		version=excluded.version,
		error=excluded.error,
		updated_at=excluded.updated_at`,
		row.InstanceID, row.OwnerID, row.PackageID, row.Status, row.Version, row.Error, formatTime(row.UpdatedAt.UTC()))
	if err != nil {
		return mapWriteError(err)
	}
	return nil
}

// ListInstanceSoftware returns software rows for an owner-scoped instance.
func (s *Store) ListInstanceSoftware(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID) ([]domain.InstanceSoftware, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id, owner_id, package_id, status, version, error, updated_at
		FROM instance_software WHERE owner_id=? AND instance_id=? ORDER BY package_id`, ownerID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list instance software: %w", err)
	}
	defer rows.Close()
	out := make([]domain.InstanceSoftware, 0)
	for rows.Next() {
		row, err := scanInstanceSoftware(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list instance software: %w", err)
	}
	return out, nil
}

// GetInstanceSoftware returns one software row, or NotFound.
func (s *Store) GetInstanceSoftware(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID, packageID string) (domain.InstanceSoftware, error) {
	row := s.db.QueryRowContext(ctx, `SELECT instance_id, owner_id, package_id, status, version, error, updated_at
		FROM instance_software WHERE owner_id=? AND instance_id=? AND package_id=?`, ownerID, instanceID, packageID)
	got, err := scanInstanceSoftware(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return domain.InstanceSoftware{}, &domain.Error{Code: domain.CodeNotFound, Field: "software"}
		}
		return domain.InstanceSoftware{}, err
	}
	return got, nil
}

func scanInstanceSoftware(row rowScanner) (domain.InstanceSoftware, error) {
	var (
		out       domain.InstanceSoftware
		updatedAt string
	)
	if err := row.Scan(&out.InstanceID, &out.OwnerID, &out.PackageID, &out.Status, &out.Version, &out.Error, &updatedAt); err != nil {
		return domain.InstanceSoftware{}, err
	}
	ts, err := parseTime(updatedAt)
	if err != nil {
		return domain.InstanceSoftware{}, fmt.Errorf("instance software updated_at: %w", err)
	}
	out.UpdatedAt = ts
	return out, nil
}

func validateInstanceSoftware(row domain.InstanceSoftware) error {
	if row.InstanceID == "" {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "instance_id"}
	}
	if row.OwnerID == "" {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "owner_id"}
	}
	if row.PackageID == "" {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "package_id"}
	}
	switch row.Status {
	case domain.SoftwareAbsent, domain.SoftwarePending, domain.SoftwareInstalled, domain.SoftwareFailed:
	default:
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "status"}
	}
	if row.UpdatedAt.IsZero() {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "updated_at"}
	}
	return nil
}
