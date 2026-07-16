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
	"github.com/openbox-dev/openbox/internal/operations"
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

// EnsureOwner idempotently bootstraps the single configured local owner. A
// conflicting record is never overwritten.
func (s *Store) EnsureOwner(ctx context.Context, owner domain.Owner) error {
	if owner.ID == "" || owner.Name == "" {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "owner"}
	}
	err := s.CreateOwner(ctx, owner)
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeConflict {
		return err
	}
	var name string
	if scanErr := s.db.QueryRowContext(ctx, `SELECT name FROM owners WHERE id=?`, owner.ID).Scan(&name); scanErr != nil {
		return scanErr
	}
	if name != owner.Name {
		return &domain.Error{Code: domain.CodeConflict, Field: "owner", Cause: errors.New("configured owner identity differs")}
	}
	return nil
}

// EnsureImage records the immutable fingerprint selected for a runtime alias.
func (s *Store) EnsureImage(ctx context.Context, image domain.Image) error {
	if image.ID == "" || image.OwnerID == "" || image.Alias == "" || image.Digest == "" {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "image"}
	}
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	_, err = s.db.ExecContext(ctx, `INSERT INTO images(id,owner_id,alias,source,digest,architecture,compatibility,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, image.ID, image.OwnerID, image.Alias, image.Source,
		image.Digest, image.Architecture, image.Compatibility, formatTime(image.CreatedAt), formatTime(image.UpdatedAt))
	if err != nil {
		return mapWriteError(err)
	}
	var ownerID, alias, digest string
	if err := s.db.QueryRowContext(ctx, `SELECT owner_id,alias,digest FROM images WHERE id=?`, image.ID).Scan(&ownerID, &alias, &digest); err != nil {
		return fmt.Errorf("read ensured image: %w", err)
	}
	if ownerID != string(image.OwnerID) || alias != image.Alias || digest != image.Digest {
		return &domain.Error{Code: domain.CodeConflict, Field: "image", Cause: errors.New("immutable image record differs")}
	}
	return nil
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
	if instance.EgressMode == "" {
		if instance.Kind == domain.KindSandbox {
			instance.EgressMode = domain.EgressRestricted
		} else {
			instance.EgressMode = domain.EgressStandard
		}
	}
	if instance.EgressProfileID == "" {
		instance.EgressProfileID = domain.DefaultEgressProfileID(instance.Kind)
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
		vcpus,memory_bytes,disk_bytes,expires_at,protected,runtime_ref,egress_mode,egress_profile_id,
		clone_source_instance_id,clone_source_snapshot_id,clone_source_image_id,
		error_code,error_stage,error_retryable,created_at,updated_at,deleted_at
	) VALUES(?,?,?,?,NULLIF(?,''),?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		instance.ID, instance.OwnerID, instance.Name, instance.Kind, instance.ImageID,
		instance.RequestedIsolation, instance.ActualIsolation, instance.DesiredState, instance.ObservedState,
		instance.Resources.VCPUs, instance.Resources.MemoryBytes, instance.Resources.DiskBytes,
		nullableTime(instance.ExpiresAt), instance.Protected, instance.RuntimeRef,
		instance.EgressMode, instance.EgressProfileID,
		instance.CloneSourceInstanceID, instance.CloneSourceSnapshotID, instance.CloneSourceImageID,
		instance.ErrorCode, instance.ErrorStage, instance.ErrorRetryable, formatTime(instance.CreatedAt), formatTime(instance.UpdatedAt), nullableTime(instance.DeletedAt))
	if err != nil {
		return domain.Operation{}, false, mapWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Operation{}, false, fmt.Errorf("commit create instance: %w", err)
	}
	return operation, false, nil
}

const instanceColumns = `id,owner_id,name,kind,COALESCE(image_id,''),requested_isolation,actual_isolation,
		desired_state,observed_state,vcpus,memory_bytes,disk_bytes,expires_at,protected,runtime_ref,
		egress_mode,COALESCE(egress_profile_id,''),
		COALESCE(clone_source_instance_id,''),COALESCE(clone_source_snapshot_id,''),COALESCE(clone_source_image_id,''),
		error_code,error_stage,error_retryable,created_at,updated_at,deleted_at`

func (s *Store) GetInstance(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID) (domain.Instance, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+instanceColumns+` FROM instances WHERE owner_id=? AND id=?`, ownerID, id)
	return scanInstance(row)
}

// ListInstances returns all non-tombstoned durable instances for reconciliation.
func (s *Store) ListInstances(ctx context.Context) ([]domain.Instance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+instanceColumns+` FROM instances WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Instance, 0)
	for rows.Next() {
		instance, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, instance)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}
	return result, nil
}

