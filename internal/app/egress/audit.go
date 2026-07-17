// SPDX-License-Identifier: AGPL-3.0-only

package egress

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

const (
	ActionPolicyApply         = "policy.apply"
	ActionPolicyApplyFailed   = "policy.apply_failed"
	ActionPolicyRefresh       = "policy.refresh"
	ActionPolicyRefreshFailed = "policy.refresh_failed"
)

// PolicyAuditEvent is a redacted policy decision for durable audit storage.
// It must never carry DNS answers, packet payloads, or secret material.
type PolicyAuditEvent struct {
	OwnerID    domain.OwnerID
	Actor      string
	Action     string
	InstanceID domain.InstanceID
	ProfileID  domain.EgressProfileID
	Mode       domain.EgressMode
	Outcome    string
	Message    string
	Resolution string
}

// PolicyAuditor persists redacted policy audit events.
type PolicyAuditor interface {
	RecordPolicyEvent(context.Context, PolicyAuditEvent) error
}

// AuditStore persists immutable audit rows.
type AuditStore interface {
	CreateAuditEvent(context.Context, domain.AuditEvent) error
}

// DurablePolicyAuditor writes policy events through AuditStore.
type DurablePolicyAuditor struct {
	Store AuditStore
	Now   func() time.Time
	NewID func() string
}

func (a *DurablePolicyAuditor) RecordPolicyEvent(ctx context.Context, event PolicyAuditEvent) error {
	if a == nil || a.Store == nil {
		return fmt.Errorf("policy auditor store is required")
	}
	now := time.Now().UTC()
	if a.Now != nil {
		now = a.Now().UTC()
	}
	id := fmt.Sprintf("audit-%d", now.UnixNano())
	if a.NewID != nil {
		id = a.NewID()
	}
	meta := map[string]string{
		"instance_id": string(event.InstanceID),
		"profile_id":  string(event.ProfileID),
		"mode":        string(event.Mode),
	}
	if event.Resolution != "" {
		meta["resolution_state"] = event.Resolution
	}
	if event.Message != "" {
		meta["error_class"] = errorClassForAction(event.Action)
		if len(event.Message) > 200 {
			meta["message"] = event.Message[:200]
		} else {
			meta["message"] = event.Message
		}
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal policy audit metadata: %w", err)
	}
	actor := event.Actor
	if actor == "" {
		actor = "openboxd"
	}
	outcome := event.Outcome
	if outcome == "" {
		outcome = "succeeded"
	}
	if err := a.Store.CreateAuditEvent(ctx, domain.AuditEvent{
		ID: domain.AuditEventID(id), OwnerID: event.OwnerID, Actor: actor,
		Action: event.Action, TargetType: "instance", TargetID: string(event.InstanceID),
		Outcome: outcome, MetadataJSON: raw, CreatedAt: now,
	}); err != nil {
		log.Printf("egress: persist policy audit event action=%s instance=%s: %v", event.Action, event.InstanceID, err)
		return err
	}
	return nil
}

func errorClassForAction(action string) string {
	switch action {
	case ActionPolicyRefresh, ActionPolicyRefreshFailed:
		return "policy_refresh"
	default:
		return "policy_apply"
	}
}
