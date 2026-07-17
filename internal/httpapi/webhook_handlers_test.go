// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/webhooks"
)

func TestWebhookSubscriptionCreateAndList(t *testing.T) {
	t.Parallel()
	repo := &webhookSubscriptionRepo{}
	service, err := webhooks.New(repo)
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithOptions(t, &fakeService{}, Options{OwnerID: "owner-local", Webhooks: service})
	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/webhook-subscriptions",
		strings.NewReader(`{"url":"https://receiver.example/hook","description":"agent events","events":["operation.terminal"]}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	assertJSONContains(t, create.Body.Bytes(), `"secret":"`, `"url":"https://receiver.example/hook"`)

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/webhook-subscriptions", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), `"secret"`) {
		t.Fatalf("list leaked secret: %s", list.Body.String())
	}
}

type webhookSubscriptionRepo struct {
	subscription domain.WebhookSubscription
}

func (r *webhookSubscriptionRepo) CreateWebhookSubscription(_ context.Context, subscription domain.WebhookSubscription) error {
	subscription.ID, subscription.Secret = "whsub_test", "whsec_test"
	r.subscription = subscription
	return nil
}

func (r *webhookSubscriptionRepo) ListWebhookSubscriptions(_ context.Context, _ domain.OwnerID) ([]domain.WebhookSubscription, error) {
	if r.subscription.ID == "" {
		return []domain.WebhookSubscription{}, nil
	}
	return []domain.WebhookSubscription{r.subscription}, nil
}

func (r *webhookSubscriptionRepo) GetWebhookSubscription(_ context.Context, _ domain.OwnerID, id domain.WebhookSubscriptionID) (domain.WebhookSubscription, error) {
	if r.subscription.ID != id {
		return domain.WebhookSubscription{}, &domain.Error{Code: domain.CodeNotFound, Field: "subscription"}
	}
	return r.subscription, nil
}

func (r *webhookSubscriptionRepo) UpdateWebhookSubscription(_ context.Context, _ domain.OwnerID, _ domain.WebhookSubscriptionID, _, _ *string, _ []string, _ bool, _ *bool, _ *string, _ time.Time) (domain.WebhookSubscription, error) {
	return r.subscription, nil
}

func (r *webhookSubscriptionRepo) DeleteWebhookSubscription(_ context.Context, _ domain.OwnerID, _ domain.WebhookSubscriptionID, _ time.Time) error {
	r.subscription = domain.WebhookSubscription{}
	return nil
}
