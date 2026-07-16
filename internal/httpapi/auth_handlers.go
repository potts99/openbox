// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/domain"
)

type principalKey struct{}

func isPublicAuthRoute(segments []string, method string) bool {
	if len(segments) == 2 {
		if segments[1] == "health" || segments[1] == "bootstrap" {
			return true
		}
		return segments[1] == "sessions" && method == http.MethodPost
	}
	// Caddy on-demand TLS ask — no owner credentials; loopback/API bind is the trust boundary.
	if len(segments) == 3 && segments[1] == "certificates" && segments[2] == "allow" && method == http.MethodGet {
		return true
	}
	// Caddy forward_auth for HTTPS routes — authenticates via cookie/bearer itself.
	if len(segments) == 3 && segments[1] == "gateway" && segments[2] == "auth" && method == http.MethodGet {
		return true
	}
	return false
}

func (h *Handler) authenticate(r *http.Request) (*http.Request, error) {
	authorization := r.Header.Get("Authorization")
	if strings.HasPrefix(authorization, "Bearer ") {
		owner, err := h.auth.AuthenticateBearer(r.Context(), strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")))
		if err != nil {
			return r, err
		}
		return r.WithContext(context.WithValue(r.Context(), principalKey{}, owner)), nil
	}
	cookie, err := r.Cookie(auth.SessionCookie)
	if err != nil {
		return r, auth.ErrUnauthenticated
	}
	// Cookie-authenticated WebSocket handshakes are state-changing: require CSRF
	// even though the handshake uses GET.
	mutation := (r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions) || isWebSocketHandshake(r)
	owner, err := h.auth.AuthenticateSession(r.Context(), cookie.Value, sessionCSRFToken(r), mutation)
	if err != nil {
		return r, err
	}
	return r.WithContext(context.WithValue(r.Context(), principalKey{}, owner)), nil
}

func (h *Handler) requestOwner(r *http.Request) domain.OwnerID {
	if owner, ok := r.Context().Value(principalKey{}).(domain.OwnerID); ok {
		return owner
	}
	return h.fixedOwnerID
}

func (h *Handler) routeBootstrap(w http.ResponseWriter, r *http.Request, requestID string) bool {
	if h.auth == nil {
		h.writeError(w, requestID, http.StatusNotFound, string(domain.CodeNotFound), "path")
		return true
	}
	switch r.Method {
	case http.MethodGet:
		status, err := h.auth.BootstrapStatus(r.Context())
		if err != nil {
			h.writeServiceError(w, requestID, err)
			return true
		}
		h.writeJSON(w, http.StatusOK, status)
	case http.MethodPost:
		if !h.safeCredentialTransport(r) {
			h.writeError(w, requestID, http.StatusForbidden, "insecure_transport", "transport")
			return true
		}
		var input struct {
			Secret   string `json:"secret"`
			Password string `json:"password"`
		}
		if h.decodeJSON(w, r, &input) != nil {
			h.writeError(w, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
			return true
		}
		session, secret, err := h.auth.Bootstrap(r.Context(), h.clientAddress(r), input.Secret, input.Password)
		if err != nil {
			h.writeAuthError(w, requestID, err)
			return true
		}
		h.setSessionCookie(w, r, secret, session.ExpiresAt)
		h.writeJSON(w, http.StatusCreated, session)
	default:
		h.methodNotAllowed(w, requestID, http.MethodGet, http.MethodPost)
	}
	return true
}
func (h *Handler) routeSessions(w http.ResponseWriter, r *http.Request, requestID string) bool {
	if h.auth == nil {
		return false
	}
	if !h.requireMethod(w, r, requestID, http.MethodPost) {
		return true
	}
	if !h.safeCredentialTransport(r) {
		h.writeError(w, requestID, http.StatusForbidden, "insecure_transport", "transport")
		return true
	}
	var input struct {
		Password string `json:"password"`
	}
	if h.decodeJSON(w, r, &input) != nil {
		h.writeError(w, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
		return true
	}
	session, secret, err := h.auth.Login(r.Context(), h.clientAddress(r), input.Password)
	if err != nil {
		h.writeAuthError(w, requestID, err)
		return true
	}
	h.setSessionCookie(w, r, secret, session.ExpiresAt)
	h.writeJSON(w, http.StatusCreated, session)
	return true
}
func (h *Handler) routeSession(w http.ResponseWriter, r *http.Request, requestID string, rest []string) bool {
	if h.auth == nil {
		return false
	}
	if len(rest) != 0 {
		return false
	}
	switch r.Method {
	case http.MethodGet:
		cookie, _ := r.Cookie(auth.SessionCookie)
		if cookie == nil {
			h.writeError(w, requestID, http.StatusUnauthorized, "unauthenticated", "authorization")
			return true
		}
		session, err := h.auth.RefreshCSRF(r.Context(), cookie.Value)
		if err != nil {
			h.writeAuthError(w, requestID, err)
			return true
		}
		h.writeJSON(w, http.StatusOK, session)
	case http.MethodDelete:
		c, _ := r.Cookie(auth.SessionCookie)
		if c != nil {
			_ = h.auth.Logout(r.Context(), c.Value)
		}
		http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: h.requestIsSecure(r)})
		w.WriteHeader(http.StatusNoContent)
	default:
		h.methodNotAllowed(w, requestID, http.MethodGet, http.MethodDelete)
	}
	return true
}
func (h *Handler) routeTokens(w http.ResponseWriter, r *http.Request, requestID string, rest []string) bool {
	if h.auth == nil {
		return false
	}
	owner := h.requestOwner(r)
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			v, e := h.auth.ListTokens(r.Context(), owner)
			if e != nil {
				h.writeServiceError(w, requestID, e)
				return true
			}
			h.writeJSON(w, http.StatusOK, map[string]any{"items": v})
		case http.MethodPost:
			var in struct {
				Name      string     `json:"name"`
				Scopes    []string   `json:"scopes"`
				ExpiresAt *time.Time `json:"expires_at"`
			}
			if h.decodeJSON(w, r, &in) != nil {
				h.writeError(w, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
				return true
			}
			v, e := h.auth.CreateToken(r.Context(), owner, in.Name, in.Scopes, in.ExpiresAt)
			if e != nil {
				h.writeError(w, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "token")
				return true
			}
			h.writeJSON(w, http.StatusCreated, v)
		default:
			h.methodNotAllowed(w, requestID, http.MethodGet, http.MethodPost)
		}
		return true
	}
	if len(rest) == 1 && r.Method == http.MethodDelete {
		if e := h.auth.RevokeToken(r.Context(), owner, rest[0]); e != nil {
			h.writeServiceError(w, requestID, e)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}
func (h *Handler) routeSSHKeys(w http.ResponseWriter, r *http.Request, requestID string, rest []string) bool {
	if h.auth == nil {
		return false
	}
	owner := h.requestOwner(r)
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			v, e := h.auth.ListSSHKeys(r.Context(), owner)
			if e != nil {
				h.writeServiceError(w, requestID, e)
				return true
			}
			h.writeJSON(w, http.StatusOK, map[string]any{"items": v})
		case http.MethodPost:
			var in struct {
				Label     string `json:"label"`
				PublicKey string `json:"public_key"`
			}
			if h.decodeJSON(w, r, &in) != nil {
				h.writeError(w, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
				return true
			}
			v, e := h.auth.AddSSHKey(r.Context(), owner, in.Label, in.PublicKey)
			if e != nil {
				h.writeError(w, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "public_key")
				return true
			}
			h.writeJSON(w, http.StatusCreated, v)
		default:
			h.methodNotAllowed(w, requestID, http.MethodGet, http.MethodPost)
		}
		return true
	}
	if len(rest) == 1 {
		switch r.Method {
		case http.MethodPatch:
			var in struct {
				Label string `json:"label"`
			}
			if h.decodeJSON(w, r, &in) != nil {
				h.writeError(w, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
				return true
			}
			key, e := h.auth.UpdateSSHKey(r.Context(), owner, rest[0], in.Label)
			if e != nil {
				var domainErr *domain.Error
				if errors.As(e, &domainErr) {
					h.writeServiceError(w, requestID, e)
				} else {
					h.writeError(w, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "label")
				}
				return true
			}
			h.writeJSON(w, http.StatusOK, key)
			return true
		case http.MethodDelete:
			if e := h.auth.DeleteSSHKey(r.Context(), owner, rest[0]); e != nil {
				h.writeServiceError(w, requestID, e)
				return true
			}
			w.WriteHeader(http.StatusNoContent)
			return true
		default:
			h.methodNotAllowed(w, requestID, http.MethodPatch, http.MethodDelete)
			return true
		}
	}
	return false
}

func (h *Handler) setSessionCookie(w http.ResponseWriter, r *http.Request, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: value, Path: "/", Expires: expires, MaxAge: h.auth.CookieMaxAge(expires), HttpOnly: true, Secure: h.requestIsSecure(r), SameSite: http.SameSiteStrictMode})
}
func (h *Handler) safeCredentialTransport(r *http.Request) bool {
	if h.requestIsSecure(r) {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}
func (h *Handler) clientAddress(r *http.Request) string {
	if h.isTrustedProxy(r) {
		if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(forwarded) != nil {
			return forwarded
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (h *Handler) requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return h.isTrustedProxy(r) && strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func (h *Handler) isTrustedProxy(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, network := range h.trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
func (h *Handler) writeAuthError(w http.ResponseWriter, id string, err error) {
	switch {
	case errors.Is(err, auth.ErrRateLimited):
		w.Header().Set("Retry-After", "900")
		h.writeError(w, id, http.StatusTooManyRequests, "rate_limited", "password")
	case errors.Is(err, auth.ErrBootstrapUnavailable):
		h.writeError(w, id, http.StatusConflict, "bootstrap_unavailable", "secret")
	default:
		h.writeError(w, id, http.StatusUnauthorized, "unauthenticated", "password")
	}
}
