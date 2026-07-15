// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/terminal"
)

// authorizedTerminal is the post-auth target for a browser terminal session.
// RuntimeRef is always taken from the owned OpenBox instance record — never from
// a client-supplied Incus identity.
type authorizedTerminal struct {
	OwnerID    domain.OwnerID
	InstanceID domain.InstanceID
	RuntimeRef string
}

func (h *Handler) openTerminal(response http.ResponseWriter, request *http.Request, requestID, rawID string) {
	if !terminalOriginAllowed(request) {
		h.writeError(response, requestID, http.StatusForbidden, "forbidden", "origin")
		return
	}

	target, err := h.authorizeTerminal(request.Context(), h.requestOwner(request), domain.InstanceID(rawID))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}

	conn, err := websocket.Accept(response, request, &websocket.AcceptOptions{
		// Origin was validated above against the request host.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	h.serveAuthorizedTerminalStub(request.Context(), conn, target)
}

func (h *Handler) authorizeTerminal(ctx context.Context, owner domain.OwnerID, instanceID domain.InstanceID) (authorizedTerminal, error) {
	instance, err := h.service.GetInstance(ctx, owner, instanceID)
	if err != nil {
		return authorizedTerminal{}, err
	}
	return authorizedTerminal{
		OwnerID:    owner,
		InstanceID: instance.ID,
		RuntimeRef: instance.RuntimeRef,
	}, nil
}

func (h *Handler) serveAuthorizedTerminalStub(ctx context.Context, conn *websocket.Conn, target authorizedTerminal) {
	// Task 3 opens an Incus PTY against target.RuntimeRef only. This stub proves
	// the upgrade completed after ownership resolution without exposing that ref.
	_ = target.RuntimeRef

	payload, err := terminal.Encode(terminal.ErrorFrame{
		Code:    "not_implemented",
		Message: "terminal session is not available yet",
	})
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "encode")
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.Write(writeCtx, websocket.MessageText, payload); err != nil {
		return
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func terminalOriginAllowed(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, request.Host)
}

func isWebSocketUpgrade(request *http.Request) bool {
	return strings.EqualFold(request.Header.Get("Upgrade"), "websocket")
}

// sessionCSRFToken prefers X-CSRF-Token. For WebSocket upgrades only, browsers
// may supply the same token via ?csrf= because the WebSocket API cannot set
// custom headers. Non-WebSocket cookie mutations still require the header.
func sessionCSRFToken(request *http.Request) string {
	if token := request.Header.Get(auth.CSRFHeader); token != "" {
		return token
	}
	if isWebSocketUpgrade(request) {
		return request.URL.Query().Get(auth.CSRFQuery)
	}
	return ""
}
