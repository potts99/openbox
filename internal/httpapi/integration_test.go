// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/app/recovery"
	"github.com/openbox-dev/openbox/internal/clock"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/operations"
	"github.com/openbox-dev/openbox/internal/persistence/sqlite"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
)

func TestRealServiceSubmissionLostResponseAndWorkerCompletion(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir()+"/openbox.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	if err := store.CreateOwner(ctx, domain.Owner{ID: "owner-local", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	runtime := fake.New(runtimeapi.Capabilities{Architecture: "x86_64", Containers: true})
	runtime.AddImage(runtimeapi.Image{Fingerprint: "sha256:ubuntu", Aliases: []string{"ubuntu"}, Architecture: "x86_64", Type: "container", CloudInit: true})
	ids := []string{"instance-1", "operation-1"}
	service, err := instances.New(runtime, store, instances.Options{Now: func() time.Time { return now }, NewID: func() string { value := ids[0]; ids = ids[1:]; return value }})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(service, Options{OwnerID: "owner-local"})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"name":"dev","kind":"devbox","image":"ubuntu","owner_public_key":"ssh-ed25519 owner","requested_isolation":"standard","resources":{"vcpus":2,"memory_bytes":1024,"disk_bytes":2048}}`)

	first := submitCreate(t, handler, body, "lost-response")
	second := submitCreate(t, handler, body, "lost-response")
	if first.Operation.ID != second.Operation.ID || len(runtime.CreateRequests()) != 0 {
		t.Fatalf("first=%+v second=%+v runtime creates=%d", first, second, len(runtime.CreateRequests()))
	}

	worker, err := operations.NewWorker(store, recovery.Executor{Instances: service}, operations.Config{WorkerID: "api-integration", Concurrency: 1, Lease: time.Minute, Clock: clock.NewFake(now)})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/operations/"+first.Operation.ID, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"status":"succeeded"`)) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(runtime.CreateRequests()) != 1 {
		t.Fatalf("runtime creates=%d", len(runtime.CreateRequests()))
	}
}

type integrationCreateResult struct {
	Instance struct {
		ID string `json:"id"`
	} `json:"instance"`
	Operation struct {
		ID string `json:"id"`
	} `json:"operation"`
}

func submitCreate(t *testing.T, handler http.Handler, body []byte, key string) integrationCreateResult {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/instances", bytes.NewReader(body))
	request.Header.Set(HeaderIdempotencyKey, key)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result integrationCreateResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
