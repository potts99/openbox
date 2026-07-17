// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/artifacts"
	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/persistence/sqlite"
)

func TestArtifactHandlersUploadListDownloadAndDelete(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir()+"/openbox.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	if err := store.CreateOwner(ctx, domain.Owner{ID: "owner-local", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureImage(ctx, domain.Image{
		ID: "image-1", OwnerID: "owner-local", Alias: "image", Source: "test", Digest: "sha256:test",
		Architecture: "x86_64", Compatibility: "container", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	instance, err := domain.NewInstance("instance-1", "owner-local", "demo", domain.KindVPS, now)
	if err != nil {
		t.Fatal(err)
	}
	instance.ImageID = "image-1"
	instance.ActualIsolation = domain.IsolationContainer
	instance.ObservedState = domain.ObservedRunning
	instance.RuntimeRef = "incus-instance-1"
	operation := domain.Operation{
		ID: "operation-1", OwnerID: "owner-local", Type: "instance.create", TargetType: "instance", TargetID: "instance-1",
		Status: domain.OperationPending, Stage: "queued", IdempotencyKey: "create-1", RequestHash: "hash", CreatedAt: now, UpdatedAt: now,
	}
	if _, _, err := store.CreateInstance(ctx, instance, operation); err != nil {
		t.Fatal(err)
	}
	artifactService, err := artifacts.New(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(&fakeService{}, Options{OwnerID: "owner-local", Artifacts: artifactService})
	if err != nil {
		t.Fatal(err)
	}

	put := httptest.NewRequest(http.MethodPut, "/v1/instances/instance-1/artifacts/results/summary.json", bytes.NewBufferString(`{"ok":true}`))
	put.Header.Set("Content-Type", "application/json")
	put.Header.Set(HeaderIdempotencyKey, "artifact-1")
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", putResponse.Code, putResponse.Body.String())
	}
	replay := httptest.NewRequest(http.MethodPut, "/v1/instances/instance-1/artifacts/results/summary.json", bytes.NewBufferString(`{"ok":true}`))
	replay.Header.Set("Content-Type", "application/json")
	replay.Header.Set(HeaderIdempotencyKey, "artifact-1")
	replayResponse := httptest.NewRecorder()
	handler.ServeHTTP(replayResponse, replay)
	if replayResponse.Code != http.StatusOK || replayResponse.Body.String() != putResponse.Body.String() {
		t.Fatalf("replay status=%d body=%s", replayResponse.Code, replayResponse.Body.String())
	}

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/instances/instance-1/artifacts?prefix=results/", nil))
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte(`"path":"results/summary.json"`)) {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}

	download := httptest.NewRecorder()
	handler.ServeHTTP(download, httptest.NewRequest(http.MethodGet, "/v1/instances/instance-1/artifacts/results/summary.json/content", nil))
	if download.Code != http.StatusOK || download.Body.String() != `{"ok":true}` || download.Header().Get("ETag") == "" {
		t.Fatalf("download status=%d body=%q headers=%v", download.Code, download.Body.String(), download.Header())
	}

	remove := httptest.NewRecorder()
	handler.ServeHTTP(remove, httptest.NewRequest(http.MethodDelete, "/v1/instances/instance-1/artifacts/results/summary.json", nil))
	if remove.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", remove.Code, remove.Body.String())
	}

	authManager, err := auth.New(store)
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := New(&fakeService{}, Options{Auth: authManager, Artifacts: artifactService})
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := httptest.NewRecorder()
	authenticated.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/instances/instance-1/artifacts", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
}
