// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

func (s *Store) CreateOwner(ctx context.Context, owner domain.Owner) error {
	if owner.ID == "" {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "id"}
	}
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	_, err = s.db.ExecContext(ctx, `INSERT INTO owners(id,name,created_at,updated_at) VALUES(?,?,?,?)`, owner.ID, owner.Name, formatTime(owner.CreatedAt), formatTime(owner.UpdatedAt))
	return mapWriteError(err)
}

// CreateInstance atomically creates the durable operation and its target instance.
// A matching idempotency replay returns the original operation without another write.
func (s *Store) CreateInstance(ctx context.Context, instance domain.Instance, operation domain.Operation) (domain.Operation, bool, error) {
	if err := domain.ValidateInstance(instance); err != nil {
		return domain.Operation{}, false, err
	}
	if err := domain.ValidateOperation(operation); err != nil {
		return domain.Operation{}, false, err
	}
	if operation.OwnerID != instance.OwnerID || operation.TargetType != "instance" || operation.TargetID != string(instance.ID) {
		return domain.Operation{}, false, &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation.target"}
	}
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return domain.Operation{}, false, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Operation{}, false, fmt.Errorf("begin create instance: %w", err)
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
	_, err = tx.ExecContext(ctx, `INSERT INTO instances(
		id,owner_id,name,kind,image_id,requested_isolation,actual_isolation,desired_state,observed_state,
		vcpus,memory_bytes,disk_bytes,expires_at,protected,runtime_ref,error_code,error_stage,error_retryable,created_at,updated_at,deleted_at
	) VALUES(?,?,?,?,NULLIF(?,''),?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		instance.ID, instance.OwnerID, instance.Name, instance.Kind, instance.ImageID,
		instance.RequestedIsolation, instance.ActualIsolation, instance.DesiredState, instance.ObservedState,
		instance.Resources.VCPUs, instance.Resources.MemoryBytes, instance.Resources.DiskBytes,
		nullableTime(instance.ExpiresAt), instance.Protected, instance.RuntimeRef, instance.ErrorCode,
		instance.ErrorStage, instance.ErrorRetryable, formatTime(instance.CreatedAt), formatTime(instance.UpdatedAt), nullableTime(instance.DeletedAt))
	if err != nil {
		return domain.Operation{}, false, mapWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Operation{}, false, fmt.Errorf("commit create instance: %w", err)
	}
	return operation, false, nil
}

func (s *Store) GetInstance(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID) (domain.Instance, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,owner_id,name,kind,COALESCE(image_id,''),requested_isolation,actual_isolation,
		desired_state,observed_state,vcpus,memory_bytes,disk_bytes,expires_at,protected,runtime_ref,error_code,error_stage,
		error_retryable,created_at,updated_at,deleted_at FROM instances WHERE owner_id=? AND id=?`, ownerID, id)
	return scanInstance(row)
}

