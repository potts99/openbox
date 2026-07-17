// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
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
	var claimedBy, claimToken, operationType, targetID string
	if err := tx.QueryRowContext(ctx, `SELECT status,claimed_by,claim_token,type,target_id FROM operations WHERE owner_id=? AND id=?`, ownerID, id).Scan(&status, &claimedBy, &claimToken, &operationType, &targetID); err != nil {
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
	var metadata []byte
	if operationType == "image.build" {
		var alias, digest, architecture, runtime string
		if err := tx.QueryRowContext(ctx, `SELECT alias,digest,architecture,runtime FROM image_builds WHERE owner_id=? AND id=?`, ownerID, targetID).
			Scan(&alias, &digest, &architecture, &runtime); err != nil {
			return false, err
		}
		if digest == "" {
			return false, &domain.Error{Code: domain.CodeConflict, Field: "image_build.digest"}
		}
		metadata, err = json.Marshal(map[string]string{"alias": alias, "digest": digest, "architecture": architecture, "runtime": runtime})
		if err != nil {
			return false, err
		}
	}
	if err := appendOperationEventTx(ctx, tx, ownerID, id, "complete", domain.OperationSucceeded, "", "", "", metadata, now); err != nil {
		return false, err
	}
	if err := enqueueOperationTerminalTx(ctx, tx, ownerID, id, now); err != nil {
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
	if status == domain.OperationFailed {
		if err := enqueueOperationTerminalTx(ctx, tx, ownerID, id, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListOperationEvents(ctx context.Context, ownerID domain.OwnerID, id domain.OperationID) ([]domain.OperationEvent, error) {
	return s.ListOperationEventsAfter(ctx, ownerID, id, 0, 1000)
}

func (s *Store) ListOperationEventsAfter(ctx context.Context, ownerID domain.OwnerID, id domain.OperationID, after, limit int) ([]domain.OperationEvent, error) {
	if ownerID == "" || id == "" || after < 0 || limit <= 0 || limit > 1000 {
		return nil, &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation_events"}
	}
	if _, err := s.GetOperation(ctx, ownerID, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,owner_id,operation_id,sequence,stage,status,progress,error_class,error_code,message,metadata_json,created_at FROM operation_events WHERE owner_id=? AND operation_id=? AND sequence>? ORDER BY sequence LIMIT ?`, ownerID, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.OperationEvent
	for rows.Next() {
		var event domain.OperationEvent
		var created string
		if err := rows.Scan(&event.ID, &event.OwnerID, &event.OperationID, &event.Sequence, &event.Stage, &event.Status, &event.Progress, &event.ErrorClass, &event.ErrorCode, &event.Message, &event.MetadataJSON, &created); err != nil {
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

// CancelPendingOperation is intentionally narrow: only an unclaimed operation
// at the initial runtime stage is cancellable. The status transition and target
// rollback happen in one transaction, so claim and cancellation races have one
// winner and can never cancel an external action already in flight.
func (s *Store) CancelPendingOperation(ctx context.Context, ownerID domain.OwnerID, id domain.OperationID, now time.Time) (domain.Operation, error) {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Operation{}, err
	}
	defer tx.Rollback()
	op, err := scanOperation(tx.QueryRowContext(ctx, operationColumns+` WHERE owner_id=? AND id=?`, ownerID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Operation{}, &domain.Error{Code: domain.CodeNotFound, Field: "operation"}
	}
	if err != nil {
		return domain.Operation{}, err
	}
	if op.Status == domain.OperationFailed && op.Stage == "canceled" && op.ErrorCode == domain.CodeOperationCanceled {
		return op, nil
	}
	if op.Status != domain.OperationPending || op.Stage != "runtime" || op.ClaimedBy != "" || op.ClaimToken != "" {
		return domain.Operation{}, &domain.Error{Code: domain.CodeCancellationUnsafe, Field: "operation.stage"}
	}
	var newer int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM operations newer
		WHERE newer.owner_id=? AND newer.target_type=? AND newer.target_id=?
		AND newer.rowid > (SELECT current.rowid FROM operations current WHERE current.owner_id=? AND current.id=?))`,
		ownerID, op.TargetType, op.TargetID, ownerID, id).Scan(&newer); err != nil {
		return domain.Operation{}, err
	}
	if newer != 0 {
		return domain.Operation{}, &domain.Error{Code: domain.CodeCancellationUnsafe, Field: "operation.order"}
	}
	if op.Type == "image.build" {
		// Image builds have no mutable target until the worker leaves runtime.
		// The operation transition below is therefore the complete rollback.
	} else if op.Type == "instance.create" {
		result, deleteErr := tx.ExecContext(ctx, `DELETE FROM instances WHERE owner_id=? AND id=? AND observed_state=?`, ownerID, op.TargetID, domain.ObservedPending)
		if deleteErr != nil {
			return domain.Operation{}, mapWriteError(deleteErr)
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return domain.Operation{}, &domain.Error{Code: domain.CodeCancellationUnsafe, Field: "operation.target"}
		}
	} else {
		var payload struct {
			PreviousDesired  domain.DesiredState  `json:"previous_desired_state"`
			PreviousObserved domain.ObservedState `json:"previous_observed_state"`
			IntendedDesired  domain.DesiredState  `json:"intended_desired_state"`
			IntendedObserved domain.ObservedState `json:"intended_observed_state"`
		}
		if err := json.Unmarshal(op.PayloadJSON, &payload); err != nil || payload.PreviousDesired == "" || payload.PreviousObserved == "" || payload.IntendedDesired == "" || payload.IntendedObserved == "" {
			return domain.Operation{}, &domain.Error{Code: domain.CodeCancellationUnsafe, Field: "operation.payload", Cause: err}
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE instances SET desired_state=?,observed_state=?,updated_at=? WHERE owner_id=? AND id=? AND desired_state=? AND observed_state=?`, payload.PreviousDesired, payload.PreviousObserved, formatTime(now), ownerID, op.TargetID, payload.IntendedDesired, payload.IntendedObserved)
		if updateErr != nil {
			return domain.Operation{}, mapWriteError(updateErr)
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return domain.Operation{}, &domain.Error{Code: domain.CodeCancellationUnsafe, Field: "operation.target"}
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE operations SET status=?,stage='canceled',error_code=?,error_class='correctable',next_attempt_at=NULL,claimed_by='',claim_token='',claim_expires_at=NULL,updated_at=? WHERE owner_id=? AND id=? AND status=? AND stage='runtime' AND claimed_by='' AND claim_token=''`, domain.OperationFailed, domain.CodeOperationCanceled, formatTime(now), ownerID, id, domain.OperationPending)
	if err != nil {
		return domain.Operation{}, mapWriteError(err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return domain.Operation{}, &domain.Error{Code: domain.CodeCancellationUnsafe, Field: "operation.stage"}
	}
	if err := appendOperationEventTx(ctx, tx, ownerID, id, "canceled", domain.OperationFailed, "correctable", domain.CodeOperationCanceled, "operation canceled before runtime mutation", nil, now); err != nil {
		return domain.Operation{}, err
	}
	if err := enqueueOperationTerminalTx(ctx, tx, ownerID, id, now); err != nil {
		return domain.Operation{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Operation{}, err
	}
	op.Status = domain.OperationFailed
	op.Stage = "canceled"
	op.ErrorCode = domain.CodeOperationCanceled
	op.ErrorClass = "correctable"
	op.UpdatedAt = now.UTC()
	return op, nil
}

func appendOperationEventTx(ctx context.Context, tx *sql.Tx, ownerID domain.OwnerID, id domain.OperationID, stage string, status domain.OperationStatus, class string, code domain.ErrorCode, message string, metadata []byte, now time.Time) error {
	if len(metadata) == 0 {
		metadata = []byte("{}")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO operation_events(owner_id,operation_id,sequence,stage,status,progress,error_class,error_code,message,metadata_json,created_at) SELECT ?,?,COALESCE(MAX(sequence),0)+1,?,?,COALESCE((SELECT progress FROM operations WHERE id=?),0),?,?,?,?,? FROM operation_events WHERE operation_id=?`, ownerID, id, stage, status, id, class, code, message, metadata, formatTime(now), id)
	return mapWriteError(err)
}
