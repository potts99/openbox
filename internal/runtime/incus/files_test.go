// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestWriteFilePostsOctetStreamToInstanceFiles(t *testing.T) {
	t.Parallel()
	api := &filesAPI{}
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{SocketPath: socket, Project: "openbox", HostProbe: staticProbe{}})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte("h"), 2<<20) // 2 MiB — larger than exec stdin wrap
	err = adapter.WriteFile(context.Background(), runtimeapi.WriteFileRequest{
		Ref:  "obx-1",
		Path: "/usr/local/bin/herdr.openbox-tmp",
		Body: bytes.NewReader(body),
		Mode: 0o755,
		UID:  0,
		GID:  0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if api.method != http.MethodPost {
		t.Fatalf("method=%s", api.method)
	}
	if !strings.HasSuffix(api.path, "/1.0/instances/obx-1/files") {
		t.Fatalf("path=%s", api.path)
	}
	if api.query.Get("project") != "openbox" || api.query.Get("path") != "/usr/local/bin/herdr.openbox-tmp" {
		t.Fatalf("query=%v", api.query)
	}
	if api.contentType != "application/octet-stream" {
		t.Fatalf("content-type=%q", api.contentType)
	}
	if api.headers.Get("X-Incus-type") != "file" {
		t.Fatalf("type=%q", api.headers.Get("X-Incus-type"))
	}
	if api.headers.Get("X-Incus-mode") != "0755" {
		t.Fatalf("mode=%q", api.headers.Get("X-Incus-mode"))
	}
	if api.headers.Get("X-Incus-uid") != "0" || api.headers.Get("X-Incus-gid") != "0" {
		t.Fatalf("uid/gid=%q/%q", api.headers.Get("X-Incus-uid"), api.headers.Get("X-Incus-gid"))
	}
	if api.headers.Get("X-Incus-write") != "overwrite" {
		t.Fatalf("write=%q", api.headers.Get("X-Incus-write"))
	}
	if !bytes.Equal(api.body, body) {
		t.Fatalf("body len=%d want %d", len(api.body), len(body))
	}
}

func TestWriteFileRejectsHostTarget(t *testing.T) {
	t.Parallel()
	adapter, err := New(Options{SocketPath: "/tmp/incus.socket", HostProbe: staticProbe{}})
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.WriteFile(context.Background(), runtimeapi.WriteFileRequest{
		Ref:  "host",
		Path: "/tmp/x",
		Body: bytes.NewReader([]byte("x")),
		Mode: 0o644,
	})
	if err != runtimeapi.ErrHostTarget {
		t.Fatalf("err=%v, want ErrHostTarget", err)
	}
}

type filesAPI struct {
	mu          sync.Mutex
	method      string
	path        string
	query       url.Values
	contentType string
	headers     http.Header
	body        []byte
}

func (a *filesAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.method = r.Method
	a.path = r.URL.Path
	a.query = r.URL.Query()
	a.contentType = r.Header.Get("Content-Type")
	a.headers = r.Header.Clone()
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.body = body
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/files") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "sync", "status": "Success", "status_code": 200, "metadata": map[string]any{}})
}
