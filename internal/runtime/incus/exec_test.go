// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestExecRecordsOutputAndExitCode(t *testing.T) {
	t.Parallel()
	api := &execAPI{
		stdout: []byte("hello\n"),
		stderr: []byte("warn\n"),
		code:   7,
	}
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{SocketPath: socket, Project: "openbox", HostProbe: staticProbe{}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Exec(context.Background(), runtimeapi.ExecRequest{
		Ref:        "obx-1",
		Command:    []string{"echo", "hello"},
		WorkingDir: "/tmp",
		Env:        map[string]string{"FOO": "bar"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("exit=%d, want 7", result.ExitCode)
	}
	if string(result.Stdout) != "hello\n" || string(result.Stderr) != "warn\n" {
		t.Fatalf("stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
	if !reflect.DeepEqual(api.posted.Command, []string{"echo", "hello"}) {
		t.Fatalf("command=%v", api.posted.Command)
	}
	if api.posted.Cwd != "/tmp" || api.posted.Env["FOO"] != "bar" {
		t.Fatalf("cwd/env=%#v", api.posted)
	}
	if api.posted.WaitForWebsocket || !api.posted.RecordOutput || api.posted.Interactive {
		t.Fatalf("flags=%#v", api.posted)
	}
}

func TestExecWrapsStdinWithoutWebsockets(t *testing.T) {
	t.Parallel()
	api := &execAPI{code: 0}
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{SocketPath: socket, Project: "openbox", HostProbe: staticProbe{}})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"theme":"dark"}`)
	_, err = adapter.Exec(context.Background(), runtimeapi.ExecRequest{
		Ref:     "obx-1",
		Command: []string{"sh", "-c", "cat > \"$1\"", "write", "/tmp/x"},
		Stdin:   bytes.NewReader(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.posted.Command) < 5 {
		t.Fatalf("wrapped command too short: %#v", api.posted.Command)
	}
	if api.posted.Command[0] != "sh" || api.posted.Command[3] != "openbox-exec-stdin" {
		t.Fatalf("stdin wrap missing: %#v", api.posted.Command)
	}
	got, err := base64.StdEncoding.DecodeString(api.posted.Command[4])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("stdin b64=%q, want %q", got, payload)
	}
	if !reflect.DeepEqual(api.posted.Command[5:], []string{"sh", "-c", "cat > \"$1\"", "write", "/tmp/x"}) {
		t.Fatalf("original argv lost: %#v", api.posted.Command)
	}
}

func TestExecRejectsHostTarget(t *testing.T) {
	t.Parallel()
	adapter, err := New(Options{SocketPath: "/tmp/incus.socket", HostProbe: staticProbe{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Exec(context.Background(), runtimeapi.ExecRequest{Ref: "host", Command: []string{"true"}})
	if err != runtimeapi.ErrHostTarget {
		t.Fatalf("err=%v, want ErrHostTarget", err)
	}
}

type execAPI struct {
	mu     sync.Mutex
	posted execPost
	stdout []byte
	stderr []byte
	code   int
}

type execPost struct {
	Command          []string          `json:"command"`
	WaitForWebsocket bool              `json:"wait-for-websocket"`
	RecordOutput     bool              `json:"record-output"`
	Interactive      bool              `json:"interactive"`
	Cwd              string            `json:"cwd"`
	Env              map[string]string `json:"environment"`
}

func (a *execAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/exec"):
		if err := json.NewDecoder(r.Body).Decode(&a.posted); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(apiResponse{
			Type:       "async",
			StatusCode: http.StatusAccepted,
			Operation:  "/1.0/operations/exec-1",
		})
	case r.Method == http.MethodGet && r.URL.Path == "/1.0/operations/exec-1/wait":
		writeSync(w, map[string]any{
			"status_code": 200,
			"err":         "",
			"metadata": map[string]any{
				"return": a.code,
				"output": map[string]string{
					"1": "/1.0/instances/obx-1/logs/stdout",
					"2": "/1.0/instances/obx-1/logs/stderr",
				},
			},
		})
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/logs/stdout"):
		_, _ = io.Copy(w, bytes.NewReader(a.stdout))
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/logs/stderr"):
		_, _ = io.Copy(w, bytes.NewReader(a.stderr))
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}
