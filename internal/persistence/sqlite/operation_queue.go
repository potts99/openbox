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

func (s *Store) ListClaimableOperations(ctx context.Context, now time.Time, limit int) ([]domain.Operation, error) {
	if limit <= 0 {
		return nil, &domain.Error{Code: domain.CodeInvalidArgument, Field: "limit"}
	}
	rows, err := s.db.QueryContext(ctx, operationColumns+` WHERE status IN (?,?) AND (next_attempt_at IS NULL OR next_attempt_at<=?) AND (claimed_by='' OR claim_expires_at IS NULL OR claim_expires_at<=?) ORDER BY created_at,id LIMIT ?`,
		domain.OperationPending, domain.OperationRunning, formatTime(now), formatTime(now), limit)
	if err != nil {
		return nil, fmt.Errorf("list claimable operations: %w", err)
	}
	defer rows.Close()
	var result []domain.Operation
	for rows.Next() {
		op, scanErr := scanOperation(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan claimable operation: %w", scanErr)
		}
		result = append(result, op)
	}
	return result, rows.Err()
}

func (s *Store) ClaimOperation(ctx context.Context, id domain.OperationID, worker, token string, now time.Time, lease time.Duration) (domain.Operation, bool, bool, error) {
	if id == "" || worker == "" || token == "" || lease <= 0 {
		return domain.Operation{}, false, false, &domain.Error{Code: domain.CodeInvalidArgument, Field: "claim"}
	}
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return domain.Operation{}, false, false, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Operation{}, false, false, fmt.Errorf("begin operation claim: %w", err)
	}
	defer tx.Rollback()
	op, err := scanOperation(tx.QueryRowContext(ctx, operationColumns+` WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Operation{}, false, false, nil
	}
	if err != nil {
		return domain.Operation{}, false, false, err
	}
	if op.Status != domain.OperationPending && op.Status != domain.OperationRunning {
		return op, false, false, nil
	}
	if op.NextAttemptAt != nil && op.NextAttemptAt.After(now) {
		return op, false, false, nil
	}
	if op.ClaimedBy != "" && op.ClaimExpiresAt != nil && op.ClaimExpiresAt.After(now) {
		return op, false, false, nil
	}
	abandoned := op.Status == domain.OperationRunning || op.ClaimedBy != ""
	expires := now.Add(lease)
	result, err := tx.ExecContext(ctx, `UPDATE operations SET status=?,claimed_by=?,claim_token=?,claim_expires_at=?,attempts=attempts+1,updated_at=? WHERE id=? AND status IN (?,?) AND (claimed_by='' OR claim_expires_at IS NULL OR claim_expires_at<=?)`, domain.OperationRunning, worker, token, formatTime(expires), formatTime(now), id, domain.OperationPending, domain.OperationRunning, formatTime(now))
	if err != nil {
		return domain.Operation{}, false, false, mapWriteError(err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return op, false, false, nil
	}
	stage := "claimed"
	if abandoned {
		stage = "recovered_abandoned"
	}
	if err := appendOperationEventTx(ctx, tx, op.OwnerID, op.ID, stage, domain.OperationRunning, "", "", "", nil, now); err != nil {
		return domain.Operation{}, false, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Operation{}, false, false, fmt.Errorf("commit operation claim: %w", err)
	}
	op.Status = domain.OperationRunning
	op.ClaimedBy = worker
	op.ClaimToken = token
	op.ClaimExpiresAt = &expires
	op.Attempts++
	op.UpdatedAt = now.UTC()
	return op, true, abandoned, nil
}

func (s *Store) RenewClaim(ctx context.Context, id domain.OperationID, worker, token string, now time.Time, lease time.Duration) (bool, error) {
	if id == "" || worker == "" || token == "" || lease <= 0 {
		return false, &domain.Error{Code: domain.CodeInvalidArgument, Field: "claim"}
	}
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	result, err := s.db.ExecContext(ctx, `UPDATE operations SET claim_expires_at=?,updated_at=? WHERE id=? AND status=? AND claimed_by=? AND claim_token=?`, formatTime(now.Add(lease)), formatTime(now), id, domain.OperationRunning, worker, token)
	if err != nil {
		return false, mapWriteError(err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *Store) RetryOperation(ctx context.Context, ownerID domain.OwnerID, id domain.OperationID, worker, token, class string, code domain.ErrorCode, message string, next, timeNow time.Time) error {
	return s.finishAttempt(ctx, ownerID, id, worker, token, domain.OperationPending, "retry_scheduled", class, code, message, &next, timeNow)
}

func (s *Store) FailOperation(ctx context.Context, ownerID domain.OwnerID, id domain.OperationID, worker, token, class string, code domain.ErrorCode, message string, now time.Time) error {
	return s.finishAttempt(ctx, ownerID, id, worker, token, domain.OperationFailed, "failed", class, code, message, nil, now)
}

// CompleteClaim completes an operation only for the current fencing token. A
// lifecycle executor may have atomically completed it already (notably delete);
// that state is accepted without allowing an old claim to overwrite a new one.
func (s *Store) CompleteClaim(ctx context.Context, ownerID domain.OwnerID, id domain.OperationID, worker, token string, now time.Time) (bool, error) {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var status domain.OperationStatus
	var claimedBy, claimToken string
	if err := tx.QueryRowContext(ctx, `SELECT status,claimed_by,claim_token FROM operations WHERE owner_id=? AND id=?`, ownerID, id).Scan(&status, &claimedBy, &claimToken); err != nil {
		return false, err
	}
	if status == domain.OperationSucceeded {
		return false, nil
	}
	if status != domain.OperationRunning || claimedBy != worker || claimToken != token {
		return false, &domain.Error{Code: domain.CodeConflict, Field: "operation.claim"}
	}
	result, err := tx.ExecContext(ctx, `UPDATE operations SET status=?,stage='complete',progress=100,error_code='',next_attempt_at=NULL,claimed_by='',claim_token='',claim_expires_at=NULL,error_class='',updated_at=? WHERE owner_id=? AND id=? AND status=? AND claimed_by=? AND claim_token=?`, domain.OperationSucceeded, formatTime(now), ownerID, id, domain.OperationRunning, worker, token)
	if err != nil {
		return false, mapWriteError(err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return false, &domain.Error{Code: domain.CodeConflict, Field: "operation.claim"}
	}
	if err := appendOperationEventTx(ctx, tx, ownerID, id, "complete", domain.OperationSucceeded, "", "", "", nil, now); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) finishAttempt(ctx context.Context, ownerID domain.OwnerID, id domain.OperationID, worker, token string, status domain.OperationStatus, stage, class string, code domain.ErrorCode, message string, next *time.Time, now time.Time) error {
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
	var nextValue any
	if next != nil {
		nextValue = formatTime(*next)
	}
	result, err := tx.ExecContext(ctx, `UPDATE operations SET status=?,stage=?,error_class=?,error_code=?,next_attempt_at=?,claimed_by='',claim_token='',claim_expires_at=NULL,updated_at=? WHERE owner_id=? AND id=? AND claimed_by=? AND claim_token=?`, status, stage, class, code, nextValue, formatTime(now), ownerID, id, worker, token)
	if err != nil {
		return mapWriteError(err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return &domain.Error{Code: domain.CodeConflict, Field: "operation.claim"}
	}
	if err := appendOperationEventTx(ctx, tx, ownerID, id, stage, status, class, code, message, nil, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListOperationEvents(ctx context.Context, ownerID domain.OwnerID, id domain.OperationID) ([]domain.OperationEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,owner_id,operation_id,sequence,stage,status,error_class,error_code,message,metadata_json,created_at FROM operation_events WHERE owner_id=? AND operation_id=? ORDER BY sequence`, ownerID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.OperationEvent
	for rows.Next() {
		var event domain.OperationEvent
		var created string
		if err := rows.Scan(&event.ID, &event.OwnerID, &event.OperationID, &event.Sequence, &event.Stage, &event.Status, &event.ErrorClass, &event.ErrorCode, &event.Message, &event.MetadataJSON, &created); err != nil {
			return nil, err
		}
		event.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func appendOperationEventTx(ctx context.Context, tx *sql.Tx, ownerID domain.OwnerID, id domain.OperationID, stage string, status domain.OperationStatus, class string, code domain.ErrorCode, message string, metadata []byte, now time.Time) error {
	if len(metadata) == 0 {
		metadata = []byte("{}")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO operation_events(owner_id,operation_id,sequence,stage,status,error_class,error_code,message,metadata_json,created_at) SELECT ?,?,COALESCE(MAX(sequence),0)+1,?,?,?,?,?,?,? FROM operation_events WHERE operation_id=?`, ownerID, id, stage, status, class, code, message, metadata, formatTime(now), id)
	return mapWriteError(err)
}
