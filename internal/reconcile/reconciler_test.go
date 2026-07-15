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

func TestReconcileConvergesDesiredStateWithoutRecreatingMissing(t *testing.T) {
	now := time.Date(2026, 7, 15, 3, 0, 0, 0, time.UTC)
	meta := map[string]string{instances.MetadataManaged: "true", instances.MetadataInstanceID: "box"}
	cases := []struct {
		name     string
		desired  domain.DesiredState
		actual   runtimeapi.InstanceState
		wantCall string
	}{
		{name: "start stopped instance", desired: domain.DesiredRunning, actual: runtimeapi.StateStopped, wantCall: "start"},
		{name: "stop running instance", desired: domain.DesiredStopped, actual: runtimeapi.StateRunning, wantCall: "stop"},
		{name: "delete present instance", desired: domain.DesiredDeleted, actual: runtimeapi.StateRunning, wantCall: "delete"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &repositoryFake{instances: []domain.Instance{{
				ID: "box", OwnerID: "owner", RuntimeRef: "obx-box", ActualIsolation: domain.IsolationContainer,
				DesiredState: tc.desired, ObservedState: domain.ObservedRunning,
			}}}
			mutator := &mutatorFake{}
			runtime := &runtimeFake{instances: []runtimeapi.Instance{{Ref: "obx-box", State: tc.actual, Metadata: meta}}}
			reconciler, err := New(runtime, repo, mutator, Options{Now: func() time.Time { return now }, NewID: func() string { return "cycle" }})
			if err != nil {
				t.Fatal(err)
			}
			report, err := reconciler.RunOnce(context.Background())
			if err != nil || report.Mutations != 1 || mutator.last != tc.wantCall || len(report.Diagnostics) != 0 {
				t.Fatalf("report=%+v err=%v mutator=%+v", report, err, mutator)
			}
		})
	}

	repo := &repositoryFake{instances: []domain.Instance{{
		ID: "gone", OwnerID: "owner", RuntimeRef: "obx-gone", ActualIsolation: domain.IsolationContainer,
		DesiredState: domain.DesiredRunning, ObservedState: domain.ObservedRunning,
	}}}
	mutator := &mutatorFake{}
	reconciler, err := New(&runtimeFake{}, repo, mutator, Options{Now: func() time.Time { return now }, NewID: func() string { return "cycle" }})
	if err != nil {
		t.Fatal(err)
	}
	report, err := reconciler.RunOnce(context.Background())
	if err != nil || report.Mutations != 0 || mutator.calls != 0 || repo.lastError != domain.CodeRuntimeMissing {
		t.Fatalf("missing runtime must not recreate: report=%+v err=%v mutator=%+v lastError=%s", report, err, mutator, repo.lastError)
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

type mutatorFake struct {
	calls int
	last  string
}

func (f *mutatorFake) Start(context.Context, domain.OwnerID, domain.InstanceID, string) (domain.Instance, error) {
	f.calls++
	f.last = "start"
	return domain.Instance{}, nil
}
func (f *mutatorFake) Stop(context.Context, domain.OwnerID, domain.InstanceID, string) (domain.Instance, error) {
	f.calls++
	f.last = "stop"
	return domain.Instance{}, nil
}
func (f *mutatorFake) Delete(context.Context, domain.OwnerID, domain.InstanceID, string) error {
	f.calls++
	f.last = "delete"
	return nil
}