// ListInstancesByOwner provides the bounded owner-scoped read model used by
// transport adapters. Reconciliation intentionally uses ListInstances instead.
func (s *Store) ListInstancesByOwner(ctx context.Context, ownerID domain.OwnerID, limit int) ([]domain.Instance, error) {
	if ownerID == "" || limit <= 0 || limit > 1000 {
		return nil, &domain.Error{Code: domain.CodeInvalidArgument, Field: "limit"}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+instanceColumns+` FROM instances WHERE owner_id=? AND deleted_at IS NULL ORDER BY id LIMIT ?`, ownerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list owner instances: %w", err)
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

func (s *Store) ListImagesByOwner(ctx context.Context, ownerID domain.OwnerID, limit int) ([]domain.Image, error) {
	if ownerID == "" || limit <= 0 || limit > 1000 {
		return nil, &domain.Error{Code: domain.CodeInvalidArgument, Field: "limit"}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,owner_id,alias,source,digest,architecture,compatibility,created_at,updated_at FROM images WHERE owner_id=? ORDER BY id LIMIT ?`, ownerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Image, 0)
	for rows.Next() {
		var image domain.Image
		var created, updated string
		if err := rows.Scan(&image.ID, &image.OwnerID, &image.Alias, &image.Source, &image.Digest, &image.Architecture, &image.Compatibility, &created, &updated); err != nil {
			return nil, err
		}
		image.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		image.UpdatedAt, err = parseTime(updated)
		if err != nil {
			return nil, err
		}
		result = append(result, image)
	}
	return result, rows.Err()
}

func (s *Store) GetOperationByIdempotency(ctx context.Context, ownerID domain.OwnerID, key string) (domain.Operation, bool, error) {
	op, err := scanOperation(s.db.QueryRowContext(ctx, operationColumns+` WHERE owner_id=? AND idempotency_key=?`, ownerID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Operation{}, false, nil
	}
	if err != nil {
		return domain.Operation{}, false, fmt.Errorf("find idempotent operation: %w", err)
	}
	return op, true, nil
}

func (s *Store) GetOperation(ctx context.Context, ownerID domain.OwnerID, id domain.OperationID) (domain.Operation, error) {
	op, err := scanOperation(s.db.QueryRowContext(ctx, operationColumns+` WHERE owner_id=? AND id=?`, ownerID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Operation{}, &domain.Error{Code: domain.CodeNotFound, Field: "operation"}
	}
	return op, err
}

func (s *Store) ListOperations(ctx context.Context, ownerID domain.OwnerID, limit int) ([]domain.Operation, error) {
	if ownerID == "" || limit <= 0 || limit > 1000 {
		return nil, &domain.Error{Code: domain.CodeInvalidArgument, Field: "limit"}
	}
	rows, err := s.db.QueryContext(ctx, operationColumns+` WHERE owner_id=? ORDER BY created_at DESC,id DESC LIMIT ?`, ownerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Operation, 0)
	for rows.Next() {
		op, scanErr := scanOperation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, op)
	}
	return result, rows.Err()
}

func (s *Store) CompleteOperation(ctx context.Context, ownerID domain.OwnerID, id domain.OperationID, completedAt time.Time) error {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := enforceClaimTx(ctx, tx, ownerID, "", id, completedAt); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE operations SET status=?,stage='complete',progress=100,error_code='',next_attempt_at=NULL,claimed_by='',claim_token='',claim_expires_at=NULL,error_class='',updated_at=? WHERE owner_id=? AND id=? AND status IN (?,?)`,
		domain.OperationSucceeded, formatTime(completedAt), ownerID, id, domain.OperationPending, domain.OperationRunning)
	if err != nil {
		return mapWriteError(err)
	}
	if count, _ := result.RowsAffected(); count == 1 {
		if err := appendOperationEventTx(ctx, tx, ownerID, id, "complete", domain.OperationSucceeded, "", "", "", nil, completedAt); err != nil {
			return err
		}
		return tx.Commit()
	}
	var status domain.OperationStatus
	if err := tx.QueryRowContext(ctx, `SELECT status FROM operations WHERE owner_id=? AND id=?`, ownerID, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &domain.Error{Code: domain.CodeNotFound, Field: "operation"}
		}
		return fmt.Errorf("read completed operation: %w", err)
	}
	if status == domain.OperationSucceeded {
		return nil
	}
	return &domain.Error{Code: domain.CodeConflict, Field: "operation.status"}
}

func (s *Store) UpdateOperationStage(ctx context.Context, ownerID domain.OwnerID, id domain.OperationID, stage string, progress int, updatedAt time.Time) error {
	if stage == "" || progress < 0 || progress > 99 {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation.stage"}
	}
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := enforceClaimTx(ctx, tx, ownerID, "", id, updatedAt); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE operations SET status=?,stage=?,progress=?,updated_at=? WHERE owner_id=? AND id=? AND status IN (?,?)`,
		domain.OperationRunning, stage, progress, formatTime(updatedAt), ownerID, id, domain.OperationPending, domain.OperationRunning)
	if err != nil {
		return mapWriteError(err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return &domain.Error{Code: domain.CodeConflict, Field: "operation.status"}
	}
	if err := appendOperationEventTx(ctx, tx, ownerID, id, stage, domain.OperationRunning, "", "", "", nil, updatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateInstanceObservation persists runtime facts while preventing runtime
// identity changes and invalid lifecycle transitions.
func (s *Store) UpdateInstanceObservation(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, runtimeRef string, actual domain.IsolationType, observed domain.ObservedState, errorCode domain.ErrorCode, updatedAt time.Time) error {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin observation update: %w", err)
	}
	defer tx.Rollback()
	if err := enforceClaimTx(ctx, tx, ownerID, string(id), "", updatedAt); err != nil {
		return err
	}
	current, err := getInstanceTx(ctx, tx, ownerID, id)
	if err != nil {
		return err
	}
	if current.RuntimeRef != runtimeRef {
		return &domain.Error{Code: domain.CodeConflict, Field: "runtime_ref"}
	}
	if actual != domain.IsolationContainer && actual != domain.IsolationVM {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "actual_isolation"}
	}
	if current.ActualIsolation != actual {
		return &domain.Error{Code: domain.CodeConflict, Field: "actual_isolation"}
	}
	if err := domain.ValidateObservedTransition(current.ObservedState, observed); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE instances SET actual_isolation=?,observed_state=?,error_code=?,updated_at=? WHERE owner_id=? AND id=? AND runtime_ref=?`,
		actual, observed, errorCode, formatTime(updatedAt), ownerID, id, runtimeRef)
	if err != nil {
		return mapWriteError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return &domain.Error{Code: domain.CodeConflict, Field: "runtime_ref"}
	}
	return tx.Commit()
}

