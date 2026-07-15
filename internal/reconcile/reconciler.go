// SPDX-License-Identifier: AGPL-3.0-only

// Package reconcile converges durable desired state with verified runtime state.
package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/operations"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

type Runtime interface {
	ListInstances(context.Context) ([]runtimeapi.Instance, error)
}

type Repository interface {
	ListInstances(context.Context) ([]domain.Instance, error)
	UpdateInstanceObservation(context.Context, domain.OwnerID, domain.InstanceID, string, domain.IsolationType, domain.ObservedState, domain.ErrorCode, time.Time) error
}

type Mutator interface {
	Start(context.Context, domain.OwnerID, domain.InstanceID, string) (domain.Instance, error)
	Stop(context.Context, domain.OwnerID, domain.InstanceID, string) (domain.Instance, error)
	Delete(context.Context, domain.OwnerID, domain.InstanceID, string) error
}

type DiagnosticKind string

const (
	UnmanagedResource   DiagnosticKind = "unmanaged_resource"
	MissingRuntime      DiagnosticKind = "runtime_missing"
	ReplacementIdentity DiagnosticKind = "replacement_identity"
)

type Diagnostic struct {
	Kind       DiagnosticKind
	RuntimeRef string
	InstanceID domain.InstanceID
	Message    string
}

type Report struct {
	Degraded    bool
	Diagnostics []Diagnostic
	Mutations   int
}

type Options struct {
	Now   func() time.Time
	NewID func() string
	Mode  *operations.Mode
}

type Reconciler struct {
	runtime Runtime
	repo    Repository
	mutator Mutator
	now     func() time.Time
	newID   func() string
	mode    *operations.Mode
}

func New(runtime Runtime, repo Repository, mutator Mutator, options Options) (*Reconciler, error) {
	if runtime == nil || repo == nil || mutator == nil {
		return nil, fmt.Errorf("runtime, repository, and mutator are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = func() string { return fmt.Sprintf("%d", options.Now().UnixNano()) }
	}
	if options.Mode == nil {
		options.Mode = &operations.Mode{}
	}
	return &Reconciler{runtime: runtime, repo: repo, mutator: mutator, now: options.Now, newID: options.NewID, mode: options.Mode}, nil
}

func (r *Reconciler) RunOnce(ctx context.Context) (Report, error) {
	durable, err := r.repo.ListInstances(ctx)
	if err != nil {
		return Report{}, err
	}
	runtimeInstances, err := r.runtime.ListInstances(ctx)
	if err != nil {
		r.mode.SetDegraded(true)
		return Report{Degraded: true}, fmt.Errorf("list runtime instances: %w", err)
	}
	r.mode.SetDegraded(false)
	byRef := make(map[string]runtimeapi.Instance, len(runtimeInstances))
	for _, item := range runtimeInstances {
		byRef[item.Ref] = item
	}
	report := Report{}
	for _, instance := range durable {
		actual, exists := byRef[instance.RuntimeRef]
		if !exists {
			if instance.DesiredState == domain.DesiredDeleted {
				if err := r.mutator.Delete(ctx, instance.OwnerID, instance.ID, r.key(instance, "delete")); err != nil {
					return report, err
				}
				report.Mutations++
				continue
			}
			if err := r.repo.UpdateInstanceObservation(ctx, instance.OwnerID, instance.ID, instance.RuntimeRef, instance.ActualIsolation, domain.ObservedError, domain.CodeRuntimeMissing, r.now()); err != nil {
				return report, err
			}
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Kind: MissingRuntime, RuntimeRef: instance.RuntimeRef, InstanceID: instance.ID, Message: "persistent runtime data is absent; explicit restore or forget is required"})
			continue
		}
		delete(byRef, instance.RuntimeRef)
		if err := instances.VerifyRuntimeIdentity(actual, instance.ID, instance.ActualIsolation); err != nil {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Kind: ReplacementIdentity, RuntimeRef: instance.RuntimeRef, InstanceID: instance.ID, Message: err.Error()})
			continue
		}
		switch instance.DesiredState {
		case domain.DesiredRunning:
			if actual.State != runtimeapi.StateRunning {
				if _, err := r.mutator.Start(ctx, instance.OwnerID, instance.ID, r.key(instance, "start")); err != nil {
					return report, err
				}
				report.Mutations++
			}
		case domain.DesiredStopped:
			if actual.State == runtimeapi.StateRunning {
				if _, err := r.mutator.Stop(ctx, instance.OwnerID, instance.ID, r.key(instance, "stop")); err != nil {
					return report, err
				}
				report.Mutations++
			}
		case domain.DesiredDeleted:
			if err := r.mutator.Delete(ctx, instance.OwnerID, instance.ID, r.key(instance, "delete")); err != nil {
				return report, err
			}
			report.Mutations++
		}
	}
	for _, actual := range byRef {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{Kind: UnmanagedResource, RuntimeRef: actual.Ref, Message: "runtime resource has no durable OpenBox instance record and was left untouched"})
	}
	return report, nil
}

func (r *Reconciler) key(instance domain.Instance, action string) string {
	return "reconcile:" + action + ":" + string(instance.ID) + ":" + r.newID()
}
