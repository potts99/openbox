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
	"github.com/openbox-dev/openbox/internal/pi"
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
	// Cookie sessions have ambient authority — require matching Origin.
	// Bearer tokens are explicit credentials with no cookie CSRF surface.
	if !terminalAuthUsesBearer(request) && !terminalOriginAllowed(request) {
		h.writeError(response, requestID, http.StatusForbidden, "forbidden", "origin")
		return
	}

	target, err := h.authorizeTerminal(request.Context(), h.requestOwner(request), domain.InstanceID(rawID))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}

	release, err := h.terminalSessions.Acquire(string(target.OwnerID), string(target.InstanceID))
	if err != nil {
		h.writeError(response, requestID, http.StatusTooManyRequests, "session_limit", "terminal")
		return
	}
	defer release()

	conn, err := websocket.Accept(response, request, &websocket.AcceptOptions{
		// Origin was validated above against the request host.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	conn.SetReadLimit(int64(h.terminalLimits.MaxFrameBytes))

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
	limits := h.terminalLimits
	rate := terminal.NewInboundLimiter(limits.MaxInboundFramesPerWindow, limits.MaxInboundBytesPerWindow, limits.RateWindow)
	idle := terminal.NewIdleWatch(limits.IdleTimeout)
	idle.Touch(time.Now())

	start, err := h.readTerminalStart(ctx, conn, rate, idle, limits.MaxFrameBytes)
	if err != nil {
		h.closeTerminalLimit(ctx, conn, err)
		return
	}

	session, sessionName, sessionID, cols, rows, persisted, err := h.resolveTerminalConsole(ctx, target, start)
	if err != nil {
		code, message := terminalResolveError(err)
		h.writeTerminalError(ctx, conn, code, message)
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return
	}

	base := TerminalAuditEvent{
		OwnerID:     target.OwnerID,
		InstanceID:  target.InstanceID,
		SessionID:   sessionID,
		SessionName: sessionName,
	}
	startEvt := base
	startEvt.Phase = TerminalAuditPhaseStart
	h.recordTerminalAudit(ctx, startEvt)
	endReason := TerminalAuditReasonCanceled
	defer func() {
		endEvt := base
		endEvt.Phase = TerminalAuditPhaseEnd
		endEvt.Reason = endReason
		h.recordTerminalAudit(context.Background(), endEvt)
	}()

	// Named detach / reconnect leaves the console open in persistentConsoles.
	// Ephemeral sessions and explicit terminate always Close.
	closeConsole := true
	defer func() {
		if !closeConsole {
			return
		}
		if sessionID != "" {
			h.persistentConsoles.purgeAndClose(sessionID)
			return
		}
		_ = session.Close()
	}()

	ack, err := terminal.Encode(terminal.OpenFrame{
		InstanceID:  string(target.InstanceID),
		Cols:        cols,
		Rows:        rows,
		SessionName: sessionName,
		SessionID:   sessionID,
	})
	if err != nil {
		endReason = TerminalAuditReasonError
		_ = conn.Close(websocket.StatusInternalError, "encode")
		return
	}
	if err := conn.Write(ctx, websocket.MessageText, ack); err != nil {
		endReason = TerminalAuditReasonCanceled
		return
	}
	idle.Touch(time.Now())

	stdout := io.Reader(session.Stdout())
	if persisted != nil {
		stdout = persisted.attachOutput()
		defer persisted.detachOutput()
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	idleCh := make(chan struct{}, 1)
	go watchTerminalIdle(sessionCtx, idle, func() {
		select {
		case idleCh <- struct{}{}:
		default:
		}
		// Do not cancel sessionCtx here: canceling the WebSocket Read context
		// makes coder/websocket force-close the conn before we can write idle_timeout.
	})

	errCh := make(chan error, 2)
	go func() {
		errCh <- pipeConsoleOutput(sessionCtx, conn, stdout, idle)
	}()
	go func() {
		errCh <- pumpTerminalInput(sessionCtx, conn, session, sessionName, rate, idle, limits.MaxFrameBytes)
	}()

	select {
	case <-idleCh:
		endReason = h.closeTerminalLimit(ctx, conn, terminal.ErrIdleTimeout)
		cancel()
		return
	case <-sessionCtx.Done():
		endReason = TerminalAuditReasonCanceled
		return
	case err := <-errCh:
		cancel()
		if errors.Is(err, errTerminalDetached) {
			// Named persistent session: close the WebSocket only; console stays
			// registered for session_id / session_name reattach. detachOutput
			// (deferred) returns the stdout pump to discard mode.
			closeConsole = false
			endReason = TerminalAuditReasonDetach
			if sessionID != "" {
				h.persistentConsoles.markDetached(sessionID)
			}
			_ = conn.Close(websocket.StatusNormalClosure, "detached")
			return
		}
		if err != nil && isTerminalLimitError(err) {
			endReason = h.closeTerminalLimit(ctx, conn, err)
			return
		}
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
			endReason = TerminalAuditReasonError
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return
		}
	}

	exitCode, waitErr := session.Wait()
	if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
		endReason = TerminalAuditReasonError
		h.writeTerminalError(ctx, conn, "console_failed", "console session ended unexpectedly")
		return
	}
	endReason = TerminalAuditReasonExit
	payload, err := terminal.Encode(terminal.ExitFrame{Code: exitCode})
	if err != nil {
		return
	}
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer writeCancel()
	_ = conn.Write(writeCtx, websocket.MessageText, payload)
}

