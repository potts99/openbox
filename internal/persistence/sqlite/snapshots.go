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

// CreateSnapshotRecord atomically inserts a snapshot and its durable operation.
func (s *Store) CreateSnapshotRecord(ctx context.Context, snapshot domain.Snapshot, operation domain.Operation) (domain.Operation, bool, error) {
	if snapshot.ID == "" || snapshot.OwnerID == "" || snapshot.InstanceID == "" || snapshot.Name == "" || snapshot.RuntimeRef == "" {
		return domain.Operation{}, false, &domain.Error{Code: domain.CodeInvalidArgument, Field: "snapshot"}
	}
	if err := domain.ValidateOperation(operation); err != nil {
		return domain.Operation{}, false, err
	}
	if operation.OwnerID != snapshot.OwnerID || operation.TargetType != "snapshot" || operation.TargetID != string(snapshot.ID) {
		return domain.Operation{}, false, &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation.target"}
	}
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return domain.Operation{}, false, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Operation{}, false, fmt.Errorf("begin create snapshot: %w", err)
	}
	defer tx.Rollback()
	existing, found, err := findOperationByIdempotency(ctx, tx, operation.OwnerID, operation.IdempotencyKey)
	if err != nil {
		return domain.Operation{}, false, err
	}
	if found {
		if existing.RequestHash != operation.RequestHash {
			return domain.Operation{}, false, &domain.Error{Code: domain.CodeIdempotencyConflict, Field: "idempotency_key"}
		}
		return existing, true, nil
	}
	if err := insertOperation(ctx, tx, operation); err != nil {
		return domain.Operation{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO snapshots(id,owner_id,instance_id,name,runtime_ref,created_at) VALUES(?,?,?,?,?,?)`,
		snapshot.ID, snapshot.OwnerID, snapshot.InstanceID, snapshot.Name, snapshot.RuntimeRef, formatTime(snapshot.CreatedAt))
	if err != nil {
		return domain.Operation{}, false, mapWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Operation{}, false, fmt.Errorf("commit create snapshot: %w", err)
	}
	return operation, false, nil
}

func (s *Store) GetSnapshot(ctx context.Context, ownerID domain.OwnerID, id domain.SnapshotID) (domain.Snapshot, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,owner_id,instance_id,name,runtime_ref,created_at FROM snapshots WHERE owner_id=? AND id=?`, ownerID, id)
	return scanSnapshot(row)
}

func (s *Store) ListSnapshots(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID) ([]domain.Snapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,owner_id,instance_id,name,runtime_ref,created_at FROM snapshots
		WHERE owner_id=? AND instance_id=? ORDER BY created_at, id`, ownerID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Snapshot, 0)
	for rows.Next() {
		snapshot, scanErr := scanSnapshot(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, snapshot)
	}
	return result, rows.Err()
}

func (s *Store) DeleteSnapshotRecord(ctx context.Context, ownerID domain.OwnerID, id domain.SnapshotID) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM snapshots WHERE owner_id=? AND id=?`, ownerID, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return &domain.Error{Code: domain.CodeNotFound, Field: "snapshot"}
	}
	return nil
}

func (s *Store) UpdateSnapshotRuntimeRef(ctx context.Context, ownerID domain.OwnerID, id domain.SnapshotID, runtimeRef string, at time.Time) error {
	if runtimeRef == "" {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "runtime_ref"}
	}
	result, err := s.db.ExecContext(ctx, `UPDATE snapshots SET runtime_ref=? WHERE owner_id=? AND id=?`, runtimeRef, ownerID, id)
	if err != nil {
		return mapWriteError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return &domain.Error{Code: domain.CodeNotFound, Field: "snapshot"}
	}
	_ = at
	return nil
}

// CreateDeleteOperation records a durable delete without mutating the target row yet.
func (s *Store) CreateDeleteOperation(ctx context.Context, operation domain.Operation) (domain.Operation, bool, error) {
	if err := domain.ValidateOperation(operation); err != nil {
		return domain.Operation{}, false, err
	}
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return domain.Operation{}, false, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Operation{}, false, fmt.Errorf("begin create delete operation: %w", err)
	}
	defer tx.Rollback()
	existing, found, err := findOperationByIdempotency(ctx, tx, operation.OwnerID, operation.IdempotencyKey)
	if err != nil {
		return domain.Operation{}, false, err
	}
	if found {
		if existing.RequestHash != operation.RequestHash {
			return domain.Operation{}, false, &domain.Error{Code: domain.CodeIdempotencyConflict, Field: "idempotency_key"}
		}
		return existing, true, nil
	}
	if err := insertOperation(ctx, tx, operation); err != nil {
		return domain.Operation{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Operation{}, false, fmt.Errorf("commit create delete operation: %w", err)
	}
	return operation, false, nil
}

func scanSnapshot(row rowScanner) (domain.Snapshot, error) {
	var snapshot domain.Snapshot
	var created string
	err := row.Scan(&snapshot.ID, &snapshot.OwnerID, &snapshot.InstanceID, &snapshot.Name, &snapshot.RuntimeRef, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Snapshot{}, &domain.Error{Code: domain.CodeNotFound, Field: "snapshot"}
	}
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("scan snapshot: %w", err)
	}
	if snapshot.CreatedAt, err = parseTime(created); err != nil {
		return domain.Snapshot{}, err
	}
	return snapshot, nil
}
