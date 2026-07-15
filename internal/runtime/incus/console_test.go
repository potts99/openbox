// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestOpenConsoleRejectsHostTargets(t *testing.T) {
	t.Parallel()
	adapter, err := New(Options{SocketPath: "/tmp/openbox-incus-console-test.socket"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"", "host"} {
		_, err := adapter.OpenConsole(context.Background(), runtimeapi.ConsoleRequest{Ref: ref, Cols: 80, Rows: 24})
		if !errors.Is(err, runtimeapi.ErrHostTarget) {
			t.Fatalf("ref %q: %v", ref, err)
		}
	}
}

func TestOpenConsoleInteractiveWebsocketSession(t *testing.T) {
	t.Parallel()
	api := &consoleAPI{}
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{SocketPath: socket, Project: "openbox", HostProbe: staticProbe{}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := adapter.OpenConsole(context.Background(), runtimeapi.ConsoleRequest{
		Ref:     "obx-1",
		Command: []string{"tmux", "new-session", "-A", "-s", "pi", "--", "pi"},
		Cols:    120,
		Rows:    40,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	if got := api.posted.Command; strings.Join(got, " ") != "tmux new-session -A -s pi -- pi" {
		t.Fatalf("command=%v", got)
	}
	if !api.posted.WaitForWebsocket || !api.posted.Interactive || api.posted.Width != 120 || api.posted.Height != 40 {
		t.Fatalf("exec flags=%#v", api.posted)
	}

	if _, err := session.Stdin().Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	readDone := make(chan error, 1)
	go func() {
		_, readErr := io.ReadFull(session.Stdout(), buf)
		readDone <- readErr
	}()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for echoed stdout")
	}
	if string(buf) != "hello" {
		t.Fatalf("stdout=%q", buf)
	}

	if err := session.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if api.resizedWidth() == "100" && api.resizedHeight() == "30" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if api.resizedWidth() != "100" || api.resizedHeight() != "30" {
		t.Fatalf("resize width=%q height=%q", api.resizedWidth(), api.resizedHeight())
	}

	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	code, waitErr := session.Wait()
	if waitErr != nil {
		t.Fatal(waitErr)
	}
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
}

type consoleAPI struct {
	mu     sync.Mutex
	posted consoleExecPost
	width  string
	height string
}

type consoleExecPost struct {
	Command          []string `json:"command"`
	WaitForWebsocket bool     `json:"wait-for-websocket"`
	Interactive      bool     `json:"interactive"`
	Width            int      `json:"width"`
	Height           int      `json:"height"`
}

func (a *consoleAPI) resizedWidth() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.width
}

func (a *consoleAPI) resizedHeight() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.height
}

func (a *consoleAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/exec"):
		a.mu.Lock()
		_ = json.NewDecoder(r.Body).Decode(&a.posted)
		a.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(apiResponse{
			Type:       "async",
			StatusCode: http.StatusAccepted,
			Operation:  "/1.0/operations/console-1",
			Metadata: mustJSON(map[string]any{
				"id":     "console-1",
				"class":  "websocket",
				"status": "Running",
				"metadata": map[string]any{
					"fds": map[string]string{
						"0":       "data-secret",
						"control": "control-secret",
					},
				},
			}),
		})
	case r.Method == http.MethodGet && r.URL.Path == "/1.0/operations/console-1/websocket":
		secret := r.URL.Query().Get("secret")
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		switch secret {
		case "data-secret":
			for {
				typ, data, err := conn.Read(ctx)
				if err != nil {
					return
				}
				if typ == websocket.MessageBinary || typ == websocket.MessageText {
					if err := conn.Write(ctx, websocket.MessageBinary, data); err != nil {
						return
					}
				}
			}
		case "control-secret":
			for {
				_, data, err := conn.Read(ctx)
				if err != nil {
					return
				}
				var msg struct {
					Command string            `json:"command"`
					Args    map[string]string `json:"args"`
				}
				if json.Unmarshal(data, &msg) == nil && msg.Command == "window-resize" {
					a.mu.Lock()
					a.width = msg.Args["width"]
					a.height = msg.Args["height"]
					a.mu.Unlock()
				}
			}
		default:
			_ = conn.Close(websocket.StatusPolicyViolation, "bad secret")
		}
	case r.Method == http.MethodGet && r.URL.Path == "/1.0/operations/console-1/wait":
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
		writeSync(w, map[string]any{
			"status_code": 200,
			"err":         "",
			"metadata":    map[string]any{"return": 0},
		})
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
