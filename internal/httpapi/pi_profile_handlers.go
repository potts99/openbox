// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"net/http"

	"github.com/openbox-dev/openbox/internal/domain"
	pi "github.com/openbox-dev/openbox/internal/profiles/pi"
)

func (h *Handler) routePiProfiles(response http.ResponseWriter, request *http.Request, requestID string, rest []string) bool {
	if h.piProfiles == nil {
		return false
	}
	owner := h.requestOwner(request)
	if len(rest) == 0 {
		if !h.requireMethod(response, request, requestID, http.MethodGet) {
			return true
		}
		h.listPiProfiles(response, request, requestID, owner)
		return true
	}
	if len(rest) == 2 && rest[1] == "versions" {
		if !h.requireMethod(response, request, requestID, http.MethodGet) {
			return true
		}
		h.listPiProfileVersions(response, request, requestID, owner, rest[0])
		return true
	}
	if len(rest) == 2 && rest[1] == "rollback" {
		if !h.requireMethod(response, request, requestID, http.MethodPost) {
			return true
		}
		h.rollbackPiProfile(response, request, requestID, owner, rest[0])
		return true
	}
	if len(rest) == 2 && rest[1] == "apply" {
		if !h.requireMethod(response, request, requestID, http.MethodPost) {
			return true
		}
		h.applyPiProfile(response, request, requestID, owner, rest[0])
		return true
	}
	return false
}

func (h *Handler) listPiProfiles(response http.ResponseWriter, request *http.Request, requestID string, owner domain.OwnerID) {
	values, err := h.piProfiles.List(request.Context(), owner)
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	items := make([]map[string]any, 0, len(values))
	for _, value := range values {
		items = append(items, mapPiProfile(value))
	}
	h.writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) listPiProfileVersions(response http.ResponseWriter, request *http.Request, requestID string, owner domain.OwnerID, id string) {
	values, err := h.piProfiles.ListHistory(request.Context(), owner, domain.PiProfileID(id))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	items := make([]map[string]any, 0, len(values))
	for _, value := range values {
		items = append(items, map[string]any{
			"version":       value.Version,
			"settings_json": string(value.SettingsJSON),
			"created_at":    value.CreatedAt,
		})
	}
	h.writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) rollbackPiProfile(response http.ResponseWriter, request *http.Request, requestID string, owner domain.OwnerID, id string) {
	var input struct {
		Version int `json:"version"`
	}
	if h.decodeJSON(response, request, &input) != nil {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
		return
	}
	profile, err := h.piProfiles.Rollback(request.Context(), owner, domain.PiProfileID(id), input.Version)
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusOK, mapPiProfile(profile))
}

func (h *Handler) applyPiProfile(response http.ResponseWriter, request *http.Request, requestID string, owner domain.OwnerID, id string) {
	if h.piApplier == nil {
		h.writeError(response, requestID, http.StatusNotImplemented, string(domain.CodeNotImplemented), "apply")
		return
	}
	var input struct {
		InstanceIDs []string `json:"instance_ids"`
	}
	if h.decodeJSON(response, request, &input) != nil {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
		return
	}
	targets := make([]pi.InstanceTarget, 0, len(input.InstanceIDs))
	for _, instanceID := range input.InstanceIDs {
		instance, err := h.service.GetInstance(request.Context(), owner, domain.InstanceID(instanceID))
		if err != nil {
			h.writeServiceError(response, requestID, err)
			return
		}
		targets = append(targets, pi.InstanceTarget{ID: instance.ID, RuntimeRef: instance.RuntimeRef})
	}
	if err := h.piApplier.Apply(request.Context(), owner, domain.PiProfileID(id), targets); err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

func mapPiProfile(profile domain.PiProfile) map[string]any {
	return map[string]any{
		"id":            string(profile.ID),
		"name":          profile.Name,
		"version":       profile.Version,
		"settings_json": string(profile.SettingsJSON),
		"updated_at":    profile.UpdatedAt,
	}
}
