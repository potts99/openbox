// SPDX-License-Identifier: AGPL-3.0-only

package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/clones"
	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/app/recovery"
	"github.com/openbox-dev/openbox/internal/clock"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/httpapi"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
	"github.com/openbox-dev/openbox/internal/snapshots"
)

// TestCheckpointReuseFanOut covers the Phase 2 no-live-host gate:
// create → checkpoint → restore + live clone → identity/provenance → restart recovery → cleanup.
func TestCheckpointReuseFanOut(t *testing.T) {
	ctx := context.Background()
	fakeClock := clock.NewFake(time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC))
	runtime := fake.New(runtimeapi.Capabilities{
		Architecture: "x86_64", Containers: true, StorageDrivers: []string{"zfs"},
	})
	runtime.AddImage(runtimeapi.Image{
		Fingerprint: "sha256:ubuntu", Aliases: []string{"ubuntu"},
		Architecture: "x86_64", Type: "container", CloudInit: true,
	})
	store := openStore(t)
	ids := sequentialIDs()
	instanceSvc, err := instances.New(runtime, store, instances.Options{
		Now: fakeClock.Now, NewID: ids, NetworkPolicy: nopPolicy{},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshotSvc, err := snapshots.New(runtime, store, snapshots.Options{Now: fakeClock.Now, NewID: ids})
	if err != nil {
		t.Fatal(err)
	}
	cloneSvc, err := clones.New(runtime, store, clones.Options{Now: fakeClock.Now, NewID: ids})
	if err != nil {
		t.Fatal(err)
	}
	executor := recovery.Executor{Instances: instanceSvc, Snapshots: snapshotSvc, Clones: cloneSvc}
	runOp := func(op domain.Operation) {
		t.Helper()
		if err := executor.Execute(ctx, op); err != nil {
			t.Fatalf("recover %s: %v", op.Type, err)
		}
	}

	source, createOp, err := instanceSvc.SubmitCreate(ctx, instances.CreateInput{
		OwnerID: "owner-1", Name: "golden", Kind: domain.KindVPS, Image: "ubuntu",
		OwnerPublicKey: "ssh-ed25519 owner", IdempotencyKey: "create-golden",
	})
	if err != nil {
		t.Fatal(err)
	}
	runOp(createOp)
	source, err = instanceSvc.GetInstance(ctx, "owner-1", source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if source.ObservedState != domain.ObservedRunning {
		t.Fatalf("source observed=%q", source.ObservedState)
	}

	handler, err := httpapi.New(instanceSvc, httpapi.Options{
		OwnerID: "owner-1", Snapshots: snapshotSvc, Clones: cloneSvc,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Checkpoint via HTTP.
	snapReq := httptest.NewRequest(http.MethodPost, "/v1/instances/"+string(source.ID)+"/snapshots", bytes.NewBufferString(`{"name":"ready"}`))
	snapReq.Header.Set("Content-Type", "application/json")
	snapReq.Header.Set(httpapi.HeaderIdempotencyKey, "snap-ready")
	snapResp := httptest.NewRecorder()
	handler.ServeHTTP(snapResp, snapReq)
	if snapResp.Code != http.StatusAccepted {
		t.Fatalf("snapshot status=%d body=%s", snapResp.Code, snapResp.Body.String())
	}
	var snapResult struct {
		Snapshot struct {
			ID string `json:"id"`
		} `json:"snapshot"`
		Operation struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"operation"`
	}
	if err := json.Unmarshal(snapResp.Body.Bytes(), &snapResult); err != nil {
		t.Fatal(err)
	}
	snapOp, err := store.GetOperation(ctx, "owner-1", domain.OperationID(snapResult.Operation.ID))
	if err != nil {
		t.Fatal(err)
	}
	runOp(snapOp)

	// Restore-as-new via HTTP.
	restoreBody := `{"name":"worker-a","owner_public_key":"ssh-ed25519 owner"}`
	restoreReq := httptest.NewRequest(http.MethodPost, "/v1/snapshots/"+snapResult.Snapshot.ID+"/restore", bytes.NewBufferString(restoreBody))
	restoreReq.Header.Set("Content-Type", "application/json")
	restoreReq.Header.Set(httpapi.HeaderIdempotencyKey, "restore-a")
	restoreResp := httptest.NewRecorder()
	handler.ServeHTTP(restoreResp, restoreReq)
	if restoreResp.Code != http.StatusAccepted {
		t.Fatalf("restore status=%d body=%s", restoreResp.Code, restoreResp.Body.String())
	}
	var restoreResult struct {
		Instance struct {
			ID                    string `json:"id"`
			CloneSourceInstanceID string `json:"clone_source_instance_id"`
			CloneSourceSnapshotID string `json:"clone_source_snapshot_id"`
			CloneSourceImageID    string `json:"clone_source_image_id"`
		} `json:"instance"`
		Operation struct {
			ID string `json:"id"`
		} `json:"operation"`
		StorageEfficiency string   `json:"storage_efficiency"`
		Warnings          []string `json:"warnings"`
	}
	if err := json.Unmarshal(restoreResp.Body.Bytes(), &restoreResult); err != nil {
		t.Fatal(err)
	}
	if restoreResult.StorageEfficiency != "confirmed" {
		t.Fatalf("storage_efficiency=%q", restoreResult.StorageEfficiency)
	}
	if restoreResult.Instance.CloneSourceInstanceID != string(source.ID) || restoreResult.Instance.CloneSourceSnapshotID != snapResult.Snapshot.ID {
		t.Fatalf("restore provenance=%+v", restoreResult.Instance)
	}
	restoreOp, err := store.GetOperation(ctx, "owner-1", domain.OperationID(restoreResult.Operation.ID))
	if err != nil {
		t.Fatal(err)
	}
	runOp(restoreOp)
	restored, err := instanceSvc.GetInstance(ctx, "owner-1", domain.InstanceID(restoreResult.Instance.ID))
	if err != nil {
		t.Fatal(err)
	}
	if restored.RuntimeRef == "" || restored.RuntimeRef == source.RuntimeRef {
		t.Fatalf("restored runtime ref not independent: %q", restored.RuntimeRef)
	}
	keys, ok := runtime.WrittenFile(restored.RuntimeRef, "/root/.ssh/authorized_keys")
	if !ok || keys != "ssh-ed25519 owner\n" {
		t.Fatalf("restored keys=%q ok=%v", keys, ok)
	}

	// Live clone via HTTP.
	cloneBody := `{"name":"worker-b","owner_public_key":"ssh-ed25519 owner"}`
	cloneReq := httptest.NewRequest(http.MethodPost, "/v1/instances/"+string(source.ID)+"/clone", bytes.NewBufferString(cloneBody))
	cloneReq.Header.Set("Content-Type", "application/json")
	cloneReq.Header.Set(httpapi.HeaderIdempotencyKey, "clone-b")
	cloneResp := httptest.NewRecorder()
	handler.ServeHTTP(cloneResp, cloneReq)
	if cloneResp.Code != http.StatusAccepted {
		t.Fatalf("clone status=%d body=%s", cloneResp.Code, cloneResp.Body.String())
	}
	var cloneResult struct {
		Instance struct {
			ID                    string `json:"id"`
			CloneSourceInstanceID string `json:"clone_source_instance_id"`
			CloneSourceSnapshotID string `json:"clone_source_snapshot_id"`
		} `json:"instance"`
		Operation struct {
			ID string `json:"id"`
		} `json:"operation"`
		StorageEfficiency string `json:"storage_efficiency"`
	}
	if err := json.Unmarshal(cloneResp.Body.Bytes(), &cloneResult); err != nil {
		t.Fatal(err)
	}
	if cloneResult.Instance.CloneSourceInstanceID != string(source.ID) || cloneResult.Instance.CloneSourceSnapshotID != "" {
		t.Fatalf("clone provenance=%+v", cloneResult.Instance)
	}
	cloneOp, err := store.GetOperation(ctx, "owner-1", domain.OperationID(cloneResult.Operation.ID))
	if err != nil {
		t.Fatal(err)
	}
	// Restart recovery: resume pending clone after "daemon restart".
	if err := executor.Execute(ctx, cloneOp); err != nil {
		t.Fatal(err)
	}
	cloned, err := instanceSvc.GetInstance(ctx, "owner-1", domain.InstanceID(cloneResult.Instance.ID))
	if err != nil {
		t.Fatal(err)
	}
	if cloned.RuntimeRef == restored.RuntimeRef || cloned.RuntimeRef == source.RuntimeRef {
		t.Fatalf("clone runtime ref collision: %q", cloned.RuntimeRef)
	}

	// Independent lifecycle: delete restored without affecting clone/source.
	delOp, err := instanceSvc.SubmitAction(ctx, "owner-1", restored.ID, instances.MutationDelete, "del-restored")
	if err != nil {
		t.Fatal(err)
	}
	runOp(delOp)
	if _, err := instanceSvc.GetInstance(ctx, "owner-1", restored.ID); err == nil {
		t.Fatal("restored should be gone")
	}
	if _, err := instanceSvc.GetInstance(ctx, "owner-1", cloned.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.InspectInstance(ctx, source.RuntimeRef); err != nil {
		t.Fatal(err)
	}

	// Full-copy warning path on dir storage.
	dirRuntime := fake.New(runtimeapi.Capabilities{
		Architecture: "x86_64", Containers: true, StorageDrivers: []string{"dir"},
	})
	dirRuntime.AddImage(runtimeapi.Image{
		Fingerprint: "sha256:ubuntu", Aliases: []string{"ubuntu"},
		Architecture: "x86_64", Type: "container", CloudInit: true,
	})
	if _, err := dirRuntime.CreateInstance(ctx, runtimeapi.CreateRequest{
		Ref: source.RuntimeRef, Image: "sha256:ubuntu", Unprivileged: true,
		Metadata: map[string]string{"user.openbox.managed": "true", "user.openbox.resource": "instance",
			"user.openbox.instance_id": string(source.ID), "user.openbox.owner_id": "owner-1"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := dirRuntime.StartInstance(ctx, source.RuntimeRef); err != nil {
		t.Fatal(err)
	}
	dirClones, err := clones.New(dirRuntime, store, clones.Options{Now: fakeClock.Now, NewID: ids})
	if err != nil {
		t.Fatal(err)
	}
	warnResult, err := dirClones.SubmitCopy(ctx, clones.CopyInput{
		OwnerID: "owner-1", Source: string(source.ID), Destination: "full-copy",
		OwnerPublicKey: "ssh-ed25519 owner", IdempotencyKey: "clone-full",
	})
	if err != nil {
		t.Fatal(err)
	}
	if warnResult.StorageEfficiency != "not_supported" {
		t.Fatalf("efficiency=%s", warnResult.StorageEfficiency)
	}
	found := false
	for _, warning := range warnResult.Warnings {
		if warning == clones.WarningFullCopy {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings=%v", warnResult.Warnings)
	}

	// Snapshot delete via HTTP after fan-out completes.
	delSnap := httptest.NewRequest(http.MethodDelete, "/v1/snapshots/"+snapResult.Snapshot.ID, nil)
	delSnap.Header.Set(httpapi.HeaderIdempotencyKey, "snap-del")
	delSnapResp := httptest.NewRecorder()
	handler.ServeHTTP(delSnapResp, delSnap)
	if delSnapResp.Code != http.StatusAccepted {
		t.Fatalf("delete snapshot status=%d", delSnapResp.Code)
	}
	delSnapOp, err := store.GetOperation(ctx, "owner-1", domain.OperationID(mustOpID(delSnapResp.Body.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	runOp(delSnapOp)
	if _, err := snapshotSvc.Get(ctx, "owner-1", domain.SnapshotID(snapResult.Snapshot.ID)); err == nil {
		t.Fatal("snapshot should be gone")
	}
	// Clone still usable after snapshot delete.
	if _, err := runtime.InspectInstance(ctx, cloned.RuntimeRef); err != nil {
		t.Fatalf("clone invalid after snapshot delete: %v", err)
	}
	if _, err := instanceSvc.GetInstance(ctx, "owner-1", cloned.ID); err != nil {
		t.Fatal(err)
	}
}

func mustOpID(body []byte) string {
	var payload struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &payload)
	return payload.ID
}
