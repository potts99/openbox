// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

func TestAuditEventsAreImmutableStructuredAndOwnerScoped(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/openbox.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	for _, owner := range []domain.OwnerID{"owner-1", "owner-2"} {
		if err := store.CreateOwner(ctx, domain.Owner{ID: owner, Name: string(owner), CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	event := domain.AuditEvent{ID: "audit-1", OwnerID: "owner-1", Actor: "SHA256:fingerprint", Action: "ssh.command", TargetType: "gateway", TargetID: "openbox", Outcome: "succeeded", MetadataJSON: []byte(`{"command":"ls"}`), CreatedAt: now}
	if err := store.CreateAuditEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAuditEvent(ctx, event); err == nil {
		t.Fatal("duplicate immutable audit event accepted")
	}
	events, err := store.ListAuditEvents(ctx, "owner-1", 10)
	if err != nil || len(events) != 1 || string(events[0].MetadataJSON) != `{"command":"ls"}` {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	other, err := store.ListAuditEvents(ctx, "owner-2", 10)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-owner events=%+v err=%v", other, err)
	}
}

func TestAuditEventRejectsInvalidMetadata(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/openbox.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateAuditEvent(ctx, domain.AuditEvent{ID: "event", OwnerID: "owner", Actor: "actor", Action: "action", TargetType: "gateway", TargetID: "openbox", Outcome: "failed", MetadataJSON: []byte("secret=bad"), CreatedAt: time.Now()}); err == nil {
		t.Fatal("invalid structured metadata accepted")
	}
}
