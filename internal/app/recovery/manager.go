// SPDX-License-Identifier: AGPL-3.0-only

package recovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/operations"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

var ErrDegradedReadOnly = errors.New("runtime is unavailable; OpenBox is in degraded read-only mode")

type InstanceRepository interface {
	GetInstance(context.Context, domain.OwnerID, domain.InstanceID) (domain.Instance, error)
}

type RuntimeInspector interface {
	InspectInstance(context.Context, string) (runtimeapi.Instance, error)
}

type Lifecycle interface {
	Refresh(context.Context, domain.OwnerID, domain.InstanceID) (domain.Instance, error)
	Delete(context.Context, domain.OwnerID, domain.InstanceID, string) error
}

// Restorer is deliberately operator-supplied: choosing a snapshot or backup is
// an explicit recovery decision and is never inferred by reconciliation.
type Restorer interface {
	RestoreInstance(context.Context, domain.Instance) (runtimeapi.Instance, error)
}

type Manager struct {
	repo      InstanceRepository
	runtime   RuntimeInspector
	lifecycle Lifecycle
	mode      *operations.Mode
	newID     func() string
}

func NewManager(repo InstanceRepository, runtime RuntimeInspector, lifecycle Lifecycle, mode *operations.Mode, newID func() string) (*Manager, error) {
	if repo == nil || runtime == nil || lifecycle == nil {
		return nil, errors.New("repository, runtime, and lifecycle are required")
	}
	if mode == nil {
		mode = &operations.Mode{}
	}
	if newID == nil {
		newID = func() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }
	}
	return &Manager{repo: repo, runtime: runtime, lifecycle: lifecycle, mode: mode, newID: newID}, nil
}

// Forget explicitly removes durable metadata after the normal identity-safe
// deletion path confirms that the runtime is absent.
func (m *Manager) Forget(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID) error {
	if m.mode.Degraded() {
		return ErrDegradedReadOnly
	}
	return m.lifecycle.Delete(ctx, ownerID, id, "recovery:forget:"+m.newID())
}

// Adopt reconnects an existing, correctly labelled runtime resource to its
// restored durable record. Unknown or replacement identities are refused.
func (m *Manager) Adopt(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID) (domain.Instance, error) {
	if m.mode.Degraded() {
		return domain.Instance{}, ErrDegradedReadOnly
	}
	instance, err := m.repo.GetInstance(ctx, ownerID, id)
	if err != nil {
		return domain.Instance{}, err
	}
	actual, err := m.runtime.InspectInstance(ctx, instance.RuntimeRef)
	if err != nil {
		return domain.Instance{}, err
	}
	if err := instances.VerifyRuntimeIdentity(actual, instance.ID, instance.ActualIsolation); err != nil {
		return domain.Instance{}, err
	}
	return m.lifecycle.Refresh(ctx, ownerID, id)
}

// Restore invokes an explicitly selected restore implementation only when the
// durable record is already marked runtime_missing and the old runtime is gone.
func (m *Manager) Restore(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, restorer Restorer) (domain.Instance, error) {
	if m.mode.Degraded() {
		return domain.Instance{}, ErrDegradedReadOnly
	}
	if restorer == nil {
		return domain.Instance{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "restorer"}
	}
	instance, err := m.repo.GetInstance(ctx, ownerID, id)
	if err != nil {
		return domain.Instance{}, err
	}
	if instance.ErrorCode != domain.CodeRuntimeMissing {
		return domain.Instance{}, &domain.Error{Code: domain.CodeConflict, Field: "instance.error_code"}
	}
	if _, err := m.runtime.InspectInstance(ctx, instance.RuntimeRef); !errors.Is(err, runtimeapi.ErrNotFound) {
		if err == nil {
			return domain.Instance{}, &domain.Error{Code: domain.CodeConflict, Field: "runtime_ref", Cause: errors.New("runtime resource already exists")}
		}
		return domain.Instance{}, err
	}
	restored, err := restorer.RestoreInstance(ctx, instance)
	if err != nil {
		return domain.Instance{}, err
	}
	if err := instances.VerifyRuntimeIdentity(restored, instance.ID, instance.ActualIsolation); err != nil {
		return domain.Instance{}, err
	}
	return m.lifecycle.Refresh(ctx, ownerID, id)
}
