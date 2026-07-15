// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestSnapshotCreateDeleteAndCopy(t *testing.T) {
	api := &snapshotAPI{
		instances: map[string]instanceRecord{},
		snapshots: map[string]map[string]bool{},
	}
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{SocketPath: socket, Project: "openbox"})
	if err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	api.instances["base"] = instanceRecord{Name: "base", Type: "container", Status: "Running", Config: map[string]string{}, ExpandedConfig: map[string]string{}}
	api.mu.Unlock()

	if err := adapter.CreateSnapshot(context.Background(), "base", "ready"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.CreateSnapshot(context.Background(), "base", "ready"); !errors.Is(err, runtimeapi.ErrAlreadyExists) {
		t.Fatalf("duplicate create err=%v", err)
	}
	copied, err := adapter.CopyInstance(context.Background(), runtimeapi.CopyRequest{SourceRef: "base", Snapshot: "ready", TargetRef: "feature"})
	if err != nil || copied.Ref != "feature" {
		t.Fatalf("copy=%+v err=%v", copied, err)
	}
	if err := adapter.DeleteSnapshot(context.Background(), "base", "ready"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.DeleteSnapshot(context.Background(), "base", "ready"); !errors.Is(err, runtimeapi.ErrNotFound) {
		t.Fatalf("missing delete err=%v", err)
	}
}

type snapshotAPI struct {
	mu        sync.Mutex
	instances map[string]instanceRecord
	snapshots map[string]map[string]bool
}

func (a *snapshotAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.instances == nil {
		a.instances = map[string]instanceRecord{}
	}
	if a.snapshots == nil {
		a.snapshots = map[string]map[string]bool{}
	}
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/snapshots"):
		ref := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/snapshots"), "/1.0/instances/")
		if _, ok := a.instances[ref]; !ok {
			writeError(w, http.StatusNotFound, "Instance not found")
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if a.snapshots[ref] == nil {
			a.snapshots[ref] = map[string]bool{}
		}
		if a.snapshots[ref][body.Name] {
			writeError(w, http.StatusConflict, "Snapshot already exists")
			return
		}
		a.snapshots[ref][body.Name] = true
		writeSync(w, nil)
	case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/snapshots/"):
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/1.0/instances/"), "/snapshots/")
		if len(parts) != 2 {
			writeError(w, http.StatusBadRequest, "bad path")
			return
		}
		ref, name := parts[0], parts[1]
		if !a.snapshots[ref][name] {
			writeError(w, http.StatusNotFound, "Snapshot not found")
			return
		}
		delete(a.snapshots[ref], name)
		writeSync(w, nil)
	case r.Method == http.MethodPost && r.URL.Path == "/1.0/instances":
		var body struct {
			Name   string            `json:"name"`
			Source map[string]string `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, exists := a.instances[body.Name]; exists {
			writeError(w, http.StatusConflict, "Instance already exists")
			return
		}
		source := body.Source["source"]
		if _, ok := a.instances[source]; !ok {
			writeError(w, http.StatusNotFound, "Source not found")
			return
		}
		if snap := body.Source["snapshot"]; snap != "" && !a.snapshots[source][snap] {
			writeError(w, http.StatusNotFound, "Snapshot not found")
			return
		}
		record := a.instances[source]
		record.Name = body.Name
		a.instances[body.Name] = record
		writeSync(w, nil)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/1.0/instances/"):
		ref := strings.TrimPrefix(r.URL.Path, "/1.0/instances/")
		record, ok := a.instances[ref]
		if !ok {
			writeError(w, http.StatusNotFound, "Instance not found")
			return
		}
		writeSync(w, record)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}
