// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

func TestTerminalOperationEnqueuesMatchingWebhook(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	createOwner(t, store, now)
	subscription := domain.WebhookSubscription{
		ID: "whsub_1", OwnerID: "owner-1", URL: "https://receiver.example/hook", Secret: "whsec_1",
		Events: []string{"operation.terminal"}, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateWebhookSubscription(ctx, subscription); err != nil {
		t.Fatal(err)
	}
	instance, err := domain.NewInstance("instance-1", "owner-1", "project", domain.KindVPS, now)
	if err != nil {
		t.Fatal(err)
	}
	op := operation("op-1", "create-project", "hash-1", now)
	if _, _, err := store.CreateInstance(ctx, instance, op); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteOperation(ctx, "owner-1", op.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	deliveries, err := store.ListWebhookDeliveries(ctx, "owner-1", "pending", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].EventID == "" || deliveries[0].Status != "pending" || deliveries[0].Attempt != 1 {
		t.Fatalf("deliveries=%+v", deliveries)
	}
}

func TestDeletingSubscriptionCancelsPendingDeliveries(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	createOwner(t, store, now)
	subscription := domain.WebhookSubscription{
		ID: "whsub_1", OwnerID: "owner-1", URL: "https://receiver.example/hook", Secret: "whsec_1",
		Events: []string{"operation.terminal"}, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateWebhookSubscription(ctx, subscription); err != nil {
		t.Fatal(err)
	}
	instance, _ := domain.NewInstance("instance-1", "owner-1", "project", domain.KindVPS, now)
	op := operation("op-1", "create-project", "hash-1", now)
	if _, _, err := store.CreateInstance(ctx, instance, op); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteOperation(ctx, "owner-1", op.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteWebhookSubscription(ctx, "owner-1", "whsub_1", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	deliveries, err := store.ListWebhookDeliveries(ctx, "owner-1", "canceled", "whsub_1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("canceled deliveries=%+v", deliveries)
	}
}
