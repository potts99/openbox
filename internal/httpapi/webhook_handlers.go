// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/webhooks"
)

type WebhookDeliveries interface {
	ListWebhookDeliveries(context.Context, domain.OwnerID, string, domain.WebhookSubscriptionID, int) ([]domain.WebhookDelivery, error)
}

func (h *Handler) routeWebhooks(response http.ResponseWriter, request *http.Request, requestID string, rest []string) bool {
	if h.webhooks == nil {
		return false
	}
	owner := h.requestOwner(request)
	if len(rest) == 0 {
		switch request.Method {
		case http.MethodGet:
			items, err := h.webhooks.List(request.Context(), owner)
			if err != nil {
				h.writeServiceError(response, requestID, err)
				return true
			}
			result := make([]any, 0, len(items))
			for _, item := range items {
				result = append(result, mapWebhookSubscription(item, false))
			}
			h.writeJSON(response, http.StatusOK, map[string]any{"items": result})
		case http.MethodPost:
			var input struct {
				URL         string   `json:"url"`
				Description string   `json:"description"`
				Events      []string `json:"events"`
				Enabled     *bool    `json:"enabled"`
			}
			if h.decodeJSON(response, request, &input) != nil {
				h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
				return true
			}
			item, err := h.webhooks.Create(request.Context(), webhooks.CreateInput{OwnerID: owner, URL: input.URL, Description: input.Description, Events: input.Events, Enabled: input.Enabled})
			if err != nil {
				h.writeServiceError(response, requestID, err)
				return true
			}
			h.writeJSON(response, http.StatusCreated, mapWebhookSubscription(item, true))
		default:
			h.methodNotAllowed(response, requestID, http.MethodGet, http.MethodPost)
		}
		return true
	}
	if len(rest) != 1 {
		return false
	}
	id := domain.WebhookSubscriptionID(rest[0])
	switch request.Method {
	case http.MethodGet:
		item, err := h.webhooks.Get(request.Context(), owner, id)
		if err != nil {
			h.writeServiceError(response, requestID, err)
			return true
		}
		h.writeJSON(response, http.StatusOK, mapWebhookSubscription(item, false))
	case http.MethodPatch:
		var input struct {
			URL          *string   `json:"url"`
			Description  *string   `json:"description"`
			Events       *[]string `json:"events"`
			Enabled      *bool     `json:"enabled"`
			RotateSecret bool      `json:"rotate_secret"`
		}
		if h.decodeJSON(response, request, &input) != nil {
			h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
			return true
		}
		if input.URL == nil && input.Description == nil && input.Events == nil && input.Enabled == nil && !input.RotateSecret {
			h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
			return true
		}
		item, err := h.webhooks.Update(request.Context(), owner, id, webhooks.UpdateInput{
			URL: input.URL, Description: input.Description, Events: input.Events, Enabled: input.Enabled, RotateSecret: input.RotateSecret,
		})
		if err != nil {
			h.writeServiceError(response, requestID, err)
			return true
		}
		h.writeJSON(response, http.StatusOK, mapWebhookSubscription(item, input.RotateSecret))
	case http.MethodDelete:
		if err := h.webhooks.Delete(request.Context(), owner, id); err != nil {
			h.writeServiceError(response, requestID, err)
			return true
		}
		response.WriteHeader(http.StatusNoContent)
	default:
		h.methodNotAllowed(response, requestID, http.MethodGet, http.MethodPatch, http.MethodDelete)
	}
	return true
}

func (h *Handler) listWebhookDeliveries(response http.ResponseWriter, request *http.Request, requestID string) {
	if h.webhookDeliveries == nil {
		h.writeError(response, requestID, http.StatusNotImplemented, string(domain.CodeNotImplemented), "webhook_deliveries")
		return
	}
	status := request.URL.Query().Get("status")
	if status != "" && status != "pending" && status != "delivered" && status != "failed" && status != "dead" && status != "canceled" {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "status")
		return
	}
	limit := 100
	if value := request.URL.Query().Get("limit"); value != "" {
		var err error
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 1000 {
			h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "limit")
			return
		}
	}
	items, err := h.webhookDeliveries.ListWebhookDeliveries(request.Context(), h.requestOwner(request), status, domain.WebhookSubscriptionID(request.URL.Query().Get("subscription_id")), limit)
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	result := make([]any, 0, len(items))
	for _, item := range items {
		result = append(result, mapWebhookDelivery(item))
	}
	h.writeJSON(response, http.StatusOK, map[string]any{"items": result})
}

func mapWebhookSubscription(value domain.WebhookSubscription, includeSecret bool) map[string]any {
	result := map[string]any{
		"id": value.ID, "url": value.URL, "description": value.Description, "events": value.Events,
		"enabled": value.Enabled, "created_at": value.CreatedAt, "updated_at": value.UpdatedAt,
	}
	if includeSecret {
		result["secret"] = value.Secret
	}
	return result
}

func mapWebhookDelivery(value domain.WebhookDelivery) map[string]any {
	result := map[string]any{
		"id": value.ID, "event_id": value.EventID, "subscription_id": value.SubscriptionID,
		"status": value.Status, "attempt": value.Attempt, "created_at": value.CreatedAt, "updated_at": value.UpdatedAt,
	}
	if value.HTTPStatus != 0 {
		result["http_status"] = value.HTTPStatus
	}
	if value.ErrorClass != "" {
		result["error_class"] = value.ErrorClass
	}
	if value.NextAttemptAt != nil {
		result["next_attempt_at"] = value.NextAttemptAt
	}
	return result
}
