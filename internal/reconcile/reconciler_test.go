// SPDX-License-Identifier: AGPL-3.0-only

package reconcile

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/operations"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestReconcileSafetyDiagnosticsAndDegradedMode(t *testing.T) {
	now := time.Date(2026, 7, 15, 3, 0, 0, 0, time.UTC)
	durable := domain.Instance{ID: "expected", OwnerID: "owner", RuntimeRef: "obx-expected", ActualIsolation: domain.IsolationContainer, DesiredState: domain.DesiredRunning, ObservedState: domain.ObservedStopped}
	repo := &repositoryFake{instances: []domain.Instance{durable}}
	mutator := &mutatorFake{}
	mode := &operations.Mode{}
	runtime := &runtimeFake{err: errors.New("Incus unavailable")}
	reconciler, err := New(runtime, repo, mutator, Options{Now: func() time.Time { return now }, NewID: func() string { return "cycle" }, Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	report, err := reconciler.RunOnce(context.Background())
	if err == nil || !report.Degraded || !mode.Degraded() || mutator.calls != 0 {
		t.Fatalf("report=%+v err=%v degraded=%v calls=%d", report, err, mode.Degraded(), mutator.calls)
	}

	runtime.err = nil
	runtime.instances = []runtimeapi.Instance{
		{Ref: "obx-expected", State: runtimeapi.StateStopped, Metadata: map[string]string{instances.MetadataManaged: "true", instances.MetadataInstanceID: "replacement"}},
		{Ref: "foreign", State: runtimeapi.StateRunning, Metadata: map[string]string{}},
	}
	report, err = reconciler.RunOnce(context.Background())
	if err != nil || report.Degraded || mode.Degraded() || mutator.calls != 0 {
		t.Fatalf("report=%+v err=%v degraded=%v calls=%d", report, err, mode.Degraded(), mutator.calls)
	}
	if len(report.Diagnostics) != 2 || report.Diagnostics[0].Kind != ReplacementIdentity || report.Diagnostics[1].Kind != UnmanagedResource {
		t.Fatalf("diagnostics=%+v", report.Diagnostics)
	}

	runtime.instances = nil
	report, err = reconciler.RunOnce(context.Background())
	if err != nil || len(report.Diagnostics) != 1 || report.Diagnostics[0].Kind != MissingRuntime || repo.lastError != domain.CodeRuntimeMissing || mutator.calls != 0 {
		t.Fatalf("report=%+v err=%v lastError=%s calls=%d", report, err, repo.lastError, mutator.calls)
	}
}

type runtimeFake struct {
	instances []runtimeapi.Instance
	err       error
}

func (f *runtimeFake) ListInstances(context.Context) ([]runtimeapi.Instance, error) {
	return f.instances, f.err
}

type repositoryFake struct {
	instances []domain.Instance
	lastError domain.ErrorCode
}

func (f *repositoryFake) ListInstances(context.Context) ([]domain.Instance, error) {
	return f.instances, nil
}
func (f *repositoryFake) UpdateInstanceObservation(_ context.Context, _ domain.OwnerID, _ domain.InstanceID, _ string, _ domain.IsolationType, _ domain.ObservedState, code domain.ErrorCode, _ time.Time) error {
	f.lastError = code
	return nil
}

type mutatorFake struct{ calls int }

func (f *mutatorFake) Start(context.Context, domain.OwnerID, domain.InstanceID, string) (domain.Instance, error) {
	f.calls++
	return domain.Instance{}, nil
}
func (f *mutatorFake) Stop(context.Context, domain.OwnerID, domain.InstanceID, string) (domain.Instance, error) {
	f.calls++
	return domain.Instance{}, nil
}
func (f *mutatorFake) Delete(context.Context, domain.OwnerID, domain.InstanceID, string) error {
	f.calls++
	return nil
}
