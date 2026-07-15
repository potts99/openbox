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
	service, err := instances.New(runtime, store, instances.Options{Now: func() time.Time { return now }, NewID: func() string { value := ids[0]; ids = ids[1:]; return value }, NetworkPolicy: runtime})
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

func TestCreateAndExtendResponsesIncludeNetworkPolicy(t *testing.T) {
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
	policy := integrationNetworkPolicy{status: domain.NetworkPolicyStatus{
		EgressMode: domain.EgressRestricted,
		ACLs:       []string{"openbox-default-deny"},
		Resolution: domain.AllowlistResolution{State: "idle", Pending: []string{}, Resolved: []string{}, Failed: []string{}},
	}}
	service, err := instances.New(runtime, store, instances.Options{
		Now: func() time.Time { return now },
		NewID: func() string {
			return "instance-1"
		},
		NetworkPolicy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(service, Options{OwnerID: "owner-local"})
	if err != nil {
		t.Fatal(err)
	}

	create := httptest.NewRequest(http.MethodPost, "/v1/instances", bytes.NewBufferString(`{"name":"sandbox","kind":"sandbox","image":"ubuntu","owner_public_key":"ssh-ed25519 owner"}`))
	create.Header.Set(HeaderIdempotencyKey, "policy-create")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	assertJSONContains(t, createResponse.Body.Bytes(), `"egress_mode":"restricted"`, `"acls":["openbox-default-deny"]`, `"state":"idle"`, `"denied_flows":0`)

	replay := httptest.NewRequest(http.MethodPost, "/v1/instances", bytes.NewBufferString(`{"name":"sandbox","kind":"sandbox","image":"ubuntu","owner_public_key":"ssh-ed25519 owner"}`))
	replay.Header.Set(HeaderIdempotencyKey, "policy-create")
	replayResponse := httptest.NewRecorder()
	handler.ServeHTTP(replayResponse, replay)
	if replayResponse.Code != http.StatusAccepted {
		t.Fatalf("replay status=%d body=%s", replayResponse.Code, replayResponse.Body.String())
	}
	assertJSONContains(t, replayResponse.Body.Bytes(), `"egress_mode":"restricted"`, `"acls":["openbox-default-deny"]`, `"state":"idle"`, `"denied_flows":0`)

	extend := httptest.NewRequest(http.MethodPost, "/v1/instances/instance-1/extend", bytes.NewBufferString(`{"duration_seconds":60}`))
	extendResponse := httptest.NewRecorder()
	handler.ServeHTTP(extendResponse, extend)
	if extendResponse.Code != http.StatusOK {
		t.Fatalf("extend status=%d body=%s", extendResponse.Code, extendResponse.Body.String())
	}
	assertJSONContains(t, extendResponse.Body.Bytes(), `"egress_mode":"restricted"`, `"acls":["openbox-default-deny"]`, `"state":"idle"`, `"denied_flows":0`)
}

type integrationNetworkPolicy struct {
	status domain.NetworkPolicyStatus
}

func (p integrationNetworkPolicy) ApplyNetworkPolicy(context.Context, domain.Instance) error {
	return nil
}
func (p integrationNetworkPolicy) RemoveNetworkPolicy(context.Context, domain.Instance) error {
	return nil
}
func (p integrationNetworkPolicy) NetworkPolicyStatus(domain.Instance) domain.NetworkPolicyStatus {
	return p.status
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
