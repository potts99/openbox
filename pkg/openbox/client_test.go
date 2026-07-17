// SPDX-License-Identifier: AGPL-3.0-only

package openbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNegotiatesV1AndSendsVersionHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(APIVersionHeader); got != APIVersionV1 {
			t.Fatalf("version header = %q", got)
		}
		w.Header().Set(APIVersionHeader, APIVersionV1)
		_, _ = w.Write([]byte(`{"status":"ok","server_version":"v0.1.0","api_version":"v1"}`))
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	health, err := c.Negotiate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.ServerVersion != "v0.1.0" || c.ServerVersion() != "v0.1.0" {
		t.Fatalf("health = %#v, client server version = %q", health, c.ServerVersion())
	}
}

func TestSendsConfiguredBearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer owner-token" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"status":"ok","server_version":"v0.1.0","api_version":"v1"}`))
	}))
	defer server.Close()

	c, err := New(Options{BaseURL: server.URL, Token: "owner-token"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNegotiationRejectsUnsupportedServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","api_version":"v2"}`))
	}))
	defer server.Close()

	_, err := newTestClient(t, server.URL).Negotiate(context.Background())
	var versionErr *VersionError
	if !errors.As(err, &versionErr) || versionErr.Wanted != APIVersionV1 {
		t.Fatalf("error = %#v", err)
	}
}

func TestTypedAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"idempotency_conflict","message":"key reused","field":"idempotency_key","retryable":false,"request_id":"req-1"}}`))
	}))
	defer server.Close()

	_, err := newTestClient(t, server.URL).GetInstance(context.Background(), "missing")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict || apiErr.Code != "idempotency_conflict" || apiErr.RequestID != "req-1" {
		t.Fatalf("error = %#v", err)
	}
}

func TestRetriesSafeReadsAndIdempotentMutations(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*Client) error
	}{
		{name: "read", run: func(c *Client) error { _, err := c.ListInstances(context.Background()); return err }},
		{name: "idempotent mutation", run: func(c *Client) error { _, err := c.StartInstance(context.Background(), "box-1", "key-1"); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"try again","retryable":true}}`))
					return
				}
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"items":[]}`))
					return
				}
				_, _ = w.Write([]byte(`{"id":"op-1","status":"pending"}`))
			}))
			defer server.Close()
			c := newTestClient(t, server.URL)
			if err := test.run(c); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 2 {
				t.Fatalf("calls = %d", calls.Load())
			}
		})
	}
}

func TestDoesNotRetryMutationWithoutIdempotencyKey(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"unavailable","retryable":true}}`))
	}))
	defer server.Close()

	_, err := newTestClient(t, server.URL).StartInstance(context.Background(), "box-1", "")
	if err == nil || calls.Load() != 0 {
		t.Fatalf("error = %v, calls = %d", err, calls.Load())
	}
}

func TestRetriesInterruptedSuccessfulMutationResponseWithSameKey(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Idempotency-Key") != "stable-key" {
			t.Fatalf("idempotency key=%q", r.Header.Get("Idempotency-Key"))
		}
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"id":"operation-1","status":`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"operation-1","status":"pending"}`))
	}))
	defer server.Close()

	result, err := newTestClient(t, server.URL).StartInstance(context.Background(), "box-1", "stable-key")
	if err != nil || result.Operation.ID != "operation-1" || calls.Load() != 2 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls.Load(), err)
	}
}

func TestUnknownEnumsFailWithContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"id":"box-1","kind":"quantum","requested_isolation":"strong","desired_state":"running","observed_state":"running","actual_isolation":"container"}]}`))
	}))
	defer server.Close()

	_, err := newTestClient(t, server.URL).ListInstances(context.Background())
	if err == nil || !strings.Contains(err.Error(), `instance "box-1"`) || !strings.Contains(err.Error(), `kind "quantum"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestClientToleratesAdditiveUnknownFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"id":"box-1","kind":"vps","requested_isolation":"container","desired_state":"running","observed_state":"running","actual_isolation":"container","future_instance_field":true}],"future_envelope_field":{"enabled":true}}`))
	}))
	defer server.Close()

	instances, err := newTestClient(t, server.URL).ListInstances(context.Background())
	if err != nil || len(instances) != 1 || instances[0].ID != "box-1" {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
}

func TestWatchOperationParsesSSEAndResumes(t *testing.T) {
	var eventCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/operations/op-1" {
			_, _ = fmt.Fprint(w, `{"id":"op-1","status":"running"}`)
			return
		}
		call := eventCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			_, _ = fmt.Fprint(w, "id: 4\nevent: operation\ndata: {\"sequence\":4,\"operation_id\":\"op-1\",\"status\":\"running\",\"stage\":\"booting\"}\n\n")
			return
		}
		if got := r.Header.Get("Last-Event-ID"); got != "4" {
			t.Fatalf("Last-Event-ID = %q", got)
		}
		_, _ = fmt.Fprint(w, "id: 5\nevent: operation\ndata: {\"sequence\":5,\"operation_id\":\"op-1\",\"status\":\"succeeded\",\"stage\":\"complete\",\"progress\":100}\n\n")
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	events, errs := c.WatchOperation(ctx, "op-1", WatchOptions{Reconnect: true})
	var got []OperationEvent
	for event := range events {
		got = append(got, event)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Status != OperationSucceeded || eventCalls.Load() != 2 {
		t.Fatalf("events = %#v, calls = %d", got, eventCalls.Load())
	}
}

func TestWatchOperationIgnoresOverallHTTPClientTimeout(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(started)
		time.Sleep(150 * time.Millisecond)
		_, _ = fmt.Fprint(w, "id: 1\nevent: operation\ndata: {\"sequence\":1,\"operation_id\":\"op-1\",\"status\":\"succeeded\",\"stage\":\"complete\",\"progress\":100}\n\n")
	}))
	defer server.Close()

	c, err := New(Options{BaseURL: server.URL, HTTPClient: &http.Client{Timeout: 40 * time.Millisecond}, MaxRetries: 0, RetryWait: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	events, errs := c.WatchOperation(ctx, "op-1", WatchOptions{})
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("server never started streaming")
	}
	var got []OperationEvent
	for event := range events {
		got = append(got, event)
	}
	if err := <-errs; err != nil {
		t.Fatalf("watch failed under short client timeout: %v", err)
	}
	if len(got) != 1 || got[0].Status != OperationSucceeded {
		t.Fatalf("events = %#v", got)
	}
}

func TestWatchOperationStopsWhenTerminalEventWasAlreadyConsumed(t *testing.T) {
	var eventCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/operations/op-1/events" {
			eventCalls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			return
		}
		_, _ = fmt.Fprint(w, `{"id":"op-1","status":"succeeded"}`)
	}))
	defer server.Close()

	events, errs := newTestClient(t, server.URL).WatchOperation(context.Background(), "op-1", WatchOptions{AfterSequence: 9, Reconnect: true})
	for range events {
		t.Fatal("unexpected event")
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if eventCalls.Load() != 1 {
		t.Fatalf("event calls = %d", eventCalls.Load())
	}
}

func TestCheckpointClientRoutesAndIdempotency(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/instances/instance-1/snapshots":
			if r.Method == http.MethodGet {
				_, _ = fmt.Fprint(w, `{"items":[{"id":"snap-1","instance_id":"instance-1","name":"ready","ready":true,"created_at":"2026-07-16T12:00:00Z"}]}`)
				return
			}
			if r.Header.Get("Idempotency-Key") != "snap-key" {
				t.Fatalf("snapshot idempotency=%q", r.Header.Get("Idempotency-Key"))
			}
			_, _ = fmt.Fprint(w, `{"snapshot":{"id":"snap-1","instance_id":"instance-1","name":"ready","ready":false,"created_at":"2026-07-16T12:00:00Z"},"operation":{"id":"op-1","status":"pending"}}`)
		case "/v1/snapshots/snap-1/restore":
			if r.Header.Get("Idempotency-Key") != "restore-key" {
				t.Fatalf("restore idempotency=%q", r.Header.Get("Idempotency-Key"))
			}
			_, _ = fmt.Fprint(w, `{"instance":{"id":"instance-2","name":"restored","kind":"vps","requested_isolation":"container","actual_isolation":"container","desired_state":"running","observed_state":"creating","resources":{},"protected":false,"network_policy":{},"created_at":"2026-07-16T12:00:00Z","updated_at":"2026-07-16T12:00:00Z"},"operation":{"id":"op-2","status":"pending"},"warnings":[],"storage_efficiency":"confirmed"}`)
		case "/v1/instances/instance-1/clone":
			if r.Header.Get("Idempotency-Key") != "clone-key" {
				t.Fatalf("clone idempotency=%q", r.Header.Get("Idempotency-Key"))
			}
			_, _ = fmt.Fprint(w, `{"operation":{"id":"op-3","status":"pending"},"warnings":[],"storage_efficiency":"not_supported"}`)
		default:
			t.Fatalf("unexpected route %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c := newTestClient(t, server.URL)
	items, err := c.ListSnapshots(context.Background(), "instance-1")
	if err != nil || len(items) != 1 || !items[0].Ready {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if _, err := c.CreateSnapshot(context.Background(), "instance-1", "ready", "snap-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RestoreSnapshot(context.Background(), "snap-1", RestoreSnapshotRequest{Name: "restored", OwnerPublicKey: "ssh-ed25519 owner"}, "restore-key"); err != nil {
		t.Fatal(err)
	}
	if result, err := c.CloneInstance(context.Background(), "instance-1", CloneInstanceRequest{Name: "cloned", OwnerPublicKey: "ssh-ed25519 owner"}, "clone-key"); err != nil || result.StorageEfficiency != "not_supported" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestArtifactClientRoutes(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/instances/instance-1/artifacts":
			if r.Method != http.MethodGet || r.URL.Query().Get("prefix") != "results/" {
				t.Fatalf("list request method=%s query=%q", r.Method, r.URL.RawQuery)
			}
			_, _ = fmt.Fprint(w, `{"items":[{"id":"artifact-1","instance_id":"instance-1","path":"results/out.txt","size_bytes":2,"content_type":"text/plain","sha256":"abc"}]}`)
		case "/v1/instances/instance-1/artifacts/results/out.txt":
			if r.Method == http.MethodPut {
				if r.Header.Get("Content-Type") != "text/plain" || r.Header.Get("Idempotency-Key") != "artifact-key" {
					t.Fatalf("upload headers=%v", r.Header)
				}
				_, _ = fmt.Fprint(w, `{"id":"artifact-1","instance_id":"instance-1","path":"results/out.txt","size_bytes":2,"content_type":"text/plain","sha256":"abc"}`)
				return
			}
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			t.Fatalf("unexpected artifact method %s", r.Method)
		case "/v1/instances/instance-1/artifacts/results/out.txt/content":
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("ETag", `"abc"`)
			_, _ = fmt.Fprint(w, "ok")
		default:
			t.Fatalf("unexpected route %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c := newTestClient(t, server.URL)
	if _, err := c.PutArtifact(context.Background(), "instance-1", "results/out.txt", strings.NewReader("ok"), 2, "text/plain", "artifact-key"); err != nil {
		t.Fatal(err)
	}
	items, err := c.ListArtifacts(context.Background(), "instance-1", "results/")
	if err != nil || len(items) != 1 || items[0].Path != "results/out.txt" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	download, err := c.GetArtifact(context.Background(), "instance-1", "results/out.txt")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(download.Body)
	_ = download.Body.Close()
	if readErr != nil || string(body) != "ok" || download.SHA256 != "abc" {
		t.Fatalf("download=%+v body=%q err=%v", download, body, readErr)
	}
	if err := c.DeleteArtifact(context.Background(), "instance-1", "results/out.txt"); err != nil {
		t.Fatal(err)
	}
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := New(Options{BaseURL: baseURL, HTTPClient: &http.Client{Timeout: time.Second}, MaxRetries: 2, RetryWait: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
