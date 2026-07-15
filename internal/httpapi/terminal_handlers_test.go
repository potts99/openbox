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

func dialOpenTerminal(t *testing.T, rt *fake.Runtime, limits terminal.Limits) (*Handler, *websocket.Conn, context.Context) {
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
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/instances/inst-owned/terminal?" +
		auth.CSRFQuery + "=" + url.QueryEscape(session.CSRFToken)

	conn, err := dialTerminal(t, wsURL, http.Header{
		"Cookie": []string{auth.SessionCookie + "=" + cookie},
		"Origin": []string{server.URL},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)

	openPayload, err := terminal.Encode(terminal.OpenFrame{InstanceID: "inst-owned", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, openPayload); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("open ack: %v", err)
	}
	return h, conn, ctx
}
