// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
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
	"github.com/openbox-dev/openbox/internal/sshgateway"
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

func TestDaemonPublicListenerRequiresCompleteTLS(t *testing.T) {
	publicPlaintext := testDaemonConfig()
	publicPlaintext.APIAddress = "0.0.0.0:8443"
	if err := publicPlaintext.validate(); err == nil {
		t.Fatal("public plaintext API address accepted")
	}
	publicTLS := publicPlaintext
	publicTLS.APITLSCertificate = "/cert.pem"
	publicTLS.APITLSKey = "/key.pem"
	if err := publicTLS.validate(); err != nil {
		t.Fatalf("public API with complete TLS rejected: %v", err)
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

func TestDaemonWaitsForSSHBeforeClosingPersistence(t *testing.T) {
	sshDone := make(chan struct{})
	closer := &orderedCloser{sshDone: sshDone}
	factory := factoryFunc(func(context.Context, daemonConfig) (daemonComponents, error) {
		return daemonComponents{operations: &countingOperations{called: make(chan struct{}, 4)}, reconciler: &countingReconciler{called: make(chan struct{}, 4)}, closer: closer, ssh: waitingSSH{done: sshDone}}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runDaemon(ctx, testDaemonConfig(), factory) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if closer.closedBeforeSSH.Load() {
		t.Fatal("persistence closed before SSH transport stopped")
	}
}

func TestDaemonTreatsUnexpectedSSHStopAsFatal(t *testing.T) {
	factory := factoryFunc(func(context.Context, daemonConfig) (daemonComponents, error) {
		return daemonComponents{operations: &countingOperations{called: make(chan struct{}, 4)}, reconciler: &countingReconciler{called: make(chan struct{}, 4)}, closer: &countingCloser{}, ssh: immediateSSH{}}, nil
	})
	err := runDaemon(context.Background(), testDaemonConfig(), factory)
	if err == nil || !strings.Contains(err.Error(), "stopped unexpectedly") {
		t.Fatalf("unexpected SSH stop error=%v", err)
	}
}

func TestDurableSSHAuditorStoresOnlySafeStructuredMetadata(t *testing.T) {
	writer := &auditCapture{}
	auditor := durableSSHAuditor{store: writer, fallbackOwner: "owner-local"}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	if err := auditor.Record(context.Background(), sshgateway.AuditEvent{At: now, RemoteIP: "192.0.2.3", Fingerprint: "SHA256:key", Command: "new", Target: "dev", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	var metadata map[string]string
	if err := json.Unmarshal(writer.event.MetadataJSON, &metadata); err != nil {
		t.Fatal(err)
	}
	if writer.event.OwnerID != "owner-local" || writer.event.Actor != "SHA256:key" || writer.event.TargetID != "dev" || writer.event.Outcome != "success" || metadata["command"] != "new" || metadata["remote_ip"] != "192.0.2.3" {
		t.Fatalf("audit event=%+v metadata=%v", writer.event, metadata)
	}
}

func TestDurableTerminalAuditorStoresOnlySafeStructuredMetadata(t *testing.T) {
	writer := &auditCapture{}
	auditor := durableTerminalAuditor{store: writer, fallbackOwner: "owner-local"}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	marker := "UNIQUE_PTY_MARKER_must_not_appear_in_metadata"
	if err := auditor.Record(context.Background(), httpapi.TerminalAuditEvent{
		At: now, OwnerID: "owner-1", InstanceID: "inst-1",
		SessionID: "sess-1", SessionName: "pi",
		Phase: httpapi.TerminalAuditPhaseEnd, Reason: httpapi.TerminalAuditReasonDetach,
	}); err != nil {
		t.Fatal(err)
	}
	var metadata map[string]string
	if err := json.Unmarshal(writer.event.MetadataJSON, &metadata); err != nil {
		t.Fatal(err)
	}
	if writer.event.Action != "terminal.session" || writer.event.TargetType != "instance" || writer.event.TargetID != "inst-1" {
		t.Fatalf("event=%+v", writer.event)
	}
	if writer.event.Outcome != httpapi.TerminalAuditReasonDetach {
		t.Fatalf("outcome=%q", writer.event.Outcome)
	}
	if metadata["phase"] != httpapi.TerminalAuditPhaseEnd || metadata["session_id"] != "sess-1" || metadata["session_name"] != "pi" || metadata["reason"] != httpapi.TerminalAuditReasonDetach {
		t.Fatalf("metadata=%v", metadata)
	}
	raw, _ := json.Marshal(writer.event)
	if strings.Contains(string(raw), marker) || strings.Contains(string(writer.event.MetadataJSON), "input") || strings.Contains(string(writer.event.MetadataJSON), "output") {
		t.Fatalf("unsafe terminal audit payload: %s", raw)
	}
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

type waitingSSH struct{ done chan struct{} }

func (s waitingSSH) ListenAndServe(ctx context.Context) error {
	<-ctx.Done()
	close(s.done)
	return nil
}

type immediateSSH struct{}

func (immediateSSH) ListenAndServe(context.Context) error { return nil }

type orderedCloser struct {
	sshDone         chan struct{}
	closedBeforeSSH atomic.Bool
}

type auditCapture struct{ event domain.AuditEvent }

func (a *auditCapture) CreateAuditEvent(_ context.Context, event domain.AuditEvent) error {
	a.event = event
	return nil
}

func (c *orderedCloser) Close() error {
	select {
	case <-c.sshDone:
	default:
		c.closedBeforeSSH.Store(true)
	}
	return nil
}

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
	return daemonConfig{DatabasePath: "/tmp/openbox-test.db", IncusSocket: "/tmp/incus.sock", APIAddress: "127.0.0.1:8443", SSHAddress: ":2222", SSHHostKeyPath: "/tmp/openbox-test-host", SSHInstanceKeyPath: "/tmp/openbox-test-instance", SSHKnownHostsPath: "/tmp/openbox-test-known", OwnerID: "owner-local", OwnerName: "Local owner", WorkerConcurrency: 1, OperationInterval: time.Hour, ReconcileInterval: time.Hour, Lease: time.Minute}
}

func waitCall(t *testing.T, called <-chan struct{}) {
	t.Helper()
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for daemon cycle")
	}
}
