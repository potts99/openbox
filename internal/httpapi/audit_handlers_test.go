// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

type auditListStub struct {
	events []domain.AuditEvent
}

func (s auditListStub) ListAuditEvents(_ context.Context, owner domain.OwnerID, limit int) ([]domain.AuditEvent, error) {
	if owner != "owner-local" || limit < 1 {
		return nil, &domain.Error{Code: domain.CodeInvalidArgument, Field: "limit"}
	}
	return s.events, nil
}

func TestListAuditEvents(t *testing.T) {
	handler := newTestHandlerWithOptions(t, &fakeService{}, Options{
		OwnerID: "owner-local",
		AuditEvents: auditListStub{events: []domain.AuditEvent{{
			ID: "audit-1", OwnerID: "owner-local", Actor: "openboxd", Action: "policy.apply",
			TargetType: "instance", TargetID: "inst-1", Outcome: "succeeded",
			MetadataJSON: []byte(`{"profile_id":"egress-restricted","mode":"restricted"}`),
			CreatedAt:    time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		}}},
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/audit-events?limit=10", nil)
	request.Header.Set(HeaderAPIVersion, APIVersion)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0]["action"] != "policy.apply" {
		t.Fatalf("body=%#v", body)
	}
}

func TestListAuditEventsRejectsInvalidLimit(t *testing.T) {
	handler := newTestHandlerWithOptions(t, &fakeService{}, Options{
		OwnerID: "owner-local", AuditEvents: auditListStub{},
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/audit-events?limit=0", nil)
	request.Header.Set(HeaderAPIVersion, APIVersion)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
