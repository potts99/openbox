// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/openbox-dev/openbox/internal/app/egress"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/httpapi/generated"
)

func (h *Handler) routeNetwork(response http.ResponseWriter, request *http.Request, requestID string, rest []string) bool {
	if h.egressProfiles == nil {
		return false
	}
	if len(rest) == 0 {
		return false
	}
	if rest[0] != "egress-profiles" {
		return false
	}
	if len(rest) == 1 {
		switch request.Method {
		case http.MethodGet:
			h.listEgressProfiles(response, request, requestID)
		case http.MethodPost:
			h.createEgressProfile(response, request, requestID)
		default:
			h.methodNotAllowed(response, requestID, http.MethodGet, http.MethodPost)
		}
		return true
	}
	if len(rest) == 2 {
		switch request.Method {
		case http.MethodGet:
			h.getEgressProfile(response, request, requestID, rest[1])
		case http.MethodPatch:
			h.updateEgressProfile(response, request, requestID, rest[1])
		case http.MethodDelete:
			h.deleteEgressProfile(response, request, requestID, rest[1])
		default:
			h.methodNotAllowed(response, requestID, http.MethodGet, http.MethodPatch, http.MethodDelete)
		}
		return true
	}
	return false
}

func (h *Handler) listEgressProfiles(response http.ResponseWriter, request *http.Request, requestID string) {
	profiles, err := h.egressProfiles.List(request.Context())
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	items := make([]generated.EgressProfile, 0, len(profiles))
	for _, profile := range profiles {
		count, countErr := h.egressProfiles.CountInstancesWithEgressProfile(request.Context(), profile.ID)
		if countErr != nil {
			h.writeServiceError(response, requestID, countErr)
			return
		}
		items = append(items, mapEgressProfile(profile, count))
	}
	h.writeJSON(response, http.StatusOK, generated.ListEgressProfilesResponse{Items: items})
}

func (h *Handler) getEgressProfile(response http.ResponseWriter, request *http.Request, requestID string, id string) {
	profile, count, err := h.egressProfiles.Get(request.Context(), domain.EgressProfileID(id))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusOK, mapEgressProfile(profile, count))
}

func (h *Handler) createEgressProfile(response http.ResponseWriter, request *http.Request, requestID string) {
	var input generated.CreateEgressProfileRequest
	if err := h.decodeJSON(response, request, &input); err != nil {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
		return
	}
	destinations := []string{}
	if input.AllowedDestinations != nil {
		destinations = *input.AllowedDestinations
	}
	profile, err := h.egressProfiles.Create(request.Context(), egress.CreateProfileInput{
		Name: input.Name, Mode: domain.EgressMode(input.Mode), AllowedDestinations: destinations,
	})
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusCreated, mapEgressProfile(profile, 0))
}

func (h *Handler) updateEgressProfile(response http.ResponseWriter, request *http.Request, requestID string, id string) {
	var input generated.UpdateEgressProfileRequest
	if err := h.decodeJSON(response, request, &input); err != nil {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
		return
	}
	update := egress.UpdateProfileInput{}
	if input.Name != nil {
		update.Name = input.Name
	}
	if input.Mode != nil {
		mode := domain.EgressMode(*input.Mode)
		update.Mode = &mode
	}
	if input.AllowedDestinations != nil {
		update.AllowedDestinations = input.AllowedDestinations
	}
	profile, applyErrors, err := h.egressProfiles.Update(request.Context(), domain.EgressProfileID(id), update)
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	count, countErr := h.egressProfiles.CountInstancesWithEgressProfile(request.Context(), profile.ID)
	if countErr != nil {
		h.writeServiceError(response, requestID, countErr)
		return
	}
	out := generated.UpdateEgressProfileResponse{Profile: mapEgressProfile(profile, count)}
	if len(applyErrors) > 0 {
		items := make([]generated.EgressApplyError, 0, len(applyErrors))
		for _, applyErr := range applyErrors {
			items = append(items, generated.EgressApplyError{
				InstanceId: string(applyErr.InstanceID), Message: applyErr.Message,
			})
		}
		out.ApplyErrors = &items
	}
	h.writeJSON(response, http.StatusOK, out)
}

func (h *Handler) deleteEgressProfile(response http.ResponseWriter, request *http.Request, requestID string, id string) {
	if err := h.egressProfiles.Delete(request.Context(), domain.EgressProfileID(id)); err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (h *Handler) attachInstanceEgressProfile(response http.ResponseWriter, request *http.Request, requestID string, instanceID string) {
	var input generated.AttachEgressProfileRequest
	if err := h.decodeJSON(response, request, &input); err != nil {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
		return
	}
	instance, err := h.service.AttachEgressProfile(request.Context(), h.requestOwner(request), domain.InstanceID(instanceID), domain.EgressProfileID(input.EgressProfileId))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	rows, listErr := h.service.ListSoftware(request.Context(), h.requestOwner(request), instance.ID)
	if listErr != nil {
		h.writeServiceError(response, requestID, listErr)
		return
	}
	h.writeJSON(response, http.StatusOK, mapInstance(instance, rows))
}

func mapEgressProfile(profile domain.EgressProfile, attached int) generated.EgressProfile {
	destinations := []string{}
	if len(profile.AllowedDestinationsJSON) > 0 {
		_ = json.Unmarshal(profile.AllowedDestinationsJSON, &destinations)
		if destinations == nil {
			destinations = []string{}
		}
	}
	dnsPolicy := profile.DNSPolicy
	if dnsPolicy == "" {
		dnsPolicy = domain.DNSPolicyHostResolve
	}
	out := generated.EgressProfile{
		Id: string(profile.ID), Name: profile.Name, Mode: generated.EgressProfileMode(profile.Mode),
		AllowedDestinations: destinations, DnsPolicy: generated.EgressProfileDnsPolicy(dnsPolicy),
		System: profile.System, CreatedAt: profile.CreatedAt, UpdatedAt: profile.UpdatedAt,
	}
	if attached > 0 {
		count := attached
		out.AttachedInstanceCount = &count
	}
	return out
}
