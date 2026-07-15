// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"net/http"

	"github.com/openbox-dev/openbox/internal/domain"
)

// certificateAllow answers Caddy on-demand TLS ask callbacks.
// GET /v1/certificates/allow?domain=hostname — 200 allow, 404 deny.
// Unauthenticated by design; the API bind address is the trust boundary.
func (h *Handler) certificateAllow(response http.ResponseWriter, request *http.Request, requestID string) {
	if h.routes == nil {
		h.writeError(response, requestID, http.StatusNotImplemented, "not_implemented", "routes")
		return
	}
	domainName := request.URL.Query().Get("domain")
	allowed, err := h.routes.CertificateAllowed(request.Context(), domainName)
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	if !allowed {
		h.writeError(response, requestID, http.StatusNotFound, string(domain.CodeNotFound), "domain")
		return
	}
	response.WriteHeader(http.StatusOK)
}
