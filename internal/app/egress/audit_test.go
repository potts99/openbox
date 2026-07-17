// SPDX-License-Identifier: AGPL-3.0-only

package egress_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/egress"
	"github.com/openbox-dev/openbox/internal/domain"
)

type captureAuditStore struct {
	events []domain.AuditEvent
	err    error
}

func (s *captureAuditStore) CreateAuditEvent(_ context.Context, event domain.AuditEvent) error {
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, event)
	return nil
}

func TestDurablePolicyAuditorWritesRefreshErrorClass(t *testing.T) {
	store := &captureAuditStore{}
	auditor := &egress.DurablePolicyAuditor{
		Store: store,
		Now:   func() time.Time { return time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC) },
		NewID: func() string { return "audit-1" },
	}
	if err := auditor.RecordPolicyEvent(context.Background(), egress.PolicyAuditEvent{
		OwnerID: "owner-1", Actor: "openboxd", Action: egress.ActionPolicyRefreshFailed,
		InstanceID: "inst-1", ProfileID: "egress-hosts", Mode: domain.EgressRestricted,
		Outcome: "failed", Message: "resolve failed",
	}); err != nil {
		t.Fatal(err)
	}
	if len(store.events) != 1 {
		t.Fatalf("events=%d", len(store.events))
	}
	var meta map[string]string
	if err := json.Unmarshal(store.events[0].MetadataJSON, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["error_class"] != "policy_refresh" {
		t.Fatalf("error_class=%q", meta["error_class"])
	}
}

func TestDurablePolicyAuditorSurfacesPersistErrors(t *testing.T) {
	store := &captureAuditStore{err: errors.New("disk full")}
	auditor := &egress.DurablePolicyAuditor{Store: store, NewID: func() string { return "audit-1" }}
	if err := auditor.RecordPolicyEvent(context.Background(), egress.PolicyAuditEvent{
		OwnerID: "owner-1", Action: egress.ActionPolicyApply, InstanceID: "inst-1",
		ProfileID: "egress-restricted", Mode: domain.EgressRestricted, Outcome: "succeeded",
	}); err == nil {
		t.Fatal("expected persist error")
	}
}
