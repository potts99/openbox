// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/domain"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
	"github.com/openbox-dev/openbox/internal/terminal"
)

func TestTerminalWebSocketRejectsUnauthorizedUpgrades(t *testing.T) {
	h, m, bootstrap := newAuthHandler(t)
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
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/instances/inst-owned/terminal"

	t.Run("missing session", func(t *testing.T) {
		status, err := dialTerminalHTTP(t, wsURL, nil)
		if err == nil {
			t.Fatal("expected upgrade rejection")
		}
		if status != http.StatusUnauthorized {
			t.Fatalf("status=%d want %d", status, http.StatusUnauthorized)
		}
	})

	t.Run("missing CSRF", func(t *testing.T) {
		status, err := dialTerminalHTTP(t, wsURL, http.Header{
			"Cookie": []string{auth.SessionCookie + "=" + cookie},
			"Origin": []string{server.URL},
		})
		if err == nil {
			t.Fatal("expected upgrade rejection")
		}
		if status != http.StatusForbidden {
			t.Fatalf("status=%d want %d", status, http.StatusForbidden)
		}
	})

	t.Run("invalid CSRF header", func(t *testing.T) {
		status, err := dialTerminalHTTP(t, wsURL, http.Header{
			"Cookie":        []string{auth.SessionCookie + "=" + cookie},
			auth.CSRFHeader: []string{"not-a-valid-csrf-token-value!!"},
			"Origin":        []string{server.URL},
		})
		if err == nil {
			t.Fatal("expected upgrade rejection")
		}
		if status != http.StatusForbidden {
			t.Fatalf("status=%d want %d", status, http.StatusForbidden)
		}
	})

	t.Run("invalid CSRF query", func(t *testing.T) {
		status, err := dialTerminalHTTP(t, wsURL+"?"+auth.CSRFQuery+"=not-a-valid-csrf-token-value!!", http.Header{
			"Cookie": []string{auth.SessionCookie + "=" + cookie},
			"Origin": []string{server.URL},
		})
		if err == nil {
			t.Fatal("expected upgrade rejection")
		}
		if status != http.StatusForbidden {
			t.Fatalf("status=%d want %d", status, http.StatusForbidden)
		}
	})

	t.Run("bad origin", func(t *testing.T) {
		status, err := dialTerminalHTTP(t, wsURL+"?"+auth.CSRFQuery+"="+url.QueryEscape(session.CSRFToken), http.Header{
			"Cookie": []string{auth.SessionCookie + "=" + cookie},
			"Origin": []string{"https://attacker.example"},
		})
		if err == nil {
			t.Fatal("expected upgrade rejection")
		}
		if status != http.StatusForbidden {
			t.Fatalf("status=%d want %d", status, http.StatusForbidden)
		}
	})

	t.Run("missing origin", func(t *testing.T) {
		status, err := dialTerminalHTTP(t, wsURL+"?"+auth.CSRFQuery+"="+url.QueryEscape(session.CSRFToken), http.Header{
			"Cookie": []string{auth.SessionCookie + "=" + cookie},
		})
		if err == nil {
			t.Fatal("expected upgrade rejection")
		}
		if status != http.StatusForbidden {
			t.Fatalf("status=%d want %d", status, http.StatusForbidden)
		}
	})
}

func TestTerminalWebSocketAcceptsCSRFQueryWithoutHeader(t *testing.T) {
	h, m, bootstrap := newAuthHandler(t)
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
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/instances/inst-owned/terminal?" +
		auth.CSRFQuery + "=" + url.QueryEscape(session.CSRFToken)

	// Browser WebSocket: cookie + Origin + csrf query only — no X-CSRF-Token header.
	conn, err := dialTerminal(t, wsURL, http.Header{
		"Cookie": []string{auth.SessionCookie + "=" + cookie},
		"Origin": []string{server.URL},
	})
	if err != nil {
		t.Fatalf("browser-style dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read stub frame: %v", err)
	}
	frame, err := terminal.Decode(data)
	if err != nil {
		t.Fatalf("decode stub frame: %v", err)
	}
	if _, ok := frame.(terminal.ErrorFrame); !ok {
		t.Fatalf("frame type %T, want ErrorFrame", frame)
	}
}

