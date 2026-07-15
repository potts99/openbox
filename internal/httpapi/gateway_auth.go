// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"net/http"
	"strings"

	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/routes"
)

// gatewayAuth is Caddy forward_auth for HTTPS routes.
// GET /v1/gateway/auth — Host selects the route; public bypasses login.
func (h *Handler) gatewayAuth(response http.ResponseWriter, request *http.Request, requestID string) {
	if h.routes == nil {
		h.writeError(response, requestID, http.StatusNotImplemented, string(domain.CodeNotImplemented), "routes")
		return
	}
	hostname := gatewayHostname(request)
	creds := routes.AccessCredentials{}
	if owner := requestOwnerOptional(request); owner != "" {
		creds.OwnerID = owner
	} else if authz := request.Header.Get("Authorization"); strings.HasPrefix(authz, "Bearer ") {
		secret := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
		if strings.HasPrefix(secret, "obr_") {
			creds.RouteToken = secret
		} else if h.auth != nil {
			ownerID, err := h.auth.AuthenticateBearer(request.Context(), secret)
			if err == nil {
				creds.OwnerID = ownerID
			}
		}
	} else if h.auth != nil {
		if cookie, err := request.Cookie(auth.SessionCookie); err == nil {
			ownerID, err := h.auth.AuthenticateSession(request.Context(), cookie.Value, "", false)
			if err == nil {
				creds.OwnerID = ownerID
			}
		}
	}
	if err := h.routes.AuthorizeAccess(request.Context(), hostname, creds); err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	response.WriteHeader(http.StatusOK)
}

func gatewayHostname(r *http.Request) string {
	if host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); host != "" {
		return stripHostPort(host)
	}
	return stripHostPort(r.Host)
}

func stripHostPort(host string) string {
	host = strings.TrimSpace(host)
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host[i:], "]") {
		// Only strip :port when not an IPv6 literal.
		if !strings.Contains(host, "]") {
			return host[:i]
		}
	}
	return host
}

func requestOwnerOptional(r *http.Request) domain.OwnerID {
	if v := r.Context().Value(principalKey{}); v != nil {
		if owner, ok := v.(domain.OwnerID); ok {
			return owner
		}
	}
	return ""
}
