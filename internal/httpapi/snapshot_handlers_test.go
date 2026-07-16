// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/clones"
	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/domain"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
	"github.com/openbox-dev/openbox/internal/snapshots"
)

func TestSnapshotCloneRestoreHTTPContract(t *testing.T) {
	t.Parallel()
	handler, runtime, repo := newCheckpointHTTP(t)

	createSnap := httptest.NewRequest(http.MethodPost, "/v1/instances/inst-1/snapshots", bytes.NewBufferString(`{"name":"ready"}`))
	createSnap.Header.Set("Content-Type", "application/json")
	createSnap.Header.Set(HeaderIdempotencyKey, "snap-create")
	createResp := httptest.NewRecorder()
	handler.ServeHTTP(createResp, createSnap)
	if createResp.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	assertJSONContains(t, createResp.Body.Bytes(), `"type":"snapshot.create"`, `"name":"ready"`)

	var snapID string
	for id := range repo.snapshots {
		snapID = string(id)
	}
	if snapID == "" {
		t.Fatal("snapshot not recorded")
	}
	op := findPendingOp(repo, "snapshot.create")
	if err := repo.snapshotSvc.RecoverOperation(context.Background(), op); err != nil {
		t.Fatal(err)
	}

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/instances/inst-1/snapshots", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d", list.Code)
	}
	assertJSONContains(t, list.Body.Bytes(), `"name":"ready"`)

	restoreBody := `{"name":"restored","owner_public_key":"ssh-ed25519 owner"}`
	restoreReq := httptest.NewRequest(http.MethodPost, "/v1/snapshots/"+snapID+"/restore", bytes.NewBufferString(restoreBody))
	restoreReq.Header.Set("Content-Type", "application/json")
	restoreReq.Header.Set(HeaderIdempotencyKey, "restore-1")
	restoreResp := httptest.NewRecorder()
	handler.ServeHTTP(restoreResp, restoreReq)
	if restoreResp.Code != http.StatusAccepted {
		t.Fatalf("restore status=%d body=%s", restoreResp.Code, restoreResp.Body.String())
	}
	assertJSONContains(t, restoreResp.Body.Bytes(), `"type":"snapshot.restore"`, `"storage_efficiency":"confirmed"`, `"clone_source_snapshot_id":"`+snapID+`"`)

	restoreOp := findPendingOp(repo, "snapshot.restore")
	if err := repo.snapshotSvc.RecoverOperation(context.Background(), restoreOp); err != nil {
		t.Fatal(err)
	}
	restored := findInstanceByName(repo, "restored")
	keys, ok := runtime.WrittenFile(restored.RuntimeRef, "/root/.ssh/authorized_keys")
	if !ok || keys != "ssh-ed25519 owner\n" {
		t.Fatalf("authorized_keys=%q ok=%v", keys, ok)
	}

	cloneBody := `{"name":"cloned","owner_public_key":"ssh-ed25519 owner"}`
	cloneReq := httptest.NewRequest(http.MethodPost, "/v1/instances/inst-1/clone", bytes.NewBufferString(cloneBody))
	cloneReq.Header.Set("Content-Type", "application/json")
	cloneReq.Header.Set(HeaderIdempotencyKey, "clone-1")
	cloneResp := httptest.NewRecorder()
	handler.ServeHTTP(cloneResp, cloneReq)
	if cloneResp.Code != http.StatusAccepted {
		t.Fatalf("clone status=%d body=%s", cloneResp.Code, cloneResp.Body.String())
	}
	assertJSONContains(t, cloneResp.Body.Bytes(), `"type":"instance.copy"`, `"storage_efficiency":"confirmed"`)
	replayReq := httptest.NewRequest(http.MethodPost, "/v1/instances/inst-1/clone", bytes.NewBufferString(cloneBody))
	replayReq.Header.Set("Content-Type", "application/json")
	replayReq.Header.Set(HeaderIdempotencyKey, "clone-1")
	replayResp := httptest.NewRecorder()
	handler.ServeHTTP(replayResp, replayReq)
	if replayResp.Code != http.StatusAccepted {
		t.Fatalf("replay status=%d body=%s", replayResp.Code, replayResp.Body.String())
	}
	assertJSONContains(t, replayResp.Body.Bytes(), `"storage_efficiency":"confirmed"`)

	delReq := httptest.NewRequest(http.MethodDelete, "/v1/snapshots/"+snapID, nil)
	delReq.Header.Set(HeaderIdempotencyKey, "snap-del")
	delResp := httptest.NewRecorder()
	handler.ServeHTTP(delResp, delReq)
	if delResp.Code != http.StatusAccepted {
		t.Fatalf("delete status=%d body=%s", delResp.Code, delResp.Body.String())
	}
}