// errTerminalDetached is returned by the input pump when the client detaches a
// named session (or the WebSocket ends without terminate). The bridge must close
// the WebSocket without terminating the console.
var errTerminalDetached = errors.New("terminal detached")

var (
	errTerminalSessionNotFound = errors.New("terminal session not found")
	errTerminalSessionBusy     = errors.New("terminal session busy")
)

func (h *Handler) resolveTerminalConsole(
	ctx context.Context,
	target authorizedTerminal,
	start terminal.Frame,
) (session runtimeapi.ConsoleSession, sessionName, sessionID string, cols, rows uint16, persisted *persistentConsole, err error) {
	switch f := start.(type) {
	case terminal.ReconnectFrame:
		return h.reattachPersistentConsole(target, f.SessionID, 0, 0, false)
	case terminal.OpenFrame:
		cols, rows = f.Cols, f.Rows
		sessionName = strings.TrimSpace(f.SessionName)
		if sessionName != "" {
			if existing := h.persistentConsoles.getByName(string(target.OwnerID), string(target.InstanceID), sessionName); existing != nil {
				return h.claimPersistentConsole(existing, cols, rows, true)
			}
			return h.openPersistentConsole(ctx, target, sessionName, f.WorkingDirectory, cols, rows)
		}
		command, cmdErr := terminal.CommandForSession("")
		if cmdErr != nil {
			return nil, "", "", 0, 0, nil, cmdErr
		}
		session, err = h.console.OpenConsole(ctx, runtimeapi.ConsoleRequest{
			Ref: target.RuntimeRef, Command: command, Cols: cols, Rows: rows,
		})
		return session, "", "", cols, rows, nil, err
	default:
		return nil, "", "", 0, 0, nil, errors.New("first frame must be open or reconnect")
	}
}

func (h *Handler) reattachPersistentConsole(
	target authorizedTerminal,
	sessionID string,
	cols, rows uint16,
	resize bool,
) (runtimeapi.ConsoleSession, string, string, uint16, uint16, *persistentConsole, error) {
	entry := h.persistentConsoles.getByID(sessionID)
	if entry == nil ||
		entry.ownerID != string(target.OwnerID) ||
		entry.instanceID != string(target.InstanceID) {
		return nil, "", "", 0, 0, nil, errTerminalSessionNotFound
	}
	return h.claimPersistentConsole(entry, cols, rows, resize)
}

