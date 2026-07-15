// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/app/recovery"
	openboxclient "github.com/openbox-dev/openbox/internal/client"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/httpapi"
	"github.com/openbox-dev/openbox/internal/operations"
	"github.com/openbox-dev/openbox/internal/persistence/sqlite"
	"github.com/openbox-dev/openbox/internal/reconcile"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
)

func TestDaemonRunsStartupRecoveryReconciliationAndCloses(t *testing.T) {
	operations := &countingOperations{called: make(chan struct{}, 4)}
	reconciler := &countingReconciler{called: make(chan struct{}, 4)}
	closer := &countingCloser{}
	factory := factoryFunc(func(context.Context, daemonConfig) (daemonComponents, error) {
		return daemonComponents{operations: operations, reconciler: reconciler, closer: closer}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runDaemon(ctx, testDaemonConfig(), factory) }()
	waitCall(t, operations.called)
	waitCall(t, reconciler.called)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if closer.calls.Load() != 1 {
		t.Fatalf("close calls=%d", closer.calls.Load())
	}
}

func TestDaemonAndClientDefaultsAddressSamePrivateAPI(t *testing.T) {
	base, err := url.Parse(openboxclient.DefaultBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if base.Scheme != "http" || base.Host != httpapi.DefaultAddress {
		t.Fatalf("client default %q does not match daemon %q", openboxclient.DefaultBaseURL, httpapi.DefaultAddress)
	}
}

func TestDaemonRejectsPublicPreAuthenticationAPIAndPartialTLS(t *testing.T) {
	public := testDaemonConfig()
	public.APIAddress = "0.0.0.0:8443"
	if err := public.validate(); err == nil {
		t.Fatal("public API address accepted before authentication")
	}
	partialTLS := testDaemonConfig()
	partialTLS.APITLSCertificate = "/cert.pem"
	if err := partialTLS.validate(); err == nil {
		t.Fatal("partial TLS configuration accepted")
	}
}

func TestDaemonRunsAndGracefullyStopsAPI(t *testing.T) {
	api := &countingAPI{started: make(chan struct{}), stopped: make(chan struct{})}
	factory := factoryFunc(func(context.Context, daemonConfig) (daemonComponents, error) {
		return daemonComponents{operations: &countingOperations{called: make(chan struct{}, 4)}, reconciler: &countingReconciler{called: make(chan struct{}, 4)}, closer: &countingCloser{}, api: api}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runDaemon(ctx, testDaemonConfig(), factory) }()
	waitCall(t, api.started)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitCall(t, api.stopped)
}

func TestDaemonRestartRecoversWithoutDuplicateCreateOrStatusLoss(t *testing.T) {
	ctx := context.Background()
	database := t.TempDir() + "/openbox.db"
	runtime := fake.New(runtimeapi.Capabilities{Architecture: "x86_64", Containers: true})
	runtime.AddImage(runtimeapi.Image{Fingerprint: "sha256:ubuntu", Aliases: []string{"ubuntu"}, Architecture: "x86_64", Type: "container", CloudInit: true})
	store, err := sqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 15, 4, 0, 0, 0, time.UTC)
	if err := store.CreateOwner(ctx, domain.Owner{ID: "owner-1", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	ids := []string{"instance-1", "operation-1"}
	service, _ := instances.New(runtime, store, instances.Options{Now: func() time.Time { return now }, NewID: func() string { id := ids[0]; ids = ids[1:]; return id }})
	runtime.FailNext("instance.start", errors.New("simulated daemon crash"))
	_, err = service.Create(ctx, instances.CreateInput{OwnerID: "owner-1", Name: "box", Kind: domain.KindVPS, Image: "ubuntu", RequestedIsolation: domain.IsolationStandard, OwnerPublicKey: "ssh-ed25519 owner", IdempotencyKey: "create-key"})
	if err == nil {
		t.Fatal("seed create unexpectedly completed")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	config := testDaemonConfig()
	config.DatabasePath = database
	for restart := 0; restart < 2; restart++ {
		startupDone := make(chan error, 1)
		factory := recoveryFactory{runtime: runtime, startupDone: startupDone}
		runCtx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- runDaemon(runCtx, config, factory) }()
		if err := waitResult(t, startupDone); err != nil {
			t.Fatalf("startup recovery: %v", err)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if len(runtime.CreateRequests()) != 1 {
		t.Fatalf("runtime creates=%d", len(runtime.CreateRequests()))
	}
	store, err = sqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	op, found, err := store.GetOperationByIdempotency(ctx, "owner-1", "create-key")
	if err != nil || !found || op.Status != domain.OperationSucceeded || op.Stage != "complete" {
		t.Fatalf("operation=%+v found=%v err=%v", op, found, err)
	}
}

type factoryFunc func(context.Context, daemonConfig) (daemonComponents, error)

func (f factoryFunc) Build(ctx context.Context, config daemonConfig) (daemonComponents, error) {
	return f(ctx, config)
}

type countingOperations struct {
	calls  atomic.Int32
	called chan struct{}
}

func (c *countingOperations) RunOnce(context.Context) error {
	c.calls.Add(1)
	c.called <- struct{}{}
	return nil
}

type countingReconciler struct {
	calls  atomic.Int32
	called chan struct{}
}

func (c *countingReconciler) RunOnce(context.Context) (reconcile.Report, error) {
	c.calls.Add(1)
	c.called <- struct{}{}
	return reconcile.Report{}, nil
}

type countingCloser struct{ calls atomic.Int32 }

func (c *countingCloser) Close() error { c.calls.Add(1); return nil }

type countingAPI struct {
	started chan struct{}
	stopped chan struct{}
	stop    chan struct{}
}

func (a *countingAPI) Run() error {
	if a.stop == nil {
		a.stop = make(chan struct{})
	}
	close(a.started)
	<-a.stop
	return nil
}

func (a *countingAPI) Shutdown(context.Context) error {
	close(a.stop)
	close(a.stopped)
	return nil
}

type recoveryFactory struct {
	runtime     *fake.Runtime
	startupDone chan error
}

func (f recoveryFactory) Build(ctx context.Context, config daemonConfig) (daemonComponents, error) {
	store, err := sqlite.Open(ctx, config.DatabasePath)
	if err != nil {
		return daemonComponents{}, err
	}
	service, err := instances.New(f.runtime, store, instances.Options{})
	if err != nil {
		store.Close()
		return daemonComponents{}, err
	}
	worker, err := operations.NewWorker(store, recovery.Executor{Instances: service}, operations.Config{WorkerID: "openboxd-local", Concurrency: 1, Lease: time.Minute})
	if err != nil {
		store.Close()
		return daemonComponents{}, err
	}
	return daemonComponents{operations: signalOperations{operationRunner: worker, done: f.startupDone}, reconciler: noopReconciler{}, closer: store}, nil
}

type signalOperations struct {
	operationRunner
	done chan error
}

func (s signalOperations) RunOnce(ctx context.Context) error {
	err := s.operationRunner.RunOnce(ctx)
	s.done <- err
	return err
}

func waitResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for daemon result")
		return nil
	}
}

type noopReconciler struct{}

func (noopReconciler) RunOnce(context.Context) (reconcile.Report, error) {
	return reconcile.Report{}, nil
}

func testDaemonConfig() daemonConfig {
	return daemonConfig{DatabasePath: "/tmp/openbox-test.db", IncusSocket: "/tmp/incus.sock", APIAddress: "127.0.0.1:8443", OwnerID: "owner-local", OwnerName: "Local owner", WorkerConcurrency: 1, OperationInterval: time.Hour, ReconcileInterval: time.Hour, Lease: time.Minute}
}

func waitCall(t *testing.T, called <-chan struct{}) {
	t.Helper()
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for daemon cycle")
	}
}
