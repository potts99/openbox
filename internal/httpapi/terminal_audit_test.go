// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/terminal"
)

// Unique marker that must never appear in audit metadata or application logs.
const terminalPayloadMarker = "UNIQUE_PTY_MARKER_7f3a9c_do_not_log"

func TestTerminalAuditRecordsStartEndWithoutPayloads(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	audit := &memoryTerminalAudit{}

	var logBuf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prev) })

	env := newTerminalTestEnvWithOptions(t, Options{
		Console:        rt,
		TerminalLimits: terminal.DefaultLimits(),
		TerminalAudit:  audit,
	})
	conn, ctx, ack := env.dialOpen(t, terminal.OpenFrame{
		InstanceID:  "inst-owned",
		Cols:        80,
		Rows:        24,
		SessionName: "audit-proof",
	})
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, mustEncodeTerminal(t, terminal.InputFrame{
		Data: []byte(terminalPayloadMarker),
	})); err != nil {
		t.Fatal(err)
	}

	_ = readTerminalFrameUntil(t, conn, ctx, func(f terminal.Frame) bool {
		out, ok := f.(terminal.OutputFrame)
		return ok && bytes.Contains(out.Data, []byte(terminalPayloadMarker))
	})

	if err := conn.Write(ctx, websocket.MessageText, mustEncodeTerminal(t, terminal.DetachFrame{})); err != nil {
		t.Fatal(err)
	}
	waitTerminalClosed(t, conn)

	deadline := time.Now().Add(2 * time.Second)
	var events []TerminalAuditEvent
	for time.Now().Before(deadline) {
		events = audit.snapshot()
		if len(events) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(events) < 2 {
		t.Fatalf("want start+end audit events, got %d: %#v", len(events), events)
	}

	var start, end *TerminalAuditEvent
	for i := range events {
		switch events[i].Phase {
		case TerminalAuditPhaseStart:
			start = &events[i]
		case TerminalAuditPhaseEnd:
			end = &events[i]
		}
	}
	if start == nil || end == nil {
		t.Fatalf("missing start/end: %#v", events)
	}
	if start.OwnerID != "owner-local" || start.InstanceID != "inst-owned" {
		t.Fatalf("start metadata=%+v", start)
	}
	if start.SessionName != "audit-proof" {
		t.Fatalf("start session_name=%q", start.SessionName)
	}
	if start.SessionID == "" || start.SessionID != ack.SessionID {
		t.Fatalf("start session_id=%q ack=%q", start.SessionID, ack.SessionID)
	}
	if end.Reason != TerminalAuditReasonDetach {
		t.Fatalf("end reason=%q want %q", end.Reason, TerminalAuditReasonDetach)
	}
	if end.OwnerID != start.OwnerID || end.InstanceID != start.InstanceID || end.SessionID != start.SessionID {
		t.Fatalf("end metadata mismatch start=%+v end=%+v", start, end)
	}

	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(terminalPayloadMarker)) {
		t.Fatalf("audit sink received PTY payload bytes: %s", encoded)
	}
	if strings.Contains(logBuf.String(), terminalPayloadMarker) {
		t.Fatalf("application log received PTY payload bytes: %q", logBuf.String())
	}

	t.Cleanup(func() {
		if s := rt.ActiveConsole("incus-owned-ref"); s != nil {
			_ = s.Close()
		}
	})
}