func (h *Handler) claimPersistentConsole(
	entry *persistentConsole,
	cols, rows uint16,
	resize bool,
) (runtimeapi.ConsoleSession, string, string, uint16, uint16, *persistentConsole, error) {
	if !h.persistentConsoles.claimAttached(entry) {
		return nil, "", "", 0, 0, nil, errTerminalSessionBusy
	}
	if resize {
		if err := entry.console.Resize(cols, rows); err != nil {
			h.persistentConsoles.markDetached(entry.id)
			return nil, "", "", 0, 0, nil, err
		}
	} else {
		cols, rows = 0, 0
	}
	// Keep ack dimensions meaningful when reconnect omits resize.
	if cols == 0 && rows == 0 {
		cols, rows = 80, 24
	}
	return entry.console, entry.sessionName, entry.id, cols, rows, entry, nil
}

func (h *Handler) openPersistentConsole(
	ctx context.Context,
	target authorizedTerminal,
	sessionName string,
	workingDirectory string,
	cols, rows uint16,
) (runtimeapi.ConsoleSession, string, string, uint16, uint16, *persistentConsole, error) {
	command, err := consoleCommandForSession(sessionName, workingDirectory)
	if err != nil {
		return nil, "", "", 0, 0, nil, err
	}
	sessionID, err := newTerminalSessionID()
	if err != nil {
		return nil, "", "", 0, 0, nil, err
	}
	session, err := h.console.OpenConsole(ctx, runtimeapi.ConsoleRequest{
		Ref: target.RuntimeRef, Command: command, Cols: cols, Rows: rows,
	})
	if err != nil {
		return nil, "", "", 0, 0, nil, err
	}
	entry := &persistentConsole{
		id:          sessionID,
		sessionName: sessionName,
		ownerID:     string(target.OwnerID),
		instanceID:  string(target.InstanceID),
		console:     session,
		attached:    true,
	}
	entry.startStdoutPump(func() {
		h.persistentConsoles.purgeAndClose(sessionID)
	})
	h.persistentConsoles.put(entry)
	return session, sessionName, sessionID, cols, rows, entry, nil
}

func consoleCommandForSession(sessionName, workingDirectory string) ([]string, error) {
	if pi.IsLaunchSession(sessionName) {
		return pi.AttachOrCreateCommand(workingDirectory)
	}
	return terminal.CommandForSession(sessionName)
}

func terminalResolveError(err error) (code, message string) {
	switch {
	case errors.Is(err, terminal.ErrInvalidFrame):
		return "invalid_argument", "invalid session_name"
	case errors.Is(err, errTerminalSessionNotFound):
		return "not_found", "terminal session not found"
	case errors.Is(err, errTerminalSessionBusy):
		return "conflict", "terminal session is already attached"
	default:
		return terminalConsoleError(err)
	}
}

func (h *Handler) readTerminalStart(
	ctx context.Context,
	conn *websocket.Conn,
	rate *terminal.InboundLimiter,
	idle *terminal.IdleWatch,
	maxFrameBytes int,
) (terminal.Frame, error) {
	// Hard bound waiting for the first frame. coder/websocket closes the
	// connection when a Read context expires — acceptable before a session starts.
	readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		if errors.Is(err, websocket.ErrMessageTooBig) {
			return nil, terminal.ErrFrameTooLarge
		}
		return nil, err
	}
	if err := terminal.CheckFrameSize(len(data), maxFrameBytes); err != nil {
		return nil, err
	}
	if err := rate.Allow(time.Now(), len(data)); err != nil {
		return nil, err
	}
	frame, err := terminal.Decode(data)
	if err != nil {
		return nil, err
	}
	switch frame.(type) {
	case terminal.OpenFrame, terminal.ReconnectFrame:
		idle.Touch(time.Now())
		return frame, nil
	default:
		return nil, errors.New("first frame must be open or reconnect")
	}
}

// watchTerminalIdle invokes onExpire once when the idle watch expires.
// It must not cancel WebSocket Read contexts — coder/websocket force-closes
// the connection when a Read context times out.
func watchTerminalIdle(ctx context.Context, idle *terminal.IdleWatch, onExpire func()) {
	interval := 50 * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if idle.Expired(now) {
				onExpire()
				return
			}
		}
	}
}

