// SPDX-License-Identifier: AGPL-3.0-only

package client

import (
	"context"
	"errors"
	"fmt"
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
		_, _ = w.Write([]byte(`{"items":[{"id":"box-1","kind":"quantum","requested_isolation":"best_available","desired_state":"running","observed_state":"running","actual_isolation":"container"}]}`))
	}))
	defer server.Close()

	_, err := newTestClient(t, server.URL).ListInstances(context.Background())
	if err == nil || !strings.Contains(err.Error(), `instance "box-1"`) || !strings.Contains(err.Error(), `kind "quantum"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestClientToleratesAdditiveUnknownFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"id":"box-1","kind":"vps","requested_isolation":"standard","desired_state":"running","observed_state":"running","actual_isolation":"container","future_instance_field":true}],"future_envelope_field":{"enabled":true}}`))
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

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := New(Options{BaseURL: baseURL, HTTPClient: &http.Client{Timeout: time.Second}, MaxRetries: 2, RetryWait: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
