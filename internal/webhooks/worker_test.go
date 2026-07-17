// SPDX-License-Identifier: AGPL-3.0-only

package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

func TestWorkerSignsAndCompletesDelivery(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	payload := []byte(`{"id":"evt_1","type":"operation.terminal","created_at":"2026-07-17T08:00:00Z","data":{}}`)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != string(payload) {
			t.Fatalf("body=%s", body)
		}
		if request.Header.Get("X-OpenBox-Event-Id") != "evt_1" || request.Header.Get("X-OpenBox-Delivery-Id") != "whd_1" {
			t.Fatalf("event headers missing: %#v", request.Header)
		}
		timestamp := request.Header.Get("X-OpenBox-Signature-Timestamp")
		if timestamp != strconv.FormatInt(now.Unix(), 10) {
			t.Fatalf("timestamp=%s", timestamp)
		}
		mac := hmac.New(sha256.New, []byte("whsec_test"))
		_, _ = mac.Write([]byte("v1|" + timestamp + "|evt_1|whd_1|" + string(payload)))
		if request.Header.Get("X-OpenBox-Signature") != "v1="+hex.EncodeToString(mac.Sum(nil)) {
			t.Fatalf("signature=%s", request.Header.Get("X-OpenBox-Signature"))
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	repository := &deliveryRepo{
		candidates: []domain.WebhookDelivery{{ID: "whd_1"}},
		dispatch: domain.WebhookDispatch{
			Delivery: domain.WebhookDelivery{ID: "whd_1", Attempt: 1},
			EventID:  "evt_1", URL: server.URL, Secret: "whsec_test", Payload: payload,
		},
	}
	worker, err := NewWorker(repository, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return now }
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repository.status != http.StatusNoContent || repository.failureClass != "" {
		t.Fatalf("completion status=%d class=%q", repository.status, repository.failureClass)
	}
}

func TestValidateSubscription(t *testing.T) {
	t.Parallel()
	if err := validate("https://receiver.example/hook", "build events", []string{"operation.terminal"}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		url    string
		events []string
	}{
		{"http://receiver.example/hook", []string{"operation.terminal"}},
		{"https://receiver.example/hook", nil},
		{"https://receiver.example/hook", []string{"unknown"}},
	} {
		if err := validate(input.url, "", input.events); err == nil {
			t.Fatalf("validate(%q,%v) succeeded", input.url, input.events)
		}
	}
}

type deliveryRepo struct {
	candidates   []domain.WebhookDelivery
	dispatch     domain.WebhookDispatch
	status       int
	failureClass string
}

func (r *deliveryRepo) ListClaimableWebhookDeliveries(context.Context, time.Time, int) ([]domain.WebhookDelivery, error) {
	return r.candidates, nil
}

func (r *deliveryRepo) ClaimWebhookDelivery(_ context.Context, _ domain.WebhookDeliveryID, _ string, _ time.Time) (domain.WebhookDispatch, bool, error) {
	return r.dispatch, true, nil
}

func (r *deliveryRepo) CompleteWebhookDelivery(_ context.Context, _ domain.WebhookDispatch, status int, failureClass string, _ time.Time) error {
	r.status, r.failureClass = status, failureClass
	return nil
}
