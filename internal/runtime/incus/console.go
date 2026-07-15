// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// OpenConsole opens an interactive PTY inside a managed Incus instance via
// exec websockets (wait-for-websocket + interactive). Host targets are rejected.
func (a *Adapter) OpenConsole(ctx context.Context, request runtimeapi.ConsoleRequest) (runtimeapi.ConsoleSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.Ref == "" || runtimeapi.IsHostConsoleTarget(request.Ref) {
		return nil, runtimeapi.ErrHostTarget
	}
	command := append([]string(nil), request.Command...)
	if len(command) == 0 {
		command = []string{"/bin/bash"}
	}
	cols := request.Cols
	rows := request.Rows
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	payload := map[string]any{
		"command":            command,
		"wait-for-websocket": true,
		"interactive":        true,
		"width":              int(cols),
		"height":             int(rows),
		// tmux rejects PTY sessions when TERM is missing/unknown ("does not support clear").
		"environment": map[string]string{
			"TERM": "xterm-256color",
		},
	}
	query := url.Values{"project": {a.project}}
	envelope, err := a.call(ctx, a.timeout, http.MethodPost, "/1.0/instances/"+url.PathEscape(request.Ref)+"/exec", query, payload, nil)
	if err != nil {
		if isNotFound(err) {
			return nil, runtimeapi.ErrNotFound
		}
		return nil, fmt.Errorf("start Incus interactive exec: %w", err)
	}
	if envelope.Type != "async" || envelope.Operation == "" {
		return nil, fmt.Errorf("Incus interactive exec did not return an async operation")
	}
	fds, err := parseExecFDs(envelope.Metadata)
	if err != nil {
		return nil, err
	}
	dataSecret := fds["0"]
	controlSecret := fds["control"]
	if dataSecret == "" || controlSecret == "" {
		return nil, fmt.Errorf("Incus interactive exec missing websocket secrets")
	}
	opID := operationUUID(envelope.Operation)
	if opID == "" {
		return nil, fmt.Errorf("Incus interactive exec missing operation id")
	}

	dialCtx, cancelDial := context.WithTimeout(ctx, a.timeout)
	defer cancelDial()
	dataConn, err := a.dialOperationWebsocket(dialCtx, opID, dataSecret)
	if err != nil {
		return nil, fmt.Errorf("dial Incus exec data websocket: %w", err)
	}
	controlConn, err := a.dialOperationWebsocket(dialCtx, opID, controlSecret)
	if err != nil {
		_ = dataConn.Close(websocket.StatusInternalError, "control dial failed")
		return nil, fmt.Errorf("dial Incus exec control websocket: %w", err)
	}

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	session := &consoleSession{
		adapter:    a,
		operation:  envelope.Operation,
		data:       dataConn,
		control:    controlConn,
		stdin:      stdinW,
		stdout:     stdoutR,
		done:       make(chan struct{}),
		pumpDone:   make(chan struct{}),
		sessionCtx: context.Background(),
	}
	session.sessionCtx, session.cancel = context.WithCancel(context.Background())

	go session.pump(stdinR, stdoutW)
	go session.waitOperation()
	return session, nil
}

func (a *Adapter) dialOperationWebsocket(ctx context.Context, operationID, secret string) (*websocket.Conn, error) {
	query := url.Values{"secret": {secret}}
	if a.project != "" {
		query.Set("project", a.project)
	}
	endpoint := "ws://incus/1.0/operations/" + url.PathEscape(operationID) + "/websocket?" + query.Encode()
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: a.client})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(8 << 20)
	return conn, nil
}

