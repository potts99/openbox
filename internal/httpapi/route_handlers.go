// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"net/http"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/httpapi/generated"
	"github.com/openbox-dev/openbox/internal/routes"
)

func (h *Handler) routeRoutes(response http.ResponseWriter, request *http.Request, requestID string, rest []string) bool {
	if h.routes == nil {
		return false
	}
	owner := h.requestOwner(request)
	if len(rest) == 0 {
		switch request.Method {
		case http.MethodGet:
			h.listRoutes(response, request, requestID, owner)
		case http.MethodPost:
			h.createRoute(response, request, requestID, owner)
		default:
			h.methodNotAllowed(response, requestID, http.MethodGet, http.MethodPost)
		}
		return true
	}
	if len(rest) == 1 {
		switch request.Method {
		case http.MethodGet:
			h.getRoute(response, request, requestID, owner, rest[0])
		case http.MethodPatch:
			h.updateRoute(response, request, requestID, owner, rest[0])
		case http.MethodDelete:
			h.deleteRoute(response, request, requestID, owner, rest[0])
		default:
			h.methodNotAllowed(response, requestID, http.MethodGet, http.MethodPatch, http.MethodDelete)
		}
		return true
	}
	if len(rest) == 2 && rest[1] == "publish" {
		if !h.requireMethod(response, request, requestID, http.MethodPost) {
			return true
		}
		h.publishRoute(response, request, requestID, owner, rest[0])
		return true
	}
	if len(rest) == 2 && rest[1] == "validate-dns" {
		if !h.requireMethod(response, request, requestID, http.MethodPost) {
			return true
		}
		h.validateRouteDNS(response, request, requestID, owner, rest[0])
		return true
	}
	return false
}

func (h *Handler) listRoutes(response http.ResponseWriter, request *http.Request, requestID string, owner domain.OwnerID) {
	values, err := h.routes.List(request.Context(), owner)
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	items := make([]generated.Route, 0, len(values))
	for _, value := range values {
		items = append(items, mapRoute(value))
	}
	h.writeJSON(response, http.StatusOK, generated.ListRoutesResponse{Items: items})
}

func (h *Handler) createRoute(response http.ResponseWriter, request *http.Request, requestID string, owner domain.OwnerID) {
	var input generated.CreateRouteRequest
	if h.decodeJSON(response, request, &input) != nil {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
		return
	}
	route, err := h.routes.Create(request.Context(), owner, routes.CreateInput{
		InstanceID: domain.InstanceID(input.InstanceId),
		Hostname:   input.Hostname,
		TargetPort: input.TargetPort,
	})
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusCreated, mapRoute(route))
}

func (h *Handler) getRoute(response http.ResponseWriter, request *http.Request, requestID string, owner domain.OwnerID, rawID string) {
	route, err := h.routes.Get(request.Context(), owner, domain.RouteID(rawID))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusOK, mapRoute(route))
}

func (h *Handler) updateRoute(response http.ResponseWriter, request *http.Request, requestID string, owner domain.OwnerID, rawID string) {
	var input generated.UpdateRouteRequest
	if h.decodeJSON(response, request, &input) != nil {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
		return
	}
	if input.Hostname == nil && input.TargetPort == nil {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
		return
	}
	route, err := h.routes.Update(request.Context(), owner, domain.RouteID(rawID), routes.UpdateInput{
		Hostname:   input.Hostname,
		TargetPort: input.TargetPort,
	})
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusOK, mapRoute(route))
}

func (h *Handler) deleteRoute(response http.ResponseWriter, request *http.Request, requestID string, owner domain.OwnerID, rawID string) {
	if err := h.routes.Delete(request.Context(), owner, domain.RouteID(rawID)); err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (h *Handler) publishRoute(response http.ResponseWriter, request *http.Request, requestID string, owner domain.OwnerID, rawID string) {
	route, err := h.routes.Publish(request.Context(), owner, domain.RouteID(rawID))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusOK, mapRoute(route))
}

func (h *Handler) validateRouteDNS(response http.ResponseWriter, request *http.Request, requestID string, owner domain.OwnerID, rawID string) {
	route, err := h.routes.ValidateDNS(request.Context(), owner, domain.RouteID(rawID))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusOK, mapRoute(route))
}

func (h *Handler) listSuggestedPorts(response http.ResponseWriter, request *http.Request, requestID, rawInstanceID string) {
	if h.routes == nil {
		h.writeError(response, requestID, http.StatusNotFound, string(domain.CodeNotFound), "path")
		return
	}
	ports, err := h.routes.SuggestPorts(request.Context(), h.requestOwner(request), domain.InstanceID(rawInstanceID))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	if ports == nil {
		ports = []int{}
	}
	h.writeJSON(response, http.StatusOK, generated.SuggestedPortsResponse{Items: ports})
}

func mapRoute(value domain.Route) generated.Route {
	return generated.Route{
		Id:         string(value.ID),
		InstanceId: string(value.InstanceID),
		Hostname:   value.Hostname,
		TargetPort: value.TargetPort,
		Visibility: generated.RouteVisibility(value.Visibility),
		TlsState:   value.TLSState,
		CreatedAt:  value.CreatedAt,
		UpdatedAt:  value.UpdatedAt,
	}
}