func (s *Store) IsInstanceTombstoned(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM instance_tombstones WHERE owner_id=? AND instance_id=?`, ownerID, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read instance tombstone: %w", err)
	}
	return true, nil
}

// FinalizeInstanceDeletion references the already-created delete operation and
// removes active metadata only after the application verified runtime removal.
func (s *Store) FinalizeInstanceDeletion(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, operationID domain.OperationID, deletedAt time.Time) error {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin deletion finalization: %w", err)
	}
	defer tx.Rollback()
	if err := enforceClaimTx(ctx, tx, ownerID, string(id), operationID, deletedAt); err != nil {
		return err
	}
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
	var operationOwner, targetType, targetID string
	if err := tx.QueryRowContext(ctx, `SELECT owner_id,target_type,target_id FROM operations WHERE id=?`, operationID).Scan(&operationOwner, &targetType, &targetID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &domain.Error{Code: domain.CodeNotFound, Field: "operation"}
		}
		return fmt.Errorf("read delete operation: %w", err)
	}
	if operationOwner != string(ownerID) || targetType != "instance" || targetID != string(id) {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation.target"}
	}
	result, err := tx.ExecContext(ctx, `UPDATE operations SET status=?,stage='complete',progress=100,error_code='',next_attempt_at=NULL,claimed_by='',claim_token='',claim_expires_at=NULL,error_class='',updated_at=? WHERE owner_id=? AND id=? AND status IN (?,?)`,
		domain.OperationSucceeded, formatTime(deletedAt), ownerID, operationID, domain.OperationPending, domain.OperationRunning)
	if err != nil {
		return mapWriteError(err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return &domain.Error{Code: domain.CodeConflict, Field: "operation.status"}
	}
	if err := appendOperationEventTx(ctx, tx, ownerID, operationID, "complete", domain.OperationSucceeded, "", "", "", nil, deletedAt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO instance_tombstones(instance_id,owner_id,name,operation_id,deleted_at) VALUES(?,?,?,?,?)`, id, ownerID, i.Name, operationID, formatTime(deletedAt)); err != nil {
		return mapWriteError(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM instances WHERE owner_id=? AND id=?`, ownerID, id); err != nil {
		return mapWriteError(err)
	}
	return tx.Commit()
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
	if err := enforceClaimTx(ctx, tx, ownerID, string(id), operation.ID, updatedAt); err != nil {
		return err
	}
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

// UpdateInstanceProtection sets or clears the protected-base flag.
func (s *Store) UpdateInstanceProtection(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, protected bool, updatedAt time.Time) error {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	result, err := s.db.ExecContext(ctx, `UPDATE instances SET protected=?,updated_at=? WHERE owner_id=? AND id=? AND deleted_at IS NULL`,
		protected, formatTime(updatedAt), ownerID, id)
	if err != nil {
		return mapWriteError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	}
	return nil
}

// UpdateInstanceExpiry atomically sets expires_at when the instance is not on
// an irreversible delete path. Callers must pass an already-validated expiry.
func (s *Store) UpdateInstanceExpiry(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, expiresAt, updatedAt time.Time) error {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin expiry update: %w", err)
	}
	defer tx.Rollback()
	current, err := getInstanceTx(ctx, tx, ownerID, id)
	if err != nil {
		return err
	}
	if current.DesiredState == domain.DesiredDeleted || current.ObservedState == domain.ObservedDeleting || current.ObservedState == domain.ObservedDeleted {
		return &domain.Error{Code: domain.CodeInvalidTransition, Field: "expires_at"}
	}
	current.ExpiresAt = &expiresAt
	current.UpdatedAt = updatedAt.UTC()
	if err := domain.ValidateInstance(current); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE instances SET expires_at=?,updated_at=? WHERE owner_id=? AND id=? AND deleted_at IS NULL AND desired_state!=? AND observed_state NOT IN (?,?)`,
		formatTime(expiresAt), formatTime(updatedAt), ownerID, id, domain.DesiredDeleted, domain.ObservedDeleting, domain.ObservedDeleted)
	if err != nil {
		return mapWriteError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return &domain.Error{Code: domain.CodeInvalidTransition, Field: "expires_at"}
	}
	return tx.Commit()
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
	payload := op.PayloadJSON
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO operations(id,owner_id,type,target_type,target_id,status,stage,progress,error_code,idempotency_key,request_hash,payload_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		op.ID, op.OwnerID, op.Type, op.TargetType, op.TargetID, op.Status, op.Stage, op.Progress, op.ErrorCode, op.IdempotencyKey, op.RequestHash, payload, formatTime(op.CreatedAt), formatTime(op.UpdatedAt))
	return mapWriteError(err)
}