func TestTerminalAuditEndReasonExit(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	audit := &memoryTerminalAudit{}
	env := newTerminalTestEnvWithOptions(t, Options{
		Console:        rt,
		TerminalLimits: terminal.DefaultLimits(),
		TerminalAudit:  audit,
	})
	conn, ctx, _ := env.dialOpen(t, terminal.OpenFrame{
		InstanceID: "inst-owned", Cols: 80, Rows: 24,
	})
	defer conn.Close(websocket.StatusNormalClosure, "")

	marker := []byte("exit-marker-" + terminalPayloadMarker)
	if err := conn.Write(ctx, websocket.MessageText, mustEncodeTerminal(t, terminal.InputFrame{Data: marker})); err != nil {
		t.Fatal(err)
	}
	_ = readTerminalFrameUntil(t, conn, ctx, func(f terminal.Frame) bool {
		out, ok := f.(terminal.OutputFrame)
		return ok && bytes.Contains(out.Data, marker)
	})
	if err := conn.Write(ctx, websocket.MessageText, mustEncodeTerminal(t, terminal.SignalFrame{Signal: "TERM"})); err != nil {
		t.Fatal(err)
	}
	_ = readTerminalFrameUntil(t, conn, ctx, func(f terminal.Frame) bool {
		_, ok := f.(terminal.ExitFrame)
		return ok
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if containsTerminalPhase(audit.snapshot(), TerminalAuditPhaseEnd) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	events := audit.snapshot()
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, marker) || bytes.Contains(encoded, []byte(terminalPayloadMarker)) {
		t.Fatalf("audit sink received PTY payload: %s", encoded)
	}
	end := findTerminalPhase(events, TerminalAuditPhaseEnd)
	if end == nil || end.Reason != TerminalAuditReasonExit {
		t.Fatalf("end event=%#v", events)
	}
}

func TestTerminalAuditEndReasonCanceledOnContextCancel(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	audit := &memoryTerminalAudit{}
	h, m, bootstrap := newAuthHandlerWithOptions(t, Options{
		Console:        rt,
		TerminalLimits: terminal.DefaultLimits(),
		TerminalAudit:  audit,
	})
	session, cookie, err := m.Bootstrap(context.Background(), "loopback", bootstrap, "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	svc := h.service.(*fakeService)
	svc.instances = []domain.Instance{{
		ID: "inst-owned", OwnerID: "owner-local", Name: "dev", Kind: domain.KindDevbox,
		RuntimeRef: "incus-owned-ref",
	}}

	var cancelTerminal context.CancelFunc
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/terminal") && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			ctx, cancel := context.WithCancel(r.Context())
			cancelTerminal = cancel
			r = r.WithContext(ctx)
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/instances/inst-owned/terminal?" +
		auth.CSRFQuery + "=" + url.QueryEscape(session.CSRFToken)
	conn, err := dialTerminal(t, wsURL, http.Header{
		"Cookie": []string{auth.SessionCookie + "=" + cookie},
		"Origin": []string{server.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, mustEncodeTerminal(t, terminal.OpenFrame{
		InstanceID: "inst-owned", Cols: 80, Rows: 24,
	})); err != nil {
		t.Fatal(err)
	}
	_ = readOpenAck(t, conn, ctx)

	if cancelTerminal == nil {
		t.Fatal("terminal handler did not register cancel func")
	}
	cancelTerminal()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		end := findTerminalPhase(audit.snapshot(), TerminalAuditPhaseEnd)
		if end != nil {
			if end.Reason != TerminalAuditReasonCanceled {
				t.Fatalf("end reason=%q want %q events=%#v", end.Reason, TerminalAuditReasonCanceled, audit.snapshot())
			}
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no end audit event: %#v", audit.snapshot())
}

type memoryTerminalAudit struct {
	mu     sync.Mutex
	events []TerminalAuditEvent
}

func (a *memoryTerminalAudit) Record(_ context.Context, event TerminalAuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, event)
	return nil
}

func (a *memoryTerminalAudit) snapshot() []TerminalAuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]TerminalAuditEvent(nil), a.events...)
}

func containsTerminalPhase(events []TerminalAuditEvent, phase string) bool {
	return findTerminalPhase(events, phase) != nil
}

func findTerminalPhase(events []TerminalAuditEvent, phase string) *TerminalAuditEvent {
	for i := range events {
		if events[i].Phase == phase {
			return &events[i]
		}
	}
	return nil
}

func newTerminalTestEnvWithOptions(t *testing.T, options Options) *terminalTestEnv {
	t.Helper()
	h, m, bootstrap := newAuthHandlerWithOptions(t, options)
	session, cookie, err := m.Bootstrap(context.Background(), "loopback", bootstrap, "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	svc := h.service.(*fakeService)
	svc.instances = []domain.Instance{{
		ID: "inst-owned", OwnerID: "owner-local", Name: "dev", Kind: domain.KindDevbox,
		RuntimeRef: "incus-owned-ref",
	}}
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	return &terminalTestEnv{handler: h, server: server, cookie: cookie, csrf: session.CSRFToken}
}
