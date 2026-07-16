// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/execstream"
)

func TestExecStreamsNDJSONFrames(t *testing.T) {
	t.Parallel()
	service := &fakeService{
		execFrames: []execstream.Frame{
			execstream.StdoutFrame{Data: []byte("hello\n")},
			execstream.ExitFrame{Code: 0},
		},
	}
	handler := newTestHandler(t, service)
	body := bytes.NewBufferString(`{"argv":["echo","hello"],"working_dir":"/tmp"}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/instances/box-1/exec", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/x-ndjson" {
		t.Fatalf("content-type=%q", got)
	}
	if service.lastInstanceID != "box-1" || len(service.execReq.Argv) != 2 {
		t.Fatalf("exec req=%+v id=%q", service.execReq, service.lastInstanceID)
	}
	scanner := bufio.NewScanner(response.Body)
	var frames []execstream.Frame
	for scanner.Scan() {
		frame, err := execstream.Decode(scanner.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
	if len(frames) != 2 {
		t.Fatalf("frames=%d", len(frames))
	}
	out, ok := frames[0].(execstream.StdoutFrame)
	if !ok || string(out.Data) != "hello\n" {
		t.Fatalf("stdout=%#v", frames[0])
	}
	exit, ok := frames[1].(execstream.ExitFrame)
	if !ok || exit.Code != 0 {
		t.Fatalf("exit=%#v", frames[1])
	}
}

func TestExtendUpdatesExpiry(t *testing.T) {
	t.Parallel()
	expires := time.Date(2026, 7, 15, 14, 0, 0, 0, time.UTC)
	service := &fakeService{
		extended: domain.Instance{
			ID: "box-1", OwnerID: "owner-local", Name: "box", Kind: domain.KindSandbox,
			RequestedIsolation: domain.IsolationStrong, ActualIsolation: domain.IsolationContainer,
			DesiredState: domain.DesiredRunning, ObservedState: domain.ObservedRunning,
			ExpiresAt: &expires, CreatedAt: expires.Add(-time.Hour), UpdatedAt: expires,
		},
	}
	handler := newTestHandler(t, service)
	body := bytes.NewBufferString(`{"duration_seconds":1800}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/instances/box-1/extend", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if service.extendBy != 30*time.Minute {
		t.Fatalf("extendBy=%v", service.extendBy)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["id"] != "box-1" {
		t.Fatalf("payload=%v", payload)
	}
}
