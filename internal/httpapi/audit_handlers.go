// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/httpapi/generated"
)

// AuditEvents lists durable owner-scoped audit records.
type AuditEvents interface {
	ListAuditEvents(context.Context, domain.OwnerID, int) ([]domain.AuditEvent, error)
}

func (h *Handler) listAuditEvents(response http.ResponseWriter, request *http.Request, requestID string) {
	if h.auditEvents == nil {
		h.writeError(response, requestID, http.StatusNotImplemented, string(domain.CodeNotImplemented), "audit_events")
		return
	}
	limit := 100
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 1000 {
			h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "limit")
			return
		}
		limit = parsed
	}
	events, err := h.auditEvents.ListAuditEvents(request.Context(), h.requestOwner(request), limit)
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	items := make([]generated.AuditEvent, 0, len(events))
	for _, event := range events {
		items = append(items, mapAuditEvent(event))
	}
	h.writeJSON(response, http.StatusOK, generated.ListAuditEventsResponse{Items: items})
}

func mapAuditEvent(event domain.AuditEvent) generated.AuditEvent {
	metadata := map[string]string{}
	if len(event.MetadataJSON) > 0 {
		_ = json.Unmarshal(event.MetadataJSON, &metadata)
		if metadata == nil {
			metadata = map[string]string{}
		}
	}
	return generated.AuditEvent{
		Id: string(event.ID), Actor: event.Actor, Action: event.Action,
		TargetType: event.TargetType, TargetId: event.TargetID, Outcome: event.Outcome,
		Metadata: metadata, CreatedAt: event.CreatedAt,
	}
}
