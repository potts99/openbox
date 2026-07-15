// SPDX-License-Identifier: AGPL-3.0-only

package recovery

import (
	"context"
	"errors"
	"testing"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/operations"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestAdoptRequiresMatchingRuntimeIdentity(t *testing.T) {
	instance := domain.Instance{ID: "instance-1", OwnerID: "owner-1", RuntimeRef: "obx-instance-1", ActualIsolation: domain.IsolationContainer}
	repo := managerRepo{instance: instance}
	lifecycle := &managerLifecycle{instance: instance}
	runtime := &managerRuntime{instance: runtimeapi.Instance{Ref: instance.RuntimeRef, State: runtimeapi.StateRunning, Metadata: map[string]string{instances.MetadataManaged: "true", instances.MetadataInstanceID: "replacement"}}}
	manager, _ := NewManager(repo, runtime, lifecycle, nil, func() string { return "id" })
	if _, err := manager.Adopt(context.Background(), instance.OwnerID, instance.ID); err == nil {
		t.Fatal("replacement identity was adopted")
	}
	if lifecycle.refreshes != 0 {
		t.Fatal("replacement identity reached refresh")
	}
	runtime.instance.Metadata[instances.MetadataInstanceID] = string(instance.ID)
	if _, err := manager.Adopt(context.Background(), instance.OwnerID, instance.ID); err != nil {
		t.Fatal(err)
	}
	if lifecycle.refreshes != 1 {
		t.Fatalf("refreshes=%d", lifecycle.refreshes)
	}
}

func TestRestoreIsExplicitAndDegradedModeIsReadOnly(t *testing.T) {
	instance := domain.Instance{ID: "instance-1", OwnerID: "owner-1", RuntimeRef: "obx-instance-1", ActualIsolation: domain.IsolationContainer, ErrorCode: domain.CodeRuntimeMissing}
	mode := &operations.Mode{}
	runtime := &managerRuntime{err: runtimeapi.ErrNotFound}
	lifecycle := &managerLifecycle{instance: instance}
	manager, _ := NewManager(managerRepo{instance: instance}, runtime, lifecycle, mode, func() string { return "id" })
	restorer := &managerRestorer{instance: runtimeapi.Instance{Ref: instance.RuntimeRef, State: runtimeapi.StateRunning, Metadata: map[string]string{instances.MetadataManaged: "true", instances.MetadataInstanceID: string(instance.ID)}}}
	mode.SetDegraded(true)
	if _, err := manager.Restore(context.Background(), instance.OwnerID, instance.ID, restorer); !errors.Is(err, ErrDegradedReadOnly) || restorer.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, restorer.calls)
	}
	mode.SetDegraded(false)
	if _, err := manager.Restore(context.Background(), instance.OwnerID, instance.ID, restorer); err != nil {
		t.Fatal(err)
	}
	if restorer.calls != 1 || lifecycle.refreshes != 1 {
		t.Fatalf("restore calls=%d refreshes=%d", restorer.calls, lifecycle.refreshes)
	}
}

type managerRepo struct{ instance domain.Instance }

func (r managerRepo) GetInstance(context.Context, domain.OwnerID, domain.InstanceID) (domain.Instance, error) {
	return r.instance, nil
}

type managerRuntime struct {
	instance runtimeapi.Instance
	err      error
}

func (r *managerRuntime) InspectInstance(context.Context, string) (runtimeapi.Instance, error) {
	return r.instance, r.err
}

type managerLifecycle struct {
	instance  domain.Instance
	refreshes int
	deletes   int
}

func (l *managerLifecycle) Refresh(context.Context, domain.OwnerID, domain.InstanceID) (domain.Instance, error) {
	l.refreshes++
	return l.instance, nil
}
func (l *managerLifecycle) Delete(context.Context, domain.OwnerID, domain.InstanceID, string) error {
	l.deletes++
	return nil
}

type managerRestorer struct {
	instance runtimeapi.Instance
	calls    int
}

func (r *managerRestorer) RestoreInstance(context.Context, domain.Instance) (runtimeapi.Instance, error) {
	r.calls++
	return r.instance, nil
}