func TestCookieMutationIgnoresCSRFQueryParameter(t *testing.T) {
	h, m, bootstrap := newAuthHandler(t)
	session, cookie, err := m.Bootstrap(context.Background(), "loopback", bootstrap, "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/tokens?"+auth.CSRFQuery+"="+url.QueryEscape(session.CSRFToken), strings.NewReader(`{"name":"q"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("query CSRF must not authorize non-WebSocket mutations: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCookieMutationIgnoresCSRFQueryWithWebSocketUpgradeHeader(t *testing.T) {
	h, m, bootstrap := newAuthHandler(t)
	session, cookie, err := m.Bootstrap(context.Background(), "loopback", bootstrap, "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/tokens?"+auth.CSRFQuery+"="+url.QueryEscape(session.CSRFToken), strings.NewReader(`{"name":"q"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("POST with Upgrade: websocket must not accept query CSRF without header: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestTerminalWebSocketCrossInstanceAuthorization(t *testing.T) {
	h, m, bootstrap := newAuthHandler(t)
	session, cookie, err := m.Bootstrap(context.Background(), "loopback", bootstrap, "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	svc := h.service.(*fakeService)
	svc.instances = []domain.Instance{{
		ID: "inst-owned", OwnerID: "owner-local", Name: "dev", Kind: domain.KindDevbox,
		RuntimeRef: "incus-secret-ref",
	}}

	server := httptest.NewServer(h)
	t.Cleanup(server.Close)

	authHeader := func() http.Header {
		return http.Header{
			"Cookie":        []string{auth.SessionCookie + "=" + cookie},
			auth.CSRFHeader: []string{session.CSRFToken},
			"Origin":        []string{server.URL},
		}
	}

	t.Run("unknown openbox instance", func(t *testing.T) {
		url := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/instances/inst-missing/terminal"
		status, err := dialTerminalHTTP(t, url, authHeader())
		if err == nil {
			t.Fatal("expected upgrade rejection")
		}
		if status != http.StatusNotFound {
			t.Fatalf("status=%d want %d", status, http.StatusNotFound)
		}
	})

	t.Run("incus runtime ref is not addressable as instance id", func(t *testing.T) {
		url := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/instances/incus-secret-ref/terminal"
		status, err := dialTerminalHTTP(t, url, authHeader())
		if err == nil {
			t.Fatal("expected upgrade rejection")
		}
		if status != http.StatusNotFound {
			t.Fatalf("status=%d want %d", status, http.StatusNotFound)
		}
		if svc.lastInstanceID != "incus-secret-ref" {
			t.Fatalf("lookup id=%q, want attempted openbox id equal to path segment", svc.lastInstanceID)
		}
	})

	t.Run("owned instance upgrades using openbox id only", func(t *testing.T) {
		url := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/instances/inst-owned/terminal"
		conn, err := dialTerminal(t, url, authHeader())
		if err != nil {
			t.Fatalf("authorized dial: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		if svc.lastOwner != "owner-local" || svc.lastInstanceID != "inst-owned" {
			t.Fatalf("GetInstance owner=%q id=%q", svc.lastOwner, svc.lastInstanceID)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read stub frame: %v", err)
		}
		frame, err := terminal.Decode(data)
		if err != nil {
			t.Fatalf("decode stub frame: %v", err)
		}
		errFrame, ok := frame.(terminal.ErrorFrame)
		if !ok {
			t.Fatalf("frame type %T, want ErrorFrame", frame)
		}
		if errFrame.Code != "not_implemented" {
			t.Fatalf("error code=%q", errFrame.Code)
		}
		if strings.Contains(strings.ToLower(errFrame.Message), "incus") {
			t.Fatalf("stub leaked runtime identity: %q", errFrame.Message)
		}
	})
}

func TestTerminalWebSocketRejectsNonGet(t *testing.T) {
	h, m, bootstrap := newAuthHandler(t)
	session, cookie, err := m.Bootstrap(context.Background(), "loopback", bootstrap, "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	svc := h.service.(*fakeService)
	svc.instances = []domain.Instance{{
		ID: "inst-owned", OwnerID: "owner-local", Name: "dev", RuntimeRef: "incus-ref",
	}}

	req := httptest.NewRequest(http.MethodPost, "/v1/instances/inst-owned/terminal", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
	req.Header.Set(auth.CSRFHeader, session.CSRFToken)
	req.Header.Set("Origin", "http://127.0.0.1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestTerminalRejectsUnmanagedAndHostRuntimeRefs(t *testing.T) {
	cases := []struct {
		name       string
		runtimeRef string
		wantStatus int
		wantCode   string
	}{
		{name: "unmanaged empty ref", runtimeRef: "", wantStatus: http.StatusConflict, wantCode: string(domain.CodeRuntimeMissing)},
		{name: "host ref", runtimeRef: "host", wantStatus: http.StatusBadRequest, wantCode: string(domain.CodeInvalidArgument)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, m, bootstrap := newAuthHandler(t)
			session, cookie, err := m.Bootstrap(context.Background(), "loopback", bootstrap, "a sufficiently long password")
			if err != nil {
				t.Fatal(err)
			}
			svc := h.service.(*fakeService)
			svc.instances = []domain.Instance{{
				ID: "inst-owned", OwnerID: "owner-local", Name: "dev", Kind: domain.KindDevbox,
				RuntimeRef: tc.runtimeRef,
			}}

			req := httptest.NewRequest(http.MethodGet, "/v1/instances/inst-owned/terminal", nil)
			req.Host = "127.0.0.1"
			req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
			req.Header.Set(auth.CSRFHeader, session.CSRFToken)
			req.Header.Set("Origin", "http://127.0.0.1")
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Sec-WebSocket-Version", "13")
			req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s want %d", w.Code, w.Body.String(), tc.wantStatus)
			}
			if !strings.Contains(w.Body.String(), `"code":"`+tc.wantCode+`"`) {
				t.Fatalf("body=%s want code %q", w.Body.String(), tc.wantCode)
			}
		})
	}
}

func TestTerminalOpensPTYThroughRuntimeAdapterUsingStoredRef(t *testing.T) {
	rt := fake.New(runtimeapi.Capabilities{})
	rt.AddImage(runtimeapi.Image{Fingerprint: "sha256:base"})
	if _, err := rt.CreateInstance(context.Background(), runtimeapi.CreateRequest{Ref: "incus-owned-ref", Image: "sha256:base"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.StartInstance(context.Background(), "incus-owned-ref"); err != nil {
		t.Fatal(err)
	}

	h, m, bootstrap := newAuthHandlerWithOptions(t, Options{Console: rt})
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
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/instances/inst-owned/terminal?" +
		auth.CSRFQuery + "=" + url.QueryEscape(session.CSRFToken)

	conn, err := dialTerminal(t, wsURL, http.Header{
		"Cookie": []string{auth.SessionCookie + "=" + cookie},
		"Origin": []string{server.URL},
	})
	if err != nil {
		t.Fatalf("authorized dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	openPayload, err := terminal.Encode(terminal.OpenFrame{
		InstanceID: "client-supplied-incus-id", Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, openPayload); err != nil {
		t.Fatal(err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read open ack: %v", err)
	}
	frame, err := terminal.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	ack, ok := frame.(terminal.OpenFrame)
	if !ok {
		t.Fatalf("frame type %T, want OpenFrame", frame)
	}
	if ack.InstanceID != "inst-owned" {
		t.Fatalf("open ack instance_id=%q, want openbox id", ack.InstanceID)
	}
	if rt.LastConsoleRef() != "incus-owned-ref" {
		t.Fatalf("OpenConsole ref=%q, want stored RuntimeRef (not client-supplied id)", rt.LastConsoleRef())
	}

	inputPayload, err := terminal.Encode(terminal.InputFrame{Data: []byte("ping")})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, inputPayload); err != nil {
		t.Fatal(err)
	}

	_, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	outFrame, err := terminal.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	output, ok := outFrame.(terminal.OutputFrame)
	if !ok {
		t.Fatalf("frame type %T, want OutputFrame", outFrame)
	}
	if string(output.Data) != "ping" {
		t.Fatalf("output=%q", output.Data)
	}
}

func dialTerminal(t *testing.T, wsURL string, header http.Header) (*websocket.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: header})
	return conn, err
}

func dialTerminalHTTP(t *testing.T, wsURL string, header http.Header) (int, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: header})
	if resp != nil {
		defer resp.Body.Close()
		return resp.StatusCode, err
	}
	if err == nil {
		t.Fatal("dial succeeded without response")
	}
	return 0, err
}

func TestTerminalRejectsConcurrentSessionsAtCapacity(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	limits := terminal.DefaultLimits()
	limits.MaxSessionsPerOwner = 1
	limits.MaxSessionsPerInstance = 1

	h, m, bootstrap := newAuthHandlerWithOptions(t, Options{Console: rt, TerminalLimits: limits})
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
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/instances/inst-owned/terminal?" +
		auth.CSRFQuery + "=" + url.QueryEscape(session.CSRFToken)
	header := http.Header{
		"Cookie": []string{auth.SessionCookie + "=" + cookie},
		"Origin": []string{server.URL},
	}

	first, err := dialTerminal(t, wsURL, header)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	defer first.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	openPayload, err := terminal.Encode(terminal.OpenFrame{InstanceID: "inst-owned", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Write(ctx, websocket.MessageText, openPayload); err != nil {
		t.Fatal(err)
	}
	if _, _, err := first.Read(ctx); err != nil {
		t.Fatalf("open ack: %v", err)
	}

	status, err := dialTerminalHTTP(t, wsURL, header)
	if err == nil {
		t.Fatal("expected second upgrade rejected")
	}
	if status != http.StatusTooManyRequests {
		t.Fatalf("status=%d want %d", status, http.StatusTooManyRequests)
	}
}

func TestTerminalRejectsOversizedInboundFrame(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	h, conn, ctx := dialOpenTerminal(t, rt, terminal.DefaultLimits())
	_ = h
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Bypass Encode (which refuses oversized frames) and write raw bytes over the limit.
	oversized := []byte(`{"type":"input","data":"` + strings.Repeat("A", terminal.MaxFrameBytes) + `"}`)
	if len(oversized) <= terminal.MaxFrameBytes {
		t.Fatalf("test payload not oversized: %d", len(oversized))
	}
	if err := conn.Write(ctx, websocket.MessageText, oversized); err != nil {
		// Client may fail on write if peer already closed; still wait for close.
		t.Logf("write oversized: %v", err)
	}

	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("expected read failure after oversized frame")
	}
}

func TestTerminalEnforcesInboundRateLimit(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	limits := terminal.DefaultLimits()
	limits.MaxInboundFramesPerWindow = 3
	limits.MaxInboundBytesPerWindow = 1 << 20
	limits.RateWindow = time.Second

	_, conn, ctx := dialOpenTerminal(t, rt, limits)
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Open already consumed 1 frame. Two more inputs are allowed; the next should trip the limiter.
	for i := 0; i < 2; i++ {
		payload, err := terminal.Encode(terminal.InputFrame{Data: []byte("x")})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.Read(ctx); err != nil {
			t.Fatalf("echo %d: %v", i, err)
		}
	}

	payload, err := terminal.Encode(terminal.InputFrame{Data: []byte("y")})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read rate-limit error: %v", err)
	}
	frame, err := terminal.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	errFrame, ok := frame.(terminal.ErrorFrame)
	if !ok {
		t.Fatalf("frame type %T, want ErrorFrame", frame)
	}
	if errFrame.Code != "rate_limited" {
		t.Fatalf("code=%q want rate_limited", errFrame.Code)
	}
}

func TestTerminalClosesIdleSessions(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	limits := terminal.DefaultLimits()
	limits.IdleTimeout = 80 * time.Millisecond

	_, conn, ctx := dialOpenTerminal(t, rt, limits)
	defer conn.Close(websocket.StatusNormalClosure, "")

	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
			t.Fatalf("idle close status=%v err=%v want StatusPolicyViolation or idle_timeout frame", websocket.CloseStatus(err), err)
		}
		return
	}
	frame, err := terminal.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	errFrame, ok := frame.(terminal.ErrorFrame)
	if !ok {
		t.Fatalf("frame type %T, want ErrorFrame", frame)
	}
	if errFrame.Code != "idle_timeout" {
		t.Fatalf("code=%q want idle_timeout", errFrame.Code)
	}
}

func TestTerminalEnforcesTotalBufferLimit(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	limits := terminal.DefaultLimits()
	limits.MaxTotalBufferBytes = 16

	_, conn, ctx := dialOpenTerminal(t, rt, limits)
	defer conn.Close(websocket.StatusNormalClosure, "")

	payload, err := terminal.Encode(terminal.InputFrame{Data: []byte("0123456789abcdef0123456789")})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read buffer error: %v", err)
	}
	frame, err := terminal.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	errFrame, ok := frame.(terminal.ErrorFrame)
	if !ok {
		t.Fatalf("frame type %T, want ErrorFrame", frame)
	}
	if errFrame.Code != "buffer_limit" {
		t.Fatalf("code=%q want buffer_limit", errFrame.Code)
	}
}

func TestTerminalResizePropagatesToConsole(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	_, conn, ctx := dialOpenTerminal(t, rt, terminal.DefaultLimits())
	defer conn.Close(websocket.StatusNormalClosure, "")

	payload, err := terminal.Encode(terminal.ResizeFrame{Cols: 132, Rows: 43})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		cols, rows := rt.LastConsoleSize("incus-owned-ref")
		if cols == 132 && rows == 43 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("console size=%dx%d, want 132x43", cols, rows)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTerminalPropagatesExitStatusOnTerminate(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	rt.SetConsoleExitCode(42)
	_, conn, ctx := dialOpenTerminal(t, rt, terminal.DefaultLimits())
	defer conn.Close(websocket.StatusNormalClosure, "")

	payload, err := terminal.Encode(terminal.SignalFrame{Signal: "TERM"})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}

	frame := readTerminalFrameUntil(t, conn, ctx, func(f terminal.Frame) bool {
		_, ok := f.(terminal.ExitFrame)
		return ok
	})
	exit, ok := frame.(terminal.ExitFrame)
	if !ok {
		t.Fatalf("frame type %T, want ExitFrame", frame)
	}
	if exit.Code != 42 {
		t.Fatalf("exit code=%d want 42", exit.Code)
	}
	if !rt.ConsoleClosed("incus-owned-ref") {
		t.Fatal("expected console session closed after TERM")
	}
}

func TestTerminalTerminateCancelsConsole(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	_, conn, ctx := dialOpenTerminal(t, rt, terminal.DefaultLimits())
	defer conn.Close(websocket.StatusNormalClosure, "")

	payload, err := terminal.Encode(terminal.SignalFrame{Signal: "KILL"})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}

	_ = readTerminalFrameUntil(t, conn, ctx, func(f terminal.Frame) bool {
		_, ok := f.(terminal.ExitFrame)
		return ok
	})
	if !rt.ConsoleClosed("incus-owned-ref") {
		t.Fatal("KILL must cancel/close the console session")
	}
}

func TestTerminalDetachDoesNotTerminateNamedSession(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	_, conn, ctx := dialOpenTerminalWithOpen(t, rt, terminal.DefaultLimits(), terminal.OpenFrame{
		InstanceID:  "inst-owned",
		Cols:        80,
		Rows:        24,
		SessionName: "main",
	})
	defer conn.Close(websocket.StatusNormalClosure, "")

	payload, err := terminal.Encode(terminal.DetachFrame{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}

	// Detach closes the WebSocket without an exit frame (session stays alive for reconnect).
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(readCtx)
		if err != nil {
			break
		}
		frame, decErr := terminal.Decode(data)
		if decErr != nil {
			continue
		}
		if _, ok := frame.(terminal.ExitFrame); ok {
			t.Fatal("named detach must not send exit (console still running)")
		}
	}

	// Give the bridge a moment to finish; named detach must leave the console open.
	time.Sleep(50 * time.Millisecond)
	if rt.ConsoleClosed("incus-owned-ref") {
		t.Fatal("named detach must not Close the console session")
	}
	t.Cleanup(func() {
		if s := rt.ActiveConsole("incus-owned-ref"); s != nil {
			_ = s.Close()
		}
	})
}

func TestTerminalNamedOpenUsesTmuxHelper(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	_, conn, ctx, ack := dialOpenTerminalWithAck(t, rt, terminal.DefaultLimits(), terminal.OpenFrame{
		InstanceID:  "inst-owned",
		Cols:        80,
		Rows:        24,
		SessionName: "pi",
	})
	defer conn.Close(websocket.StatusNormalClosure, "")
	_ = ctx

	want, err := terminal.CommandForSession("pi")
	if err != nil {
		t.Fatal(err)
	}
	if got := rt.LastConsoleCommand(); !slicesEqual(got, want) {
		t.Fatalf("console command=%v want %v", got, want)
	}
	if ack.SessionName != "pi" {
		t.Fatalf("ack session_name=%q want pi", ack.SessionName)
	}
	if ack.SessionID == "" {
		t.Fatal("named open ack must include session_id for reconnect")
	}
	t.Cleanup(func() {
		if s := rt.ActiveConsole("incus-owned-ref"); s != nil {
			_ = s.Close()
		}
	})
}

func TestTerminalUnnamedOpenDoesNotInvokeTmux(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	_, conn, _, ack := dialOpenTerminalWithAck(t, rt, terminal.DefaultLimits(), terminal.OpenFrame{
		InstanceID: "inst-owned",
		Cols:       80,
		Rows:       24,
	})
	defer conn.Close(websocket.StatusNormalClosure, "")

	got := rt.LastConsoleCommand()
	if slicesContains(got, "tmux") {
		t.Fatalf("unnamed shell command %v must not invoke tmux", got)
	}
	want, err := terminal.CommandForSession("")
	if err != nil {
		t.Fatal(err)
	}
	if !slicesEqual(got, want) {
		t.Fatalf("console command=%v want %v", got, want)
	}
	if ack.SessionID != "" {
		t.Fatalf("ephemeral open must not assign session_id, got %q", ack.SessionID)
	}
}

func TestTerminalReconnectBySessionIDReattaches(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	env := newTerminalTestEnv(t, rt, terminal.DefaultLimits())
	conn, ctx, ack := env.dialOpen(t, terminal.OpenFrame{
		InstanceID:  "inst-owned",
		Cols:        80,
		Rows:        24,
		SessionName: "main",
	})

	if err := conn.Write(ctx, websocket.MessageText, mustEncodeTerminal(t, terminal.InputFrame{Data: []byte("keep")})); err != nil {
		t.Fatal(err)
	}
	_ = readTerminalFrameUntil(t, conn, ctx, func(f terminal.Frame) bool {
		out, ok := f.(terminal.OutputFrame)
		return ok && string(out.Data) == "keep"
	})

	if err := conn.Write(ctx, websocket.MessageText, mustEncodeTerminal(t, terminal.DetachFrame{})); err != nil {
		t.Fatal(err)
	}
	waitTerminalClosed(t, conn)
	time.Sleep(50 * time.Millisecond)
	if rt.ConsoleClosed("incus-owned-ref") {
		t.Fatal("named detach must leave console open")
	}
	opensBefore := countFakeCalls(rt, "console.open")

	conn2, ctx2 := env.dial(t)
	defer conn2.Close(websocket.StatusNormalClosure, "")
	if err := conn2.Write(ctx2, websocket.MessageText, mustEncodeTerminal(t, terminal.ReconnectFrame{SessionID: ack.SessionID})); err != nil {
		t.Fatal(err)
	}
	reack := readOpenAck(t, conn2, ctx2)
	if reack.SessionID != ack.SessionID {
		t.Fatalf("reconnect ack session_id=%q want %q", reack.SessionID, ack.SessionID)
	}
	if countFakeCalls(rt, "console.open") != opensBefore {
		t.Fatal("reconnect by session_id must reattach without OpenConsole")
	}

	if err := conn2.Write(ctx2, websocket.MessageText, mustEncodeTerminal(t, terminal.InputFrame{Data: []byte("again")})); err != nil {
		t.Fatal(err)
	}
	_ = readTerminalFrameUntil(t, conn2, ctx2, func(f terminal.Frame) bool {
		out, ok := f.(terminal.OutputFrame)
		return ok && string(out.Data) == "again"
	})

	t.Cleanup(func() {
		if s := rt.ActiveConsole("incus-owned-ref"); s != nil {
			_ = s.Close()
		}
	})
}

func TestTerminalOpenSameSessionNameReattaches(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	env := newTerminalTestEnv(t, rt, terminal.DefaultLimits())
	conn, ctx, ack := env.dialOpen(t, terminal.OpenFrame{
		InstanceID:  "inst-owned",
		Cols:        80,
		Rows:        24,
		SessionName: "main",
	})
	if err := conn.Write(ctx, websocket.MessageText, mustEncodeTerminal(t, terminal.DetachFrame{})); err != nil {
		t.Fatal(err)
	}
	waitTerminalClosed(t, conn)
	time.Sleep(50 * time.Millisecond)
	opensBefore := countFakeCalls(rt, "console.open")

	conn2, ctx2 := env.dial(t)
	defer conn2.Close(websocket.StatusNormalClosure, "")
	if err := conn2.Write(ctx2, websocket.MessageText, mustEncodeTerminal(t, terminal.OpenFrame{
		InstanceID:  "inst-owned",
		Cols:        100,
		Rows:        40,
		SessionName: "main",
	})); err != nil {
		t.Fatal(err)
	}
	reack := readOpenAck(t, conn2, ctx2)
	if reack.SessionID != ack.SessionID {
		t.Fatalf("session_name reattach session_id=%q want %q", reack.SessionID, ack.SessionID)
	}
	if countFakeCalls(rt, "console.open") != opensBefore {
		t.Fatal("open with same session_name must reattach without OpenConsole")
	}
	cols, rows := rt.LastConsoleSize("incus-owned-ref")
	if cols != 100 || rows != 40 {
		t.Fatalf("reattach resize = %dx%d want 100x40", cols, rows)
	}

	t.Cleanup(func() {
		if s := rt.ActiveConsole("incus-owned-ref"); s != nil {
			_ = s.Close()
		}
	})
}

func TestTerminalNamedSessionReopensAfterGuestExit(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	env := newTerminalTestEnv(t, rt, terminal.DefaultLimits())
	conn, ctx, ack := env.dialOpen(t, terminal.OpenFrame{
		InstanceID:  "inst-owned",
		Cols:        80,
		Rows:        24,
		SessionName: "main",
	})
	if err := conn.Write(ctx, websocket.MessageText, mustEncodeTerminal(t, terminal.DetachFrame{})); err != nil {
		t.Fatal(err)
	}
	waitTerminalClosed(t, conn)
	time.Sleep(50 * time.Millisecond)
	opensAfterDetach := countFakeCalls(rt, "console.open")

	if s := rt.ActiveConsole("incus-owned-ref"); s != nil {
		_ = s.Close()
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if countFakeCalls(rt, "console.open") == opensAfterDetach && rt.ConsoleClosed("incus-owned-ref") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !rt.ConsoleClosed("incus-owned-ref") {
		t.Fatal("guest exit must close the console")
	}

	conn2, ctx2, reack := env.dialOpen(t, terminal.OpenFrame{
		InstanceID:  "inst-owned",
		Cols:        80,
		Rows:        24,
		SessionName: "main",
	})
	defer conn2.Close(websocket.StatusNormalClosure, "")
	if countFakeCalls(rt, "console.open") != opensAfterDetach+1 {
		t.Fatal("session_name reopen after guest exit must OpenConsole fresh tmux -A")
	}
	if reack.SessionID == ack.SessionID {
		t.Fatalf("reopen after guest exit must issue new session_id, got %q", reack.SessionID)
	}
	if err := conn2.Write(ctx2, websocket.MessageText, mustEncodeTerminal(t, terminal.InputFrame{Data: []byte("fresh")})); err != nil {
		t.Fatal(err)
	}
	_ = readTerminalFrameUntil(t, conn2, ctx2, func(f terminal.Frame) bool {
		out, ok := f.(terminal.OutputFrame)
		return ok && string(out.Data) == "fresh"
	})

	t.Cleanup(func() {
		if s := rt.ActiveConsole("incus-owned-ref"); s != nil {
			_ = s.Close()
		}
	})
}

func TestTerminalRejectsInvalidSessionName(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	h, m, bootstrap := newAuthHandlerWithOptions(t, Options{Console: rt, TerminalLimits: terminal.DefaultLimits()})
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
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/instances/inst-owned/terminal?" +
		auth.CSRFQuery + "=" + url.QueryEscape(session.CSRFToken)
	conn, err := dialTerminal(t, wsURL, http.Header{
		"Cookie": []string{auth.SessionCookie + "=" + cookie},
		"Origin": []string{server.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, mustEncodeTerminal(t, terminal.OpenFrame{
		InstanceID:  "inst-owned",
		Cols:        80,
		Rows:        24,
		SessionName: "bad:name",
	})); err != nil {
		t.Fatal(err)
	}
	frame := readTerminalFrameUntil(t, conn, ctx, func(f terminal.Frame) bool {
		_, ok := f.(terminal.ErrorFrame)
		return ok
	})
	errFrame := frame.(terminal.ErrorFrame)
	if errFrame.Code != "invalid_argument" {
		t.Fatalf("error code=%q want invalid_argument", errFrame.Code)
	}
	if rt.LastConsoleCommand() != nil {
		t.Fatalf("invalid session_name must not open console, got %v", rt.LastConsoleCommand())
	}
}

func TestTerminalDetachTerminatesEphemeralSession(t *testing.T) {
	// Without session_name there is no reconnect target yet (task 7), so detach
	// is treated as terminate: close console and propagate exit.
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	_, conn, ctx := dialOpenTerminal(t, rt, terminal.DefaultLimits())
	defer conn.Close(websocket.StatusNormalClosure, "")

	payload, err := terminal.Encode(terminal.DetachFrame{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}

	_ = readTerminalFrameUntil(t, conn, ctx, func(f terminal.Frame) bool {
		_, ok := f.(terminal.ExitFrame)
		return ok
	})
	if !rt.ConsoleClosed("incus-owned-ref") {
		t.Fatal("ephemeral detach must terminate the console")
	}
}

func TestTerminalClientDisconnectDoesNotEmitInvalidFrame(t *testing.T) {
	rt := newRunningFakeRuntime(t, "incus-owned-ref")
	_, conn, ctx := dialOpenTerminal(t, rt, terminal.DefaultLimits())

	errFrames := make(chan terminal.ErrorFrame, 1)
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			frame, err := terminal.Decode(data)
			if err != nil {
				continue
			}
			if errFrame, ok := frame.(terminal.ErrorFrame); ok {
				errFrames <- errFrame
				return
			}
		}
	}()

	time.Sleep(30 * time.Millisecond)
	_ = conn.Close(websocket.StatusNormalClosure, "")

	select {
	case errFrame := <-errFrames:
		if errFrame.Code == "invalid_frame" {
			t.Fatalf("client disconnect mislabeled as invalid_frame: %#v", errFrame)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

func newRunningFakeRuntime(t *testing.T, ref string) *fake.Runtime {
	t.Helper()
	rt := fake.New(runtimeapi.Capabilities{})
	rt.AddImage(runtimeapi.Image{Fingerprint: "sha256:base"})
	if _, err := rt.CreateInstance(context.Background(), runtimeapi.CreateRequest{Ref: ref, Image: "sha256:base"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.StartInstance(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	return rt
}

type terminalTestEnv struct {
	handler *Handler
	server  *httptest.Server
	cookie  string
	csrf    string
}

func newTerminalTestEnv(t *testing.T, rt *fake.Runtime, limits terminal.Limits) *terminalTestEnv {
	t.Helper()
	h, m, bootstrap := newAuthHandlerWithOptions(t, Options{Console: rt, TerminalLimits: limits})
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

func (e *terminalTestEnv) dial(t *testing.T) (*websocket.Conn, context.Context) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(e.server.URL, "http") + "/v1/instances/inst-owned/terminal?" +
		auth.CSRFQuery + "=" + url.QueryEscape(e.csrf)
	conn, err := dialTerminal(t, wsURL, http.Header{
		"Cookie": []string{auth.SessionCookie + "=" + e.cookie},
		"Origin": []string{e.server.URL},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return conn, ctx
}

func (e *terminalTestEnv) dialOpen(t *testing.T, open terminal.OpenFrame) (*websocket.Conn, context.Context, terminal.OpenFrame) {
	t.Helper()
	conn, ctx := e.dial(t)
	if err := conn.Write(ctx, websocket.MessageText, mustEncodeTerminal(t, open)); err != nil {
		t.Fatal(err)
	}
	return conn, ctx, readOpenAck(t, conn, ctx)
}

func dialOpenTerminal(t *testing.T, rt *fake.Runtime, limits terminal.Limits) (*Handler, *websocket.Conn, context.Context) {
	t.Helper()
	return dialOpenTerminalWithOpen(t, rt, limits, terminal.OpenFrame{
		InstanceID: "inst-owned", Cols: 80, Rows: 24,
	})
}

func dialOpenTerminalWithOpen(
	t *testing.T,
	rt *fake.Runtime,
	limits terminal.Limits,
	open terminal.OpenFrame,
) (*Handler, *websocket.Conn, context.Context) {
	t.Helper()
	h, conn, ctx, _ := dialOpenTerminalWithAck(t, rt, limits, open)
	return h, conn, ctx
}

func dialOpenTerminalWithAck(
	t *testing.T,
	rt *fake.Runtime,
	limits terminal.Limits,
	open terminal.OpenFrame,
) (*Handler, *websocket.Conn, context.Context, terminal.OpenFrame) {
	t.Helper()
	env := newTerminalTestEnv(t, rt, limits)
	conn, ctx, ack := env.dialOpen(t, open)
	return env.handler, conn, ctx, ack
}

func readOpenAck(t *testing.T, conn *websocket.Conn, ctx context.Context) terminal.OpenFrame {
	t.Helper()
	frame := readTerminalFrameUntil(t, conn, ctx, func(f terminal.Frame) bool {
		_, ok := f.(terminal.OpenFrame)
		return ok
	})
	ack, ok := frame.(terminal.OpenFrame)
	if !ok {
		t.Fatalf("frame type %T, want OpenFrame", frame)
	}
	return ack
}

func mustEncodeTerminal(t *testing.T, frame terminal.Frame) []byte {
	t.Helper()
	payload, err := terminal.Encode(frame)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func waitTerminalClosed(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	readCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		if _, _, err := conn.Read(readCtx); err != nil {
			return
		}
	}
}

func countFakeCalls(rt *fake.Runtime, op string) int {
	n := 0
	for _, call := range rt.Calls() {
		if call == op {
			n++
		}
	}
	return n
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func slicesContains(a []string, want string) bool {
	for _, v := range a {
		if v == want {
			return true
		}
	}
	return false
}

func readTerminalFrameUntil(
	t *testing.T,
	conn *websocket.Conn,
	ctx context.Context,
	match func(terminal.Frame) bool,
) terminal.Frame {
	t.Helper()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		frame, err := terminal.Decode(data)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if match(frame) {
			return frame
		}
	}
}