type rowScanner interface{ Scan(...any) error }

func scanInstance(row rowScanner) (domain.Instance, error) {
	var i domain.Instance
	var expires, deleted sql.NullString
	var created, updated string
	err := row.Scan(&i.ID, &i.OwnerID, &i.Name, &i.Kind, &i.ImageID, &i.RequestedIsolation, &i.ActualIsolation,
		&i.DesiredState, &i.ObservedState, &i.Resources.VCPUs, &i.Resources.MemoryBytes, &i.Resources.DiskBytes, &expires,
		&i.Protected, &i.RuntimeRef, &i.EgressMode, &i.EgressProfileID, &i.CloneSourceInstanceID, &i.CloneSourceSnapshotID, &i.CloneSourceImageID,
		&i.ErrorCode, &i.ErrorStage, &i.ErrorRetryable, &created, &updated, &deleted)
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
	return scanInstance(tx.QueryRowContext(ctx, `SELECT `+instanceColumns+` FROM instances WHERE owner_id=? AND id=?`, ownerID, id))
}

func findOperationByIdempotency(ctx context.Context, tx *sql.Tx, ownerID domain.OwnerID, key string) (domain.Operation, bool, error) {
	op, err := scanOperation(tx.QueryRowContext(ctx, operationColumns+` WHERE owner_id=? AND idempotency_key=?`, ownerID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Operation{}, false, nil
	}
	if err != nil {
		return domain.Operation{}, false, fmt.Errorf("find idempotent operation: %w", err)
	}
	return op, true, nil
}

const operationColumns = `SELECT id,owner_id,type,target_type,target_id,status,stage,progress,error_code,idempotency_key,request_hash,payload_json,attempts,next_attempt_at,claimed_by,claim_token,claim_expires_at,error_class,created_at,updated_at FROM operations`

func scanOperation(row rowScanner) (domain.Operation, error) {
	var op domain.Operation
	var nextAttempt, claimExpires sql.NullString
	var created, updated string
	err := row.Scan(&op.ID, &op.OwnerID, &op.Type, &op.TargetType, &op.TargetID, &op.Status, &op.Stage, &op.Progress, &op.ErrorCode, &op.IdempotencyKey, &op.RequestHash, &op.PayloadJSON, &op.Attempts, &nextAttempt, &op.ClaimedBy, &op.ClaimToken, &claimExpires, &op.ErrorClass, &created, &updated)
	if err != nil {
		return domain.Operation{}, err
	}
	if op.CreatedAt, err = parseTime(created); err != nil {
		return domain.Operation{}, err
	}
	if op.UpdatedAt, err = parseTime(updated); err != nil {
		return domain.Operation{}, err
	}
	if op.NextAttemptAt, err = parseNullableTime(nextAttempt); err != nil {
		return domain.Operation{}, err
	}
	if op.ClaimExpiresAt, err = parseNullableTime(claimExpires); err != nil {
		return domain.Operation{}, err
	}
	return op, nil
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

func enforceClaimTx(ctx context.Context, tx *sql.Tx, ownerID domain.OwnerID, targetID string, operationID domain.OperationID, now time.Time) error {
	claim, fenced := operations.ClaimFromContext(ctx)
	if !fenced {
		return nil
	}
	if claim.OwnerID != ownerID || (operationID != "" && claim.OperationID != operationID) {
		return &domain.Error{Code: domain.CodeConflict, Field: "operation.claim"}
	}
	var worker, token, storedTarget string
	var status domain.OperationStatus
	var expiresRaw sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT claimed_by,claim_token,status,target_id,claim_expires_at FROM operations WHERE owner_id=? AND id=?`, claim.OwnerID, claim.OperationID).Scan(&worker, &token, &status, &storedTarget, &expiresRaw)
	if err != nil {
		return err
	}
	if worker != claim.WorkerID || token != claim.Token || status != domain.OperationRunning || (targetID != "" && storedTarget != targetID) {
		return &domain.Error{Code: domain.CodeConflict, Field: "operation.claim"}
	}
	if !expiresRaw.Valid || expiresRaw.String == "" {
		return &domain.Error{Code: domain.CodeConflict, Field: "operation.claim"}
	}
	expires, err := parseTime(expiresRaw.String)
	if err != nil {
		return err
	}
	if !expires.After(now) {
		return &domain.Error{Code: domain.CodeConflict, Field: "operation.claim"}
	}
	return nil
}
