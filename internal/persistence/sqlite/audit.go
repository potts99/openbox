// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openbox-dev/openbox/internal/domain"
)

// CreateAuditEvent appends an immutable, structured security audit record.
// Callers must keep credentials and session content out of MetadataJSON.
func (s *Store) CreateAuditEvent(ctx context.Context, event domain.AuditEvent) error {
	if event.ID == "" || event.OwnerID == "" || event.Actor == "" || event.Action == "" || event.TargetType == "" || event.TargetID == "" || event.Outcome == "" || event.CreatedAt.IsZero() {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "audit_event"}
	}
	if len(event.MetadataJSON) == 0 {
		event.MetadataJSON = []byte("{}")
	}
	if !json.Valid(event.MetadataJSON) {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "audit_event.metadata"}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_events(id,owner_id,actor,action,target_type,target_id,outcome,metadata_json,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		event.ID, event.OwnerID, event.Actor, event.Action, event.TargetType, event.TargetID, event.Outcome, event.MetadataJSON, formatTime(event.CreatedAt))
	if err != nil {
		return fmt.Errorf("create audit event: %w", mapWriteError(err))
	}
	return nil
}

func (s *Store) ListAuditEvents(ctx context.Context, owner domain.OwnerID, limit int) ([]domain.AuditEvent, error) {
	if owner == "" || limit <= 0 || limit > 1000 {
		return nil, &domain.Error{Code: domain.CodeInvalidArgument, Field: "limit"}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,owner_id,actor,action,target_type,target_id,outcome,metadata_json,created_at FROM audit_events WHERE owner_id=? ORDER BY created_at DESC,id DESC LIMIT ?`, owner, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	events := make([]domain.AuditEvent, 0)
	for rows.Next() {
		var event domain.AuditEvent
		var created string
		if err := rows.Scan(&event.ID, &event.OwnerID, &event.Actor, &event.Action, &event.TargetType, &event.TargetID, &event.Outcome, &event.MetadataJSON, &created); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		event.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