func readTerminalMessage(ctx context.Context, conn *websocket.Conn, idle *terminal.IdleWatch) ([]byte, error) {
	_, data, err := conn.Read(ctx)
	if err != nil {
		if errors.Is(err, websocket.ErrMessageTooBig) {
			return nil, terminal.ErrFrameTooLarge
		}
		if idle.Expired(time.Now()) {
			return nil, terminal.ErrIdleTimeout
		}
		return nil, err
	}
	idle.Touch(time.Now())
	return data, nil
}

func pipeConsoleOutput(
	ctx context.Context,
	conn *websocket.Conn,
	stdout io.Reader,
	idle *terminal.IdleWatch,
) error {
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
			idle.Touch(time.Now())
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func pumpTerminalInput(
	ctx context.Context,
	conn *websocket.Conn,
	session runtimeapi.ConsoleSession,
	sessionName string,
	rate *terminal.InboundLimiter,
	idle *terminal.IdleWatch,
	maxFrameBytes int,
) error {
	for {
		data, err := readTerminalMessage(ctx, conn, idle)
		if err != nil {
			// Do not Close the console here: limit errors must win the bridge
			// select before stdout EOF from a premature session.Close.
			// Named sessions treat WebSocket loss as detach so tab close keeps
			// the guest tmux session (and daemon-side console) alive.
			if sessionName != "" && !isTerminalLimitError(err) {
				return errTerminalDetached
			}
			return err
		}
		if err := terminal.CheckFrameSize(len(data), maxFrameBytes); err != nil {
			return err
		}
		if err := rate.Allow(time.Now(), len(data)); err != nil {
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
			if _, writeErr := session.Stdin().Write(f.Data); writeErr != nil {
				return writeErr
			}
		case terminal.ResizeFrame:
			if err := session.Resize(f.Cols, f.Rows); err != nil {
				return err
			}
		case terminal.DetachFrame:
			// Named sessions survive detach for reconnect. Ephemeral sessions
			// have no reconnect target, so detach terminates.
			if sessionName != "" {
				return errTerminalDetached
			}
			_ = session.Close()
			return nil
		case terminal.SignalFrame:
			// Explicit terminate cancels the console; exit status is propagated
			// by the bridge after Wait returns.
			if strings.EqualFold(f.Signal, "TERM") || strings.EqualFold(f.Signal, "KILL") {
				_ = session.Close()
				return nil
			}
		default:
			// Ignore protocol frames that are not input/control for this task.
		}
	}
}

func isTerminalLimitError(err error) bool {
	return errors.Is(err, terminal.ErrFrameTooLarge) ||
		errors.Is(err, terminal.ErrRateLimited) ||
		errors.Is(err, terminal.ErrIdleTimeout)
}

// closeTerminalLimit writes a typed error frame, closes the socket, and returns
// the matching audit end reason.
func (h *Handler) closeTerminalLimit(_ context.Context, conn *websocket.Conn, err error) string {
	code, message := "invalid_frame", "expected open frame"
	status, reason := websocket.StatusPolicyViolation, "open"
	auditReason := TerminalAuditReasonError
	switch {
	case errors.Is(err, terminal.ErrFrameTooLarge):
		code, message = "frame_too_large", "frame exceeds size limit"
		status, reason = websocket.StatusMessageTooBig, "frame"
		auditReason = TerminalAuditReasonFrameTooLarge
	case errors.Is(err, terminal.ErrRateLimited):
		code, message = "rate_limited", "inbound rate limit exceeded"
		status, reason = websocket.StatusPolicyViolation, "rate"
		auditReason = TerminalAuditReasonRateLimited
	case errors.Is(err, terminal.ErrIdleTimeout):
		code, message = "idle_timeout", "session idle timeout"
		status, reason = websocket.StatusPolicyViolation, "idle"
		auditReason = TerminalAuditReasonIdleTimeout
	}
	// Detached write context so request/session cancel cannot skip the limit frame.
	writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.writeTerminalError(writeCtx, conn, code, message)
	_ = conn.Close(status, reason)
	return auditReason
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
		if err != nil && err.Error() != "" {
			return "console_failed", err.Error()
		}
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

func terminalAuthUsesBearer(request *http.Request) bool {
	return strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ")
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