func TestCloneHTTPRequiresIdempotencyKey(t *testing.T) {
	t.Parallel()
	handler, _, _ := newCheckpointHTTP(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/instances/inst-1/clone", bytes.NewBufferString(`{"name":"x","owner_public_key":"ssh-ed25519 owner"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", resp.Code)
	}
	assertJSONContains(t, resp.Body.Bytes(), `"field":"Idempotency-Key"`)
}

type checkpointRepo struct {
	instances   map[domain.InstanceID]domain.Instance
	snapshots   map[domain.SnapshotID]domain.Snapshot
	ops         map[string]domain.Operation
	software    map[domain.InstanceID][]domain.InstanceSoftware
	snapshotSvc *snapshots.Service
	cloneSvc    *clones.Service
}

func newCheckpointHTTP(t *testing.T) (http.Handler, *fake.Runtime, *checkpointRepo) {
	t.Helper()
	runtime := fake.New(runtimeapi.Capabilities{Architecture: "x86_64", Containers: true, StorageDrivers: []string{"zfs"}})
	repo := &checkpointRepo{
		instances: map[domain.InstanceID]domain.Instance{},
		snapshots: map[domain.SnapshotID]domain.Snapshot{},
		ops:       map[string]domain.Operation{},
		software:  map[domain.InstanceID][]domain.InstanceSoftware{},
	}
	n := 0
	newID := func() string {
		n++
		return fmt.Sprintf("gen-%d", n)
	}
	now := func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) }
	snapSvc, err := snapshots.New(runtime, repo, snapshots.Options{Now: now, NewID: newID})
	if err != nil {
		t.Fatal(err)
	}
	cloneSvc, err := clones.New(runtime, repo, clones.Options{Now: now, NewID: newID})
	if err != nil {
		t.Fatal(err)
	}
	repo.snapshotSvc = snapSvc
	repo.cloneSvc = cloneSvc

	nowT := now()
	source := domain.Instance{
		ID: "inst-1", OwnerID: "owner-local", Name: "base", Kind: domain.KindVPS,
		ImageID: "sha256:ubuntu", RequestedIsolation: domain.IsolationContainerReq, ActualIsolation: domain.IsolationContainer,
		DesiredState: domain.DesiredRunning, ObservedState: domain.ObservedRunning, Protected: true,
		RuntimeRef: "openbox-inst-1", CreatedAt: nowT, UpdatedAt: nowT,
	}
	repo.instances[source.ID] = source
	runtime.AddImage(runtimeapi.Image{Fingerprint: "sha256:ubuntu", Aliases: []string{"ubuntu"}, Architecture: "x86_64", Type: "container", CloudInit: true})
	if _, err := runtime.CreateInstance(context.Background(), runtimeapi.CreateRequest{
		Ref: source.RuntimeRef, Image: "sha256:ubuntu", Unprivileged: true,
		Metadata: map[string]string{
			instances.MetadataManaged: "true", instances.MetadataResource: "instance",
			instances.MetadataInstanceID: string(source.ID), instances.MetadataOwnerID: string(source.OwnerID),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.StartInstance(context.Background(), source.RuntimeRef); err != nil {
		t.Fatal(err)
	}

	handler := newTestHandlerWithOptions(t, &fakeService{
		instances: []domain.Instance{source},
	}, Options{OwnerID: "owner-local", Snapshots: snapSvc, Clones: cloneSvc})
	return handler, runtime, repo
}

func findPendingOp(repo *checkpointRepo, typ string) domain.Operation {
	for _, op := range repo.ops {
		if op.Type == typ && op.Status == domain.OperationPending {
			return op
		}
	}
	return domain.Operation{}
}

func findInstanceByName(repo *checkpointRepo, name string) domain.Instance {
	for _, instance := range repo.instances {
		if instance.Name == name {
			return instance
		}
	}
	return domain.Instance{}
}

func (m *checkpointRepo) ListInstancesByOwner(_ context.Context, owner domain.OwnerID, _ int) ([]domain.Instance, error) {
	out := make([]domain.Instance, 0)
	for _, instance := range m.instances {
		if instance.OwnerID == owner && instance.DeletedAt == nil {
			out = append(out, instance)
		}
	}
	return out, nil
}
func (m *checkpointRepo) GetInstance(_ context.Context, owner domain.OwnerID, id domain.InstanceID) (domain.Instance, error) {
	instance, ok := m.instances[id]
	if !ok || instance.OwnerID != owner {
		return domain.Instance{}, &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	}
	return instance, nil
}
func (m *checkpointRepo) CreateInstance(_ context.Context, instance domain.Instance, operation domain.Operation) (domain.Operation, bool, error) {
	for _, op := range m.ops {
		if op.OwnerID == operation.OwnerID && op.IdempotencyKey == operation.IdempotencyKey {
			return op, true, nil
		}
	}
	m.instances[instance.ID] = instance
	m.ops[string(operation.ID)] = operation
	return operation, false, nil
}
func (m *checkpointRepo) UpdateInstanceObservation(_ context.Context, owner domain.OwnerID, id domain.InstanceID, runtimeRef string, actual domain.IsolationType, observed domain.ObservedState, code domain.ErrorCode, at time.Time) error {
	instance, ok := m.instances[id]
	if !ok || instance.OwnerID != owner {
		return &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	}
	instance.RuntimeRef, instance.ActualIsolation, instance.ObservedState, instance.ErrorCode, instance.UpdatedAt = runtimeRef, actual, observed, code, at
	m.instances[id] = instance
	return nil
}
func (m *checkpointRepo) CreateSnapshotRecord(_ context.Context, snapshot domain.Snapshot, operation domain.Operation) (domain.Operation, bool, error) {
	for _, op := range m.ops {
		if op.OwnerID == operation.OwnerID && op.IdempotencyKey == operation.IdempotencyKey {
			return op, true, nil
		}
	}
	m.snapshots[snapshot.ID] = snapshot
	m.ops[string(operation.ID)] = operation
	return operation, false, nil
}
func (m *checkpointRepo) GetSnapshot(_ context.Context, owner domain.OwnerID, id domain.SnapshotID) (domain.Snapshot, error) {
	snapshot, ok := m.snapshots[id]
	if !ok || snapshot.OwnerID != owner {
		return domain.Snapshot{}, &domain.Error{Code: domain.CodeNotFound, Field: "snapshot"}
	}
	return snapshot, nil
}
func (m *checkpointRepo) ListSnapshots(_ context.Context, owner domain.OwnerID, instanceID domain.InstanceID) ([]domain.Snapshot, error) {
	out := make([]domain.Snapshot, 0)
	for _, snapshot := range m.snapshots {
		if snapshot.OwnerID == owner && snapshot.InstanceID == instanceID {
			out = append(out, snapshot)
		}
	}
	return out, nil
}
func (m *checkpointRepo) DeleteSnapshotRecord(_ context.Context, owner domain.OwnerID, id domain.SnapshotID) error {
	snapshot, ok := m.snapshots[id]
	if !ok || snapshot.OwnerID != owner {
		return &domain.Error{Code: domain.CodeNotFound, Field: "snapshot"}
	}
	delete(m.snapshots, id)
	return nil
}
func (m *checkpointRepo) UpdateSnapshotRuntimeRef(_ context.Context, owner domain.OwnerID, id domain.SnapshotID, runtimeRef string, at time.Time) error {
	snapshot, ok := m.snapshots[id]
	if !ok || snapshot.OwnerID != owner {
		return &domain.Error{Code: domain.CodeNotFound, Field: "snapshot"}
	}
	snapshot.RuntimeRef = runtimeRef
	m.snapshots[id] = snapshot
	_ = at
	return nil
}
func (m *checkpointRepo) GetOperationByIdempotency(_ context.Context, owner domain.OwnerID, key string) (domain.Operation, bool, error) {
	for _, op := range m.ops {
		if op.OwnerID == owner && op.IdempotencyKey == key {
			return op, true, nil
		}
	}
	return domain.Operation{}, false, nil
}
func (m *checkpointRepo) CompleteOperation(_ context.Context, owner domain.OwnerID, id domain.OperationID, at time.Time) error {
	op, ok := m.ops[string(id)]
	if !ok || op.OwnerID != owner {
		return &domain.Error{Code: domain.CodeNotFound, Field: "operation"}
	}
	op.Status = domain.OperationSucceeded
	op.UpdatedAt = at
	m.ops[string(id)] = op
	return nil
}
func (m *checkpointRepo) UpdateOperationStage(_ context.Context, owner domain.OwnerID, id domain.OperationID, stage string, progress int, at time.Time) error {
	op, ok := m.ops[string(id)]
	if !ok || op.OwnerID != owner {
		return &domain.Error{Code: domain.CodeNotFound, Field: "operation"}
	}
	op.Stage, op.Progress, op.UpdatedAt = stage, progress, at
	m.ops[string(id)] = op
	return nil
}
func (m *checkpointRepo) CreateDeleteOperation(_ context.Context, operation domain.Operation) (domain.Operation, bool, error) {
	for _, op := range m.ops {
		if op.OwnerID == operation.OwnerID && op.IdempotencyKey == operation.IdempotencyKey {
			return op, true, nil
		}
	}
	m.ops[string(operation.ID)] = operation
	return operation, false, nil
}
func (m *checkpointRepo) ListInstanceSoftware(_ context.Context, owner domain.OwnerID, id domain.InstanceID) ([]domain.InstanceSoftware, error) {
	instance, ok := m.instances[id]
	if !ok || instance.OwnerID != owner {
		return nil, &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	}
	return append([]domain.InstanceSoftware(nil), m.software[id]...), nil
}