// UpdateInstanceState validates both lifecycle changes and persists them with an operation atomically.
func (s *Store) UpdateInstanceState(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, desired domain.DesiredState, observed domain.ObservedState, updatedAt time.Time, operation domain.Operation) error {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin state update: %w", err)
	}
	defer tx.Rollback()
	current, err := getInstanceTx(ctx, tx, ownerID, id)
	if err != nil {
		return err
	}
	if err := domain.ValidateDesiredTransition(current, desired); err != nil {
		return err
	}
	if err := domain.ValidateObservedTransition(current.ObservedState, observed); err != nil {
		return err
	}
	if operation.OwnerID != ownerID || operation.TargetType != "instance" || operation.TargetID != string(id) {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation.target"}
	}
	if err := insertOperation(ctx, tx, operation); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE instances SET desired_state=?,observed_state=?,updated_at=? WHERE owner_id=? AND id=?`, desired, observed, formatTime(updatedAt), ownerID, id)
	if err != nil {
		return mapWriteError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit state update: %w", err)
	}
	return nil
}

// TombstoneInstance removes active metadata while retaining the minimal deletion identity.
func (s *Store) TombstoneInstance(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, operation domain.Operation, deletedAt time.Time) error {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tombstone: %w", err)
	}
	defer tx.Rollback()
	i, err := getInstanceTx(ctx, tx, ownerID, id)
	if err != nil {
		return err
	}
	if i.Protected {
		return &domain.Error{Code: domain.CodeProtectedBase, Field: "instance"}
	}
	if i.ObservedState != domain.ObservedDeleted {
		return &domain.Error{Code: domain.CodeInvalidTransition, Field: "observed_state"}
	}
	if operation.OwnerID != ownerID || operation.TargetType != "instance" || operation.TargetID != string(id) {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation.target"}
	}
	if err := insertOperation(ctx, tx, operation); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO instance_tombstones(instance_id,owner_id,name,operation_id,deleted_at) VALUES(?,?,?,?,?)`, id, ownerID, i.Name, operation.ID, formatTime(deletedAt)); err != nil {
		return mapWriteError(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM instances WHERE owner_id=? AND id=?`, ownerID, id); err != nil {
		return mapWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tombstone: %w", err)
	}
	return nil
}

func insertOperation(ctx context.Context, tx *sql.Tx, op domain.Operation) error {
	if err := domain.ValidateOperation(op); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO operations(id,owner_id,type,target_type,target_id,status,stage,progress,error_code,idempotency_key,request_hash,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		op.ID, op.OwnerID, op.Type, op.TargetType, op.TargetID, op.Status, op.Stage, op.Progress, op.ErrorCode, op.IdempotencyKey, op.RequestHash, formatTime(op.CreatedAt), formatTime(op.UpdatedAt))
	return mapWriteError(err)
}

type rowScanner interface{ Scan(...any) error }

func scanInstance(row rowScanner) (domain.Instance, error) {
	var i domain.Instance
	var expires, deleted sql.NullString
	var created, updated string
	err := row.Scan(&i.ID, &i.OwnerID, &i.Name, &i.Kind, &i.ImageID, &i.RequestedIsolation, &i.ActualIsolation,
		&i.DesiredState, &i.ObservedState, &i.Resources.VCPUs, &i.Resources.MemoryBytes, &i.Resources.DiskBytes, &expires,
		&i.Protected, &i.RuntimeRef, &i.ErrorCode, &i.ErrorStage, &i.ErrorRetryable, &created, &updated, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Instance{}, &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	}
	if err != nil {
		return domain.Instance{}, fmt.Errorf("scan instance: %w", err)
	}
	if i.CreatedAt, err = parseTime(created); err != nil {
		return domain.Instance{}, err
	}
	if i.UpdatedAt, err = parseTime(updated); err != nil {
		return domain.Instance{}, err
	}
	if i.ExpiresAt, err = parseNullableTime(expires); err != nil {
		return domain.Instance{}, err
	}
	if i.DeletedAt, err = parseNullableTime(deleted); err != nil {
		return domain.Instance{}, err
	}
	if err := domain.ValidateInstance(i); err != nil {
		return domain.Instance{}, &domain.Error{Code: domain.CodePersistenceCorruption, Field: "instance", Cause: err}
	}
	return i, nil
}

func getInstanceTx(ctx context.Context, tx *sql.Tx, ownerID domain.OwnerID, id domain.InstanceID) (domain.Instance, error) {
	return scanInstance(tx.QueryRowContext(ctx, `SELECT id,owner_id,name,kind,COALESCE(image_id,''),requested_isolation,actual_isolation,desired_state,observed_state,vcpus,memory_bytes,disk_bytes,expires_at,protected,runtime_ref,error_code,error_stage,error_retryable,created_at,updated_at,deleted_at FROM instances WHERE owner_id=? AND id=?`, ownerID, id))
}

func findOperationByIdempotency(ctx context.Context, tx *sql.Tx, ownerID domain.OwnerID, key string) (domain.Operation, bool, error) {
	var op domain.Operation
	var created, updated string
	err := tx.QueryRowContext(ctx, `SELECT id,owner_id,type,target_type,target_id,status,stage,progress,error_code,idempotency_key,request_hash,created_at,updated_at FROM operations WHERE owner_id=? AND idempotency_key=?`, ownerID, key).Scan(
		&op.ID, &op.OwnerID, &op.Type, &op.TargetType, &op.TargetID, &op.Status, &op.Stage, &op.Progress, &op.ErrorCode, &op.IdempotencyKey, &op.RequestHash, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Operation{}, false, nil
	}
	if err != nil {
		return domain.Operation{}, false, fmt.Errorf("find idempotent operation: %w", err)
	}
	op.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Operation{}, false, err
	}
	op.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return domain.Operation{}, false, err
	}
	return op, true, nil
}

func mapWriteError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return &domain.Error{Code: domain.CodeConflict, Field: "unique", Cause: err}
	}
	if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "foreign_key", Cause: err}
	}
	return err
}
