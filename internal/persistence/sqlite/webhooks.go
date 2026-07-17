// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

type webhookEnvelope struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	CreatedAt time.Time       `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

// CreateWebhookSubscription persists an already-validated subscription.
func (s *Store) CreateWebhookSubscription(ctx context.Context, subscription domain.WebhookSubscription) error {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM webhook_subscriptions WHERE owner_id=?`, subscription.OwnerID).Scan(&count); err != nil {
		return err
	}
	if count >= 10 {
		return &domain.Error{Code: domain.CodeQuotaExceeded, Field: "subscriptions"}
	}
	events, err := json.Marshal(subscription.Events)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO webhook_subscriptions(id,owner_id,url,description,secret,events_json,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		subscription.ID, subscription.OwnerID, subscription.URL, subscription.Description, subscription.Secret, events, subscription.Enabled, formatTime(subscription.CreatedAt), formatTime(subscription.UpdatedAt))
	return mapWriteError(err)
}

func (s *Store) ListWebhookSubscriptions(ctx context.Context, ownerID domain.OwnerID) ([]domain.WebhookSubscription, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,owner_id,url,description,secret,events_json,enabled,created_at,updated_at FROM webhook_subscriptions WHERE owner_id=? ORDER BY created_at DESC,id DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.WebhookSubscription
	for rows.Next() {
		item, err := scanWebhookSubscription(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) GetWebhookSubscription(ctx context.Context, ownerID domain.OwnerID, id domain.WebhookSubscriptionID) (domain.WebhookSubscription, error) {
	item, err := scanWebhookSubscription(s.db.QueryRowContext(ctx, `SELECT id,owner_id,url,description,secret,events_json,enabled,created_at,updated_at FROM webhook_subscriptions WHERE owner_id=? AND id=?`, ownerID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WebhookSubscription{}, &domain.Error{Code: domain.CodeNotFound, Field: "subscription"}
	}
	return item, err
}

func (s *Store) UpdateWebhookSubscription(ctx context.Context, ownerID domain.OwnerID, id domain.WebhookSubscriptionID, url, description *string, events []string, updateEvents bool, enabled *bool, secret *string, now time.Time) (domain.WebhookSubscription, error) {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return domain.WebhookSubscription{}, err
	}
	defer release()
	current, err := scanWebhookSubscription(s.db.QueryRowContext(ctx, `SELECT id,owner_id,url,description,secret,events_json,enabled,created_at,updated_at FROM webhook_subscriptions WHERE owner_id=? AND id=?`, ownerID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WebhookSubscription{}, &domain.Error{Code: domain.CodeNotFound, Field: "subscription"}
	}
	if err != nil {
		return domain.WebhookSubscription{}, err
	}
	if url != nil {
		current.URL = *url
	}
	if description != nil {
		current.Description = *description
	}
	if updateEvents {
		current.Events = events
	}
	if enabled != nil {
		current.Enabled = *enabled
	}
	if secret != nil {
		current.Secret = *secret
	}
	current.UpdatedAt = now.UTC()
	eventJSON, err := json.Marshal(current.Events)
	if err != nil {
		return domain.WebhookSubscription{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE webhook_subscriptions SET url=?,description=?,secret=?,events_json=?,enabled=?,updated_at=? WHERE owner_id=? AND id=?`,
		current.URL, current.Description, current.Secret, eventJSON, current.Enabled, formatTime(current.UpdatedAt), ownerID, id); err != nil {
		return domain.WebhookSubscription{}, mapWriteError(err)
	}
	if !current.Enabled {
		if _, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status='canceled',updated_at=? WHERE owner_id=? AND subscription_id=? AND status IN ('pending','running')`, formatTime(now), ownerID, id); err != nil {
			return domain.WebhookSubscription{}, err
		}
	}
	return current, nil
}

func (s *Store) DeleteWebhookSubscription(ctx context.Context, ownerID domain.OwnerID, id domain.WebhookSubscriptionID, now time.Time) error {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	result, err := s.db.ExecContext(ctx, `DELETE FROM webhook_subscriptions WHERE owner_id=? AND id=?`, ownerID, id)
	if err != nil {
		return mapWriteError(err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return &domain.Error{Code: domain.CodeNotFound, Field: "subscription"}
	}
	_, err = s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status='canceled',updated_at=? WHERE owner_id=? AND subscription_id=? AND status IN ('pending','running')`, formatTime(now), ownerID, id)
	return err
}

func (s *Store) ListWebhookDeliveries(ctx context.Context, ownerID domain.OwnerID, status string, subscriptionID domain.WebhookSubscriptionID, limit int) ([]domain.WebhookDelivery, error) {
	if limit < 1 || limit > 1000 {
		return nil, &domain.Error{Code: domain.CodeInvalidArgument, Field: "limit"}
	}
	query := `SELECT id,event_id,subscription_id,status,attempt,COALESCE(http_status,0),error_class,next_attempt_at,created_at,updated_at FROM webhook_deliveries WHERE owner_id=?`
	args := []any{ownerID}
	if status != "" {
		query += ` AND status=?`
		args = append(args, status)
	}
	if subscriptionID != "" {
		query += ` AND subscription_id=?`
		args = append(args, subscriptionID)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.WebhookDelivery
	for rows.Next() {
		item, err := scanWebhookDelivery(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ListClaimableWebhookDeliveries(ctx context.Context, now time.Time, limit int) ([]domain.WebhookDelivery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,event_id,subscription_id,status,attempt,COALESCE(http_status,0),error_class,next_attempt_at,created_at,updated_at FROM webhook_deliveries WHERE status='pending' AND next_attempt_at<=? ORDER BY next_attempt_at,id LIMIT ?`, formatTime(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.WebhookDelivery
	for rows.Next() {
		item, err := scanWebhookDelivery(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// ClaimWebhookDelivery leases an attempt and enforces the two-attempt per
// subscription concurrency limit.
func (s *Store) ClaimWebhookDelivery(ctx context.Context, id domain.WebhookDeliveryID, worker string, now time.Time) (domain.WebhookDispatch, bool, error) {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return domain.WebhookDispatch{}, false, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.WebhookDispatch{}, false, err
	}
	defer tx.Rollback()
	var dispatch domain.WebhookDispatch
	err = tx.QueryRowContext(ctx, `SELECT d.id,d.event_id,d.subscription_id,d.attempt,d.payload_json,s.url,s.secret,s.enabled,d.claim_expires_at
		FROM webhook_deliveries d LEFT JOIN webhook_subscriptions s ON s.id=d.subscription_id AND s.owner_id=d.owner_id
		WHERE d.id=? AND d.status='pending'`, id).Scan(&dispatch.Delivery.ID, &dispatch.EventID, &dispatch.SubscriptionID, &dispatch.Attempt, &dispatch.Payload, &dispatch.URL, &dispatch.Secret, &dispatch.Enabled, new(sql.NullString))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WebhookDispatch{}, false, nil
	}
	if err != nil {
		return domain.WebhookDispatch{}, false, err
	}
	if !dispatch.Enabled || dispatch.URL == "" {
		_, err := tx.ExecContext(ctx, `UPDATE webhook_deliveries SET status='canceled',updated_at=? WHERE id=? AND status='pending'`, formatTime(now), id)
		if err != nil {
			return domain.WebhookDispatch{}, false, err
		}
		return domain.WebhookDispatch{}, false, tx.Commit()
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM webhook_deliveries WHERE subscription_id=? AND status='pending' AND claimed_by!='' AND claim_expires_at>?`, dispatch.SubscriptionID, formatTime(now)).Scan(&active); err != nil {
		return domain.WebhookDispatch{}, false, err
	}
	if active >= 2 {
		return domain.WebhookDispatch{}, false, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE webhook_deliveries SET claimed_by=?,claim_expires_at=?,updated_at=? WHERE id=? AND status='pending' AND (claimed_by='' OR claim_expires_at IS NULL OR claim_expires_at<=?)`, worker, formatTime(now.Add(15*time.Second)), formatTime(now), id, formatTime(now))
	if err != nil {
		return domain.WebhookDispatch{}, false, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return domain.WebhookDispatch{}, false, nil
	}
	dispatch.Delivery = domain.WebhookDelivery{ID: id, EventID: dispatch.EventID, SubscriptionID: dispatch.SubscriptionID, Status: "pending", Attempt: dispatch.Attempt}
	if err := tx.Commit(); err != nil {
		return domain.WebhookDispatch{}, false, err
	}
	return dispatch, true, nil
}

func (s *Store) CompleteWebhookDelivery(ctx context.Context, dispatch domain.WebhookDispatch, statusCode int, failureClass string, now time.Time) error {
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
	if statusCode >= 200 && statusCode < 300 {
		_, err := tx.ExecContext(ctx, `UPDATE webhook_deliveries SET status='delivered',http_status=?,error_class='',claimed_by='',claim_expires_at=NULL,updated_at=? WHERE id=? AND status='pending' AND claimed_by!=''`, statusCode, formatTime(now), dispatch.Delivery.ID)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	var first sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT first_attempt_at FROM webhook_deliveries WHERE id=? AND status='pending' AND claimed_by!=''`, dispatch.Delivery.ID).Scan(&first); err != nil {
		return err
	}
	firstAt := now.UTC()
	if first.Valid {
		firstAt, err = parseTime(first.String)
		if err != nil {
			return err
		}
	}
	dead := dispatch.Attempt >= 8 || now.After(firstAt.Add(24*time.Hour))
	finalStatus := "failed"
	if dead {
		finalStatus = "dead"
	}
	_, err = tx.ExecContext(ctx, `UPDATE webhook_deliveries SET status=?,http_status=?,error_class=?,first_attempt_at=?,claimed_by='',claim_expires_at=NULL,updated_at=? WHERE id=? AND status='pending' AND claimed_by!=''`,
		finalStatus, nullableStatus(statusCode), failureClass, formatTime(firstAt), formatTime(now), dispatch.Delivery.ID)
	if err != nil {
		return err
	}
	if !dead {
		next := now.Add(webhookBackoff(dispatch.Attempt))
		nextID, err := newWebhookID("whd_")
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO webhook_deliveries(id,owner_id,event_id,subscription_id,event_type,payload_json,status,attempt,first_attempt_at,next_attempt_at,created_at,updated_at) SELECT ?,owner_id,event_id,subscription_id,event_type,payload_json,'pending',?,first_attempt_at,?,?,? FROM webhook_deliveries WHERE id=?`,
			nextID, dispatch.Attempt+1, formatTime(next), formatTime(now), formatTime(now), dispatch.Delivery.ID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func enqueueOperationTerminalTx(ctx context.Context, tx *sql.Tx, ownerID domain.OwnerID, operationID domain.OperationID, now time.Time) error {
	var operationType, targetType, targetID, status, errorCode string
	if err := tx.QueryRowContext(ctx, `SELECT type,target_type,target_id,status,error_code FROM operations WHERE owner_id=? AND id=?`, ownerID, operationID).
		Scan(&operationType, &targetType, &targetID, &status, &errorCode); err != nil {
		return err
	}
	if status != string(domain.OperationSucceeded) && status != string(domain.OperationFailed) {
		return nil
	}
	data, err := json.Marshal(map[string]string{
		"operation_id": string(operationID), "operation_type": operationType, "target_type": targetType, "target_id": targetID, "status": status, "error_code": errorCode,
	})
	if err != nil {
		return err
	}
	return enqueueWebhookEventTx(ctx, tx, ownerID, "operation.terminal", data, now)
}

func enqueueInstanceExpiredTx(ctx context.Context, tx *sql.Tx, instance domain.Instance, operationID domain.OperationID, now time.Time) error {
	if instance.ExpiresAt == nil {
		return nil
	}
	data, err := json.Marshal(map[string]string{
		"instance_id": string(instance.ID), "kind": string(instance.Kind), "expires_at": instance.ExpiresAt.UTC().Format(time.RFC3339Nano), "delete_operation_id": string(operationID),
	})
	if err != nil {
		return err
	}
	return enqueueWebhookEventTx(ctx, tx, instance.OwnerID, "instance.expired", data, now)
}

func enqueueInstanceDeletedTx(ctx context.Context, tx *sql.Tx, instance domain.Instance, now time.Time) error {
	data, err := json.Marshal(map[string]string{"instance_id": string(instance.ID), "kind": string(instance.Kind), "name": instance.Name})
	if err != nil {
		return err
	}
	return enqueueWebhookEventTx(ctx, tx, instance.OwnerID, "instance.deleted", data, now)
}

func enqueueWebhookEventTx(ctx context.Context, tx *sql.Tx, ownerID domain.OwnerID, eventType string, data []byte, now time.Time) error {
	eventID, err := newWebhookID("evt_")
	if err != nil {
		return err
	}
	payload, err := json.Marshal(webhookEnvelope{ID: eventID, Type: eventType, CreatedAt: now.UTC(), Data: data})
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,events_json FROM webhook_subscriptions WHERE owner_id=? AND enabled=1`, ownerID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, eventsJSON string
		if err := rows.Scan(&id, &eventsJSON); err != nil {
			return err
		}
		var events []string
		if err := json.Unmarshal([]byte(eventsJSON), &events); err != nil {
			return err
		}
		if !containsWebhookEvent(events, eventType) {
			continue
		}
		deliveryID, err := newWebhookID("whd_")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO webhook_deliveries(id,owner_id,event_id,subscription_id,event_type,payload_json,status,attempt,next_attempt_at,created_at,updated_at) VALUES(?,?,?,?,?,?,'pending',1,?,?,?)`,
			deliveryID, ownerID, eventID, id, eventType, payload, formatTime(now), formatTime(now), formatTime(now)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func scanWebhookSubscription(scanner interface{ Scan(...any) error }) (domain.WebhookSubscription, error) {
	var item domain.WebhookSubscription
	var events, created, updated string
	if err := scanner.Scan(&item.ID, &item.OwnerID, &item.URL, &item.Description, &item.Secret, &events, &item.Enabled, &created, &updated); err != nil {
		return domain.WebhookSubscription{}, err
	}
	if err := json.Unmarshal([]byte(events), &item.Events); err != nil {
		return domain.WebhookSubscription{}, err
	}
	var err error
	item.CreatedAt, err = parseTime(created)
	if err == nil {
		item.UpdatedAt, err = parseTime(updated)
	}
	return item, err
}

func scanWebhookDelivery(scanner interface{ Scan(...any) error }) (domain.WebhookDelivery, error) {
	var item domain.WebhookDelivery
	var next sql.NullString
	var created, updated string
	if err := scanner.Scan(&item.ID, &item.EventID, &item.SubscriptionID, &item.Status, &item.Attempt, &item.HTTPStatus, &item.ErrorClass, &next, &created, &updated); err != nil {
		return domain.WebhookDelivery{}, err
	}
	var err error
	item.NextAttemptAt, err = parseNullableTime(next)
	if err == nil {
		item.CreatedAt, err = parseTime(created)
	}
	if err == nil {
		item.UpdatedAt, err = parseTime(updated)
	}
	return item, err
}

func newWebhookID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value[:]), nil
}

func containsWebhookEvent(events []string, candidate string) bool {
	for _, event := range events {
		if event == candidate {
			return true
		}
	}
	return false
}

func webhookBackoff(attempt int) time.Duration {
	backoff := 30 * time.Second
	for i := 1; i < attempt && backoff < 6*time.Hour; i++ {
		backoff *= 2
		if backoff > 6*time.Hour {
			return 6 * time.Hour
		}
	}
	return backoff
}

func nullableStatus(value int) any {
	if value == 0 {
		return nil
	}
	return value
}