func parseExecFDs(metadata json.RawMessage) (map[string]string, error) {
	if len(metadata) == 0 || string(metadata) == "null" {
		return nil, fmt.Errorf("Incus interactive exec metadata missing")
	}
	var op struct {
		Metadata struct {
			FDs map[string]string `json:"fds"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(metadata, &op); err != nil {
		return nil, fmt.Errorf("decode Incus exec operation: %w", err)
	}
	if len(op.Metadata.FDs) == 0 {
		// Some responses nest only the inner metadata object.
		var inner struct {
			FDs map[string]string `json:"fds"`
		}
		if err := json.Unmarshal(metadata, &inner); err == nil && len(inner.FDs) > 0 {
			return inner.FDs, nil
		}
		return nil, fmt.Errorf("Incus interactive exec fds missing")
	}
	return op.Metadata.FDs, nil
}

func operationUUID(operation string) string {
	operation = strings.TrimPrefix(operation, "/1.0/operations/")
	if i := strings.IndexByte(operation, '/'); i >= 0 {
		operation = operation[:i]
	}
	if i := strings.IndexByte(operation, '?'); i >= 0 {
		operation = operation[:i]
	}
	return operation
}

type consoleSession struct {
	adapter   *Adapter
	operation string
	data      *websocket.Conn
	control   *websocket.Conn
	stdin     *io.PipeWriter
	stdout    *io.PipeReader

	sessionCtx context.Context
	cancel     context.CancelFunc

	mu       sync.Mutex
	closed   bool
	exitCode int
	waitErr  error
	done     chan struct{}
	pumpDone chan struct{}
}

func (s *consoleSession) Stdin() io.WriteCloser { return s.stdin }
func (s *consoleSession) Stdout() io.Reader     { return s.stdout }

func (s *consoleSession) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	s.mu.Lock()
	closed := s.closed
	control := s.control
	s.mu.Unlock()
	if closed || control == nil {
		return io.ErrClosedPipe
	}
	payload, err := json.Marshal(map[string]any{
		"command": "window-resize",
		"args": map[string]string{
			"width":  strconv.Itoa(int(cols)),
			"height": strconv.Itoa(int(rows)),
		},
	})
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(s.sessionCtx, 5*time.Second)
	defer cancel()
	return control.Write(writeCtx, websocket.MessageText, payload)
}

func (s *consoleSession) Wait() (int, error) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitCode, s.waitErr
}

func (s *consoleSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	_ = s.stdin.Close()
	if s.data != nil {
		_ = s.data.Close(websocket.StatusNormalClosure, "closed")
	}
	if s.control != nil {
		_ = s.control.Close(websocket.StatusNormalClosure, "closed")
	}
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

func (s *consoleSession) pump(stdinR *io.PipeReader, stdoutW *io.PipeWriter) {
	defer close(s.pumpDone)
	defer stdinR.Close()
	defer stdoutW.Close()

	errCh := make(chan error, 2)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, readErr := stdinR.Read(buf)
			if n > 0 {
				writeCtx, cancel := context.WithTimeout(s.sessionCtx, 30*time.Second)
				writeErr := s.data.Write(writeCtx, websocket.MessageBinary, buf[:n])
				cancel()
				if writeErr != nil {
					errCh <- writeErr
					return
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					errCh <- readErr
				} else {
					errCh <- nil
				}
				return
			}
		}
	}()
	go func() {
		for {
			_, data, readErr := s.data.Read(s.sessionCtx)
			if len(data) > 0 {
				if _, writeErr := stdoutW.Write(data); writeErr != nil {
					errCh <- writeErr
					return
				}
			}
			if readErr != nil {
				if websocket.CloseStatus(readErr) == websocket.StatusNormalClosure || readErr == io.EOF || s.sessionCtx.Err() != nil {
					errCh <- nil
				} else {
					errCh <- readErr
				}
				return
			}
		}
	}()
	<-errCh
}

func (s *consoleSession) waitOperation() {
	// Interactive sessions can last far longer than ordinary operationTimeout.
	waitTimeout := 12 * time.Hour
	var result struct {
		StatusCode int `json:"status_code"`
		Metadata   struct {
			Return int `json:"return"`
		} `json:"metadata"`
		Err string `json:"err"`
	}
	err := s.adapter.waitOperationResultWithTimeout(s.sessionCtx, s.operation, waitTimeout, &result)
	if err != nil {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed || s.sessionCtx.Err() != nil {
			s.finish(0, nil)
			return
		}
		select {
		case <-s.pumpDone:
			s.finish(0, nil)
		default:
			s.finish(0, err)
		}
		return
	}
	if result.StatusCode >= 400 || result.Err != "" {
		s.finish(result.Metadata.Return, fmt.Errorf("Incus interactive exec failed: %s", result.Err))
		return
	}
	s.finish(result.Metadata.Return, nil)
}

func (s *consoleSession) finish(exitCode int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
		return
	default:
		s.exitCode = exitCode
		s.waitErr = err
		close(s.done)
	}
}

func (a *Adapter) waitOperationResultWithTimeout(ctx context.Context, operation string, timeout time.Duration, output any) error {
	envelope, err := a.call(ctx, timeout, http.MethodGet, operation+"/wait", nil, nil, nil)
	if err != nil {
		return fmt.Errorf("wait for Incus operation: %w", err)
	}
	if envelope.Type == "async" {
		return fmt.Errorf("wait for Incus operation returned another async operation")
	}
	if len(envelope.Metadata) == 0 || string(envelope.Metadata) == "null" {
		return fmt.Errorf("Incus operation metadata missing")
	}
	if err := json.Unmarshal(envelope.Metadata, output); err != nil {
		return fmt.Errorf("decode Incus operation metadata: %w", err)
	}
	return nil
}
