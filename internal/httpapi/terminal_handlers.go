// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/domain"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
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

	if h.console == nil {
		h.serveAuthorizedTerminalStub(request.Context(), conn, target)
		return
	}
	h.serveAuthorizedTerminal(request.Context(), conn, target)
}

func (h *Handler) authorizeTerminal(ctx context.Context, owner domain.OwnerID, instanceID domain.InstanceID) (authorizedTerminal, error) {
	instance, err := h.service.GetInstance(ctx, owner, instanceID)
	if err != nil {
		return authorizedTerminal{}, err
	}
	if strings.TrimSpace(instance.RuntimeRef) == "" {
		return authorizedTerminal{}, &domain.Error{Code: domain.CodeRuntimeMissing, Field: "runtime_ref"}
	}
	if runtimeapi.IsHostConsoleTarget(instance.RuntimeRef) {
		return authorizedTerminal{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "runtime_ref"}
	}
	return authorizedTerminal{
		OwnerID:    owner,
		InstanceID: instance.ID,
		RuntimeRef: instance.RuntimeRef,
	}, nil
}

func (h *Handler) serveAuthorizedTerminalStub(ctx context.Context, conn *websocket.Conn, target authorizedTerminal) {
	// Without a Console opener, prove the upgrade completed after ownership
	// resolution without exposing RuntimeRef.
	_ = target.RuntimeRef
	h.writeTerminalError(ctx, conn, "not_implemented", "terminal session is not available yet")
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func (h *Handler) serveAuthorizedTerminal(ctx context.Context, conn *websocket.Conn, target authorizedTerminal) {
	open, err := h.readTerminalOpen(ctx, conn)
	if err != nil {
		h.writeTerminalError(ctx, conn, "invalid_frame", "expected open frame")
		_ = conn.Close(websocket.StatusPolicyViolation, "open")
		return
	}

	session, err := h.console.OpenConsole(ctx, runtimeapi.ConsoleRequest{
		Ref:     target.RuntimeRef,
		Command: []string{"/bin/bash"},
		Cols:    open.Cols,
		Rows:    open.Rows,
	})
	if err != nil {
		code, message := terminalConsoleError(err)
		h.writeTerminalError(ctx, conn, code, message)
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return
	}
	defer session.Close()

	ack, err := terminal.Encode(terminal.OpenFrame{
		InstanceID: string(target.InstanceID),
		Cols:       open.Cols,
		Rows:       open.Rows,
	})
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "encode")
		return
	}
	if err := conn.Write(ctx, websocket.MessageText, ack); err != nil {
		return
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)
	go func() {
		errCh <- pipeConsoleOutput(sessionCtx, conn, session.Stdout())
	}()
	go func() {
		errCh <- pumpTerminalInput(sessionCtx, conn, session)
	}()

	select {
	case <-sessionCtx.Done():
	case err := <-errCh:
		cancel()
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
			return
		}
	}

	exitCode, waitErr := session.Wait()
	if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
		h.writeTerminalError(ctx, conn, "console_failed", "console session ended unexpectedly")
		return
	}
	payload, err := terminal.Encode(terminal.ExitFrame{Code: exitCode})
	if err != nil {
		return
	}
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer writeCancel()
	_ = conn.Write(writeCtx, websocket.MessageText, payload)
}

func (h *Handler) readTerminalOpen(ctx context.Context, conn *websocket.Conn) (terminal.OpenFrame, error) {
	readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		return terminal.OpenFrame{}, err
	}
	frame, err := terminal.Decode(data)
	if err != nil {
		return terminal.OpenFrame{}, err
	}
	open, ok := frame.(terminal.OpenFrame)
	if !ok {
		return terminal.OpenFrame{}, errors.New("first frame must be open")
	}
	return open, nil
}

func pipeConsoleOutput(ctx context.Context, conn *websocket.Conn, stdout io.Reader) error {
	buf := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := stdout.Read(buf)
		if n > 0 {
			payload, encodeErr := terminal.Encode(terminal.OutputFrame{Data: append([]byte(nil), buf[:n]...)})
			if encodeErr != nil {
				return encodeErr
			}
			if writeErr := conn.Write(ctx, websocket.MessageText, payload); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func pumpTerminalInput(ctx context.Context, conn *websocket.Conn, session runtimeapi.ConsoleSession) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			_ = session.Close()
			return err
		}
		frame, err := terminal.Decode(data)
		if err != nil {
			continue
		}
		switch f := frame.(type) {
		case terminal.InputFrame:
			if len(f.Data) == 0 {
				continue
			}
			if _, err := session.Stdin().Write(f.Data); err != nil {
				return err
			}
		case terminal.ResizeFrame:
			if err := session.Resize(f.Cols, f.Rows); err != nil {
				return err
			}
		case terminal.DetachFrame:
			_ = session.Close()
			return nil
		case terminal.SignalFrame:
			// Signal delivery is deepened in a later task; closing stdin is enough
			// for the fake echo console to finish cleanly on TERM-like intent.
			if strings.EqualFold(f.Signal, "TERM") || strings.EqualFold(f.Signal, "KILL") {
				_ = session.Close()
				return nil
			}
		default:
			// Ignore protocol frames that are not input/control for this task.
		}
	}
}

func terminalConsoleError(err error) (code, message string) {
	switch {
	case errors.Is(err, runtimeapi.ErrHostTarget):
		return "invalid_argument", "terminal cannot target the host"
	case errors.Is(err, runtimeapi.ErrNotFound):
		return "runtime_missing", "instance runtime is unavailable"
	case errors.Is(err, runtimeapi.ErrUnsupported):
		return "unsupported", "interactive console is not available"
	default:
		return "console_failed", "failed to open console"
	}
}

func (h *Handler) writeTerminalError(ctx context.Context, conn *websocket.Conn, code, message string) {
	payload, err := terminal.Encode(terminal.ErrorFrame{Code: code, Message: message})
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "encode")
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = conn.Write(writeCtx, websocket.MessageText, payload)
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

func isWebSocketHandshake(request *http.Request) bool {
	return request.Method == http.MethodGet && isWebSocketUpgrade(request)
}

// sessionCSRFToken prefers X-CSRF-Token. For WebSocket handshakes only, browsers
// may supply the same token via ?csrf= because the WebSocket API cannot set
// custom headers. Non-WebSocket cookie mutations still require the header.
func sessionCSRFToken(request *http.Request) string {
	if token := request.Header.Get(auth.CSRFHeader); token != "" {
		return token
	}
	if isWebSocketHandshake(request) {
		return request.URL.Query().Get(auth.CSRFQuery)
	}
	return ""
}
