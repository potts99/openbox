// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openbox-dev/openbox/internal/app/clones"
	"github.com/openbox-dev/openbox/internal/app/egress"
	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/app/metrics"
	"github.com/openbox-dev/openbox/internal/app/recovery"
	"github.com/openbox-dev/openbox/internal/app/sshcommands"
	"github.com/openbox-dev/openbox/internal/artifacts"
	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/daemonlock"
	"github.com/openbox-dev/openbox/internal/dnsproxy"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/httpapi"
	imagebuild "github.com/openbox-dev/openbox/internal/images/build"
	"github.com/openbox-dev/openbox/internal/operations"
	"github.com/openbox-dev/openbox/internal/persistence/sqlite"
	piprofile "github.com/openbox-dev/openbox/internal/profiles/pi"
	"github.com/openbox-dev/openbox/internal/reconcile"
	"github.com/openbox-dev/openbox/internal/routes"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/incus"
	"github.com/openbox-dev/openbox/internal/sandbox"
	sandboxpool "github.com/openbox-dev/openbox/internal/sandbox/pool"
	"github.com/openbox-dev/openbox/internal/snapshots"
	"github.com/openbox-dev/openbox/internal/sshgateway"
	sshproxy "github.com/openbox-dev/openbox/internal/sshgateway/proxy"
	"github.com/openbox-dev/openbox/internal/webhooks"
	"golang.org/x/crypto/ssh"
)

type daemonConfig struct {
	DatabasePath, IncusSocket, Project, ContainerProfile, VMProfile, StoragePool     string
	APIAddress, APITLSCertificate, APITLSKey                                         string
	SSHAddress, SSHPublicHost, SSHHostKeyPath, SSHInstanceKeyPath, SSHKnownHostsPath string
	SSHPublicPort                                                                    int
	OwnerID, OwnerName                                                               string
	TrustedProxyCIDRs                                                                []string
	WorkerConcurrency                                                                int
	OperationInterval, ReconcileInterval, MetricsInterval, Lease                     time.Duration
}

func (c daemonConfig) validate() error {
	if c.DatabasePath == "" || c.IncusSocket == "" {
		return errors.New("database and Incus socket paths are required")
	}
	if c.WorkerConcurrency <= 0 || c.OperationInterval <= 0 || c.ReconcileInterval <= 0 || c.MetricsInterval <= 0 || c.Lease <= 0 {
		return errors.New("worker concurrency and daemon intervals must be positive")
	}
	if c.APIAddress == "" || c.OwnerID == "" || c.OwnerName == "" {
		return errors.New("API address and local owner identity are required")
	}
	if c.SSHAddress == "" || c.SSHHostKeyPath == "" || c.SSHInstanceKeyPath == "" || c.SSHKnownHostsPath == "" {
		return errors.New("SSH address, gateway host key, internal instance key, and known-hosts paths are required")
	}
	if _, _, err := net.SplitHostPort(c.SSHAddress); err != nil {
		return fmt.Errorf("invalid SSH address: %w", err)
	}
	host, _, err := net.SplitHostPort(c.APIAddress)
	if err != nil {
		return fmt.Errorf("invalid API address: %w", err)
	}
	if host != "localhost" {
		ip := net.ParseIP(host)
		if (ip == nil || !ip.IsLoopback()) && c.APITLSCertificate == "" {
			return errors.New("a non-loopback API listener requires TLS")
		}
	}
	if (c.APITLSCertificate == "") != (c.APITLSKey == "") {
		return errors.New("API TLS certificate and key must be configured together")
	}
	return nil
}

type operationRunner interface{ RunOnce(context.Context) error }
type reconciliationRunner interface {
	RunOnce(context.Context) (reconcile.Report, error)
}
type daemonCloser interface{ Close() error }
type apiRunner interface {
	Run() error
	Shutdown(context.Context) error
}
type sshRunner interface{ ListenAndServe(context.Context) error }

type metricsRunner interface{ RunOnce(context.Context) error }

type daemonComponents struct {
	operations  operationRunner
	webhooks    operationRunner
	reconciler  reconciliationRunner
	metrics     metricsRunner
	sandboxPool *sandboxpool.Manager
	closer      daemonCloser
	api         apiRunner
	ssh         sshRunner
}

type componentFactory interface {
	Build(context.Context, daemonConfig) (daemonComponents, error)
}

type realComponentFactory struct{}

func (realComponentFactory) Build(ctx context.Context, config daemonConfig) (daemonComponents, error) {
	if err := os.MkdirAll(filepath.Dir(config.DatabasePath), 0o700); err != nil {
		return daemonComponents{}, fmt.Errorf("create database directory: %w", err)
	}
	hostLock, err := daemonlock.TryAcquire(daemonlock.PathForDatabase(config.DatabasePath))
	if err != nil {
		if errors.Is(err, daemonlock.ErrHeld) {
			return daemonComponents{}, fmt.Errorf("%w; stop the other openboxd process before starting", err)
		}
		return daemonComponents{}, err
	}
	store, err := sqlite.Open(ctx, config.DatabasePath)
	if err != nil {
		_ = hostLock.Close()
		return daemonComponents{}, err
	}
	fail := func(err error) (daemonComponents, error) {
		_ = store.Close()
		_ = hostLock.Close()
		return daemonComponents{}, err
	}
	now := time.Now().UTC()
	if err := store.EnsureOwner(ctx, domain.Owner{ID: domain.OwnerID(config.OwnerID), Name: config.OwnerName, CreatedAt: now, UpdatedAt: now}); err != nil {
		return fail(err)
	}
	authManager, err := auth.New(store)
	if err != nil {
		return fail(err)
	}
	runtime, err := incus.New(incus.Options{SocketPath: config.IncusSocket, Project: config.Project, ContainerProfile: config.ContainerProfile, VMProfile: config.VMProfile, StoragePool: config.StoragePool})
	if err != nil {
		return fail(err)
	}
	poolConfig := sandboxpool.DefaultConfig()
	if config.StoragePool == "" {
		poolConfig.Enabled = false
	}
	sandboxPool, err := sandboxpool.New(runtime, sandboxpool.Options{Config: poolConfig})
	if err != nil {
		return fail(fmt.Errorf("create sandbox pool: %w", err))
	}
	if poolConfig.Enabled {
		if err := sandboxPool.Bootstrap(ctx); err != nil {
			log.Printf("openboxd: sandbox pool bootstrap: %v", err)
		}
		if err := sandboxPool.Reconcile(ctx); err != nil {
			log.Printf("openboxd: sandbox pool reconcile: %v", err)
		}
		if stats, statsErr := sandboxPool.Stats(ctx); statsErr == nil {
			log.Printf("openboxd: sandbox pool substrate=%s golden=%v stopped=%d running=%d zfs=%v", stats.Substrate, stats.GoldenReady, stats.Stopped, stats.Running, stats.CoWStorage)
		}
	}
	if config.StoragePool == "" {
		log.Printf("openboxd: skipping Incus managed bootstrap because --storage-pool is unset")
	} else if err := runtime.Bootstrap(ctx, incus.BootstrapConfig{
		Project: config.Project, StoragePool: config.StoragePool,
		ContainerProfile: config.ContainerProfile, VMProfile: config.VMProfile,
	}); err != nil {
		return fail(fmt.Errorf("bootstrap Incus resources: %w", err))
	}
	instanceSigner, err := sshgateway.LoadOrCreateHostKey(config.SSHInstanceKeyPath)
	if err != nil {
		return fail(fmt.Errorf("load internal instance SSH key: %w", err))
	}
	instancePublicKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(instanceSigner.PublicKey())))
	mode := &operations.Mode{}
	if err := store.EnsureSystemEgressProfiles(ctx); err != nil {
		return fail(fmt.Errorf("ensure system egress profiles: %w", err))
	}
	allowlistResolver, err := dnsproxy.NewAllowlistResolver(dnsproxy.Config{})
	if err != nil {
		return fail(fmt.Errorf("dns allowlist resolver: %w", err))
	}
	egressApplicator := egress.NewApplicator(allowlistResolver, egress.AdapterRuntime{Adapter: runtime})
	egressService, err := egress.New(store, egressApplicator, egress.Options{})
	if err != nil {
		return fail(fmt.Errorf("egress profiles: %w", err))
	}
	networkPolicy := &egress.PolicyBridge{
		Profiles: store, Applicator: egressApplicator, Backend: runtime,
	}
	artifactService, err := artifacts.New(store, filepath.Join(filepath.Dir(config.DatabasePath), "artifacts"))
	if err != nil {
		return fail(fmt.Errorf("artifact store: %w", err))
	}
	service, err := instances.New(runtime, store, instances.Options{
		Mode: mode, InstanceGatewayPublicKey: instancePublicKey, NetworkPolicy: networkPolicy, SandboxPool: sandboxPool,
		DeleteArtifacts: artifactService.DeleteAll,
	})
	if err != nil {
		return fail(err)
	}
	policyAuditor := &egress.DurablePolicyAuditor{Store: store}
	egressService.SetInstanceMarker(service)
	egressService.SetAuditor(policyAuditor)
	routeService, err := routes.New(store, routes.Options{})
	if err != nil {
		return fail(err)
	}
	snapshotService, err := snapshots.New(runtime, store, snapshots.Options{
		GatewayPublicKey: instancePublicKey,
		NetworkPolicy:    networkPolicy,
	})
	if err != nil {
		return fail(err)
	}
	cloneService, err := clones.New(runtime, store, clones.Options{
		GatewayPublicKey: instancePublicKey,
		NetworkPolicy:    networkPolicy,
	})
	if err != nil {
		return fail(err)
	}
	imageBuildService, err := imagebuild.New(runtime, store, imagebuild.Options{})
	if err != nil {
		return fail(err)
	}
	webhookService, err := webhooks.New(store)
	if err != nil {
		return fail(fmt.Errorf("webhook service: %w", err))
	}
	webhookWorker, err := webhooks.NewWorker(store, nil)
	if err != nil {
		return fail(fmt.Errorf("webhook worker: %w", err))
	}
	piProfiles, err := piprofile.New(store, piprofile.Options{})
	if err != nil {
		return fail(err)
	}
	guestExec := func(ctx context.Context, runtimeRef string, command []string, stdin []byte) error {
		var stdinReader io.Reader
		if len(stdin) > 0 {
			stdinReader = bytes.NewReader(stdin)
		}
		result, err := runtime.Exec(ctx, runtimeapi.ExecRequest{Ref: runtimeRef, Command: command, Stdin: stdinReader})
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			msg := strings.TrimSpace(string(result.Stderr))
			if msg == "" {
				msg = strings.TrimSpace(string(result.Stdout))
			}
			if msg == "" {
				msg = fmt.Sprintf("exit status %d", result.ExitCode)
			}
			return fmt.Errorf("guest command failed: %s", msg)
		}
		return nil
	}
	guestWrite := func(ctx context.Context, runtimeRef, path string, content []byte, mode os.FileMode) error {
		return runtime.WriteFile(ctx, runtimeapi.WriteFileRequest{
			Ref:  runtimeRef,
			Path: path,
			Body: bytes.NewReader(content),
			Mode: mode,
			UID:  0,
			GID:  0,
		})
	}
	piApplier := piprofile.NewApplier(piProfiles, piprofile.NewFileGuestWriter(guestWrite, guestExec, piprofile.DefaultGuestHome))
	worker, err := operations.NewWorker(store, recovery.Executor{Instances: service, Snapshots: snapshotService, Clones: cloneService, Images: imageBuildService}, operations.Config{WorkerID: "openboxd-local", Concurrency: config.WorkerConcurrency, Lease: config.Lease, Mode: mode})
	if err != nil {
		return fail(err)
	}
	reconciler, err := reconcile.New(runtime, store, service, reconcile.Options{Mode: mode})
	if err != nil {
		return fail(err)
	}
	expiry, err := sandbox.NewExpiryScheduler(store, service, sandbox.ExpiryOptions{})
	if err != nil {
		return fail(err)
	}
	intervalSec := int(config.MetricsInterval / time.Second)
	if intervalSec <= 0 {
		intervalSec = metrics.DefaultIntervalSeconds
	}
	capacity := (60 * 60) / intervalSec
	if capacity < 1 {
		capacity = metrics.DefaultRetention
	}
	metricsHub := metrics.NewHub(capacity, intervalSec)
	metricsSampler := metrics.NewSampler(metricsHub, func(ctx context.Context) ([]metrics.Target, error) {
		rows, err := store.ListInstances(ctx)
		if err != nil {
			return nil, err
		}
		targets := make([]metrics.Target, 0, len(rows))
		for _, row := range rows {
			if row.ObservedState != domain.ObservedRunning || strings.TrimSpace(row.RuntimeRef) == "" {
				continue
			}
			targets = append(targets, metrics.Target{
				ID:         row.ID,
				RuntimeRef: row.RuntimeRef,
				Limits: metrics.Limits{
					VCPUs:       row.Resources.VCPUs,
					MemoryBytes: row.Resources.MemoryBytes,
					DiskBytes:   row.Resources.DiskBytes,
				},
			})
		}
		return targets, nil
	}, runtime.InstanceUsage)
	publicPort := config.SSHPublicPort
	if publicPort == 0 {
		if _, portStr, err := net.SplitHostPort(config.SSHAddress); err == nil {
			if p, err := strconv.Atoi(portStr); err == nil {
				publicPort = p
			}
		}
	}
	handler, err := httpapi.New(service, httpapi.Options{
		Auth:              authManager,
		Routes:            routeService,
		Console:           runtime,
		TerminalAudit:     durableTerminalAuditor{store: store, fallbackOwner: domain.OwnerID(config.OwnerID)},
		PiProfiles:        piProfiles,
		PiApplier:         piApplier,
		EgressProfiles:    egressService,
		Metrics:           metricsHub,
		TrustedProxyCIDRs: config.TrustedProxyCIDRs,
		SSHPublicHost:     config.SSHPublicHost,
		SSHPublicPort:     publicPort,
		Snapshots:         snapshotService,
		Clones:            cloneService,
		AuditEvents:       store,
		Artifacts:         artifactService,
		ImageBuilds:       imageBuildService,
		Webhooks:          webhookService,
		WebhookDeliveries: store,
		Mode:              mode,
		Operations:        store,
		SandboxPool:       sandboxPool,
	})
	if err != nil {
		return fail(err)
	}
	api := &daemonAPIServer{server: httpapi.NewServer(config.APIAddress, rootHandler(handler)), certificate: config.APITLSCertificate, key: config.APITLSKey}
	dispatcher, err := sshcommands.New(service, instancePublicKey, nil)
	if err != nil {
		return fail(err)
	}
	knownHosts, err := sshproxy.NewTOFUHostKeys(config.SSHKnownHostsPath)
	if err != nil {
		return fail(err)
	}
	instanceProxy, err := sshproxy.New(service, runtime, instanceSigner, sshproxy.Options{HostKey: knownHosts.Callback})
	if err != nil {
		return fail(err)
	}
	sshServer, err := sshgateway.New(sshgateway.Config{Address: config.SSHAddress, HostKeyPath: config.SSHHostKeyPath, Keys: store, Commands: dispatcher, Instances: instanceProxy, Ports: instanceProxy, Audit: durableSSHAuditor{store: store, fallbackOwner: domain.OwnerID(config.OwnerID)}})
	if err != nil {
		return fail(err)
	}
	return daemonComponents{
		operations:  worker,
		webhooks:    webhookWorker,
		reconciler:  expiryThenReconcile{expiry: expiry, egress: egressService, inner: reconciler},
		metrics:     metricsSampler,
		sandboxPool: sandboxPool,
		closer:      multiCloser{store, hostLock},
		api:         api,
		ssh:         sshServer,
	}, nil
}

type multiCloser []daemonCloser

func (c multiCloser) Close() error {
	var err error
	for i := len(c) - 1; i >= 0; i-- {
		if c[i] == nil {
			continue
		}
		err = errors.Join(err, c[i].Close())
	}
	return err
}

type egressRefresher interface {
	RefreshHostnameAllowlists(context.Context) (egress.RefreshReport, error)
}

type expiryThenReconcile struct {
	expiry *sandbox.ExpiryScheduler
	egress egressRefresher
	inner  reconciliationRunner
}

func (r expiryThenReconcile) RunOnce(ctx context.Context) (reconcile.Report, error) {
	if _, err := r.expiry.RunOnce(ctx); err != nil {
		return reconcile.Report{}, err
	}
	if r.egress != nil {
		if report, err := r.egress.RefreshHostnameAllowlists(ctx); err != nil {
			log.Printf("openboxd: egress dns refresh: %v", err)
		} else if report.Refreshed > 0 || len(report.Errors) > 0 {
			log.Printf("openboxd: egress dns refresh refreshed=%d skipped=%d errors=%d", report.Refreshed, report.Skipped, len(report.Errors))
		}
	}
	return r.inner.RunOnce(ctx)
}

type daemonAPIServer struct {
	server           *http.Server
	certificate, key string
}

func (s *daemonAPIServer) Run() error {
	if s.certificate != "" {
		return s.server.ListenAndServeTLS(s.certificate, s.key)
	}
	return s.server.ListenAndServe()
}

func (s *daemonAPIServer) Shutdown(ctx context.Context) error { return s.server.Shutdown(ctx) }

func runDaemon(ctx context.Context, config daemonConfig, factory componentFactory) error {
	if err := config.validate(); err != nil {
		return err
	}
	components, err := factory.Build(ctx, config)
	if err != nil {
		return err
	}
	if components.operations == nil || components.reconciler == nil || components.closer == nil {
		if components.closer != nil {
			_ = components.closer.Close()
		}
		return errors.New("daemon factory returned incomplete components")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	serviceErrors := make(chan error, 2)
	var serviceWG sync.WaitGroup
	if components.api != nil {
		serviceWG.Add(1)
		go func() {
			defer serviceWG.Done()
			err := components.api.Run()
			if err == nil && runCtx.Err() == nil {
				err = errors.New("API server stopped unexpectedly")
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				serviceErrors <- err
			}
		}()
	}
	if components.ssh != nil {
		serviceWG.Add(1)
		go func() {
			defer serviceWG.Done()
			err := components.ssh.ListenAndServe(runCtx)
			if err == nil && runCtx.Err() == nil {
				err = errors.New("SSH gateway stopped unexpectedly")
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				serviceErrors <- err
			}
		}()
	}
	if err := components.operations.RunOnce(runCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("openboxd: startup operation recovery: %v", err)
	}
	var wg sync.WaitGroup
	periodicCount := 2
	if components.webhooks != nil {
		periodicCount++
	}
	if components.metrics != nil {
		periodicCount++
	}
	if components.sandboxPool != nil && components.sandboxPool.Enabled() {
		periodicCount++
	}
	wg.Add(periodicCount)
	go periodic(runCtx, &wg, config.OperationInterval, false, "operation recovery", func(ctx context.Context) error {
		return components.operations.RunOnce(ctx)
	})
	if components.webhooks != nil {
		go periodic(runCtx, &wg, config.OperationInterval, false, "webhook delivery", func(ctx context.Context) error {
			return components.webhooks.RunOnce(ctx)
		})
	}
	go periodic(runCtx, &wg, config.ReconcileInterval, true, "reconciliation", func(ctx context.Context) error {
		_, err := components.reconciler.RunOnce(ctx)
		return err
	})
	if components.metrics != nil {
		go periodic(runCtx, &wg, config.MetricsInterval, true, "instance metrics", func(ctx context.Context) error {
			return components.metrics.RunOnce(ctx)
		})
	}
	if components.sandboxPool != nil && components.sandboxPool.Enabled() {
		go periodic(runCtx, &wg, sandboxpool.DefaultConfig().ReplenishInterval, true, "sandbox pool", func(ctx context.Context) error {
			components.sandboxPool.Replenish(ctx)
			return nil
		})
	}
	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-serviceErrors:
		cancel()
	}
	cancel()
	wg.Wait()
	if components.api != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		shutdownErr := components.api.Shutdown(shutdownCtx)
		shutdownCancel()
		if shutdownErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("shut down API: %w", shutdownErr))
		}
	}
	// The API shutdown drains handlers, while SSH stops from runCtx. Wait for
	// both transports before closing persistence they may still be using.
	serviceWG.Wait()
	if err := components.closer.Close(); err != nil {
		runErr = errors.Join(runErr, fmt.Errorf("close metadata store: %w", err))
	}
	return runErr
}

type sshAuditWriter interface {
	CreateAuditEvent(context.Context, domain.AuditEvent) error
}

type durableSSHAuditor struct {
	store         sshAuditWriter
	fallbackOwner domain.OwnerID
}

func (a durableSSHAuditor) Record(ctx context.Context, event sshgateway.AuditEvent) error {
	owner := event.OwnerID
	if owner == "" {
		owner = a.fallbackOwner
	}
	actor := event.Fingerprint
	if actor == "" {
		actor = "unauthenticated"
	}
	metadata, err := json.Marshal(struct {
		RemoteIP string `json:"remote_ip,omitempty"`
		Command  string `json:"command,omitempty"`
	}{RemoteIP: event.RemoteIP, Command: event.Command})
	if err != nil {
		return err
	}
	targetType, targetID := "gateway", "openbox"
	if event.Target != "" {
		targetType, targetID = "instance", event.Target
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	return a.store.CreateAuditEvent(ctx, domain.AuditEvent{ID: domain.AuditEventID("audit-" + hex.EncodeToString(raw)), OwnerID: owner, Actor: actor, Action: "ssh.session", TargetType: targetType, TargetID: targetID, Outcome: event.Outcome, MetadataJSON: metadata, CreatedAt: event.At.UTC()})
}

type durableTerminalAuditor struct {
	store         sshAuditWriter
	fallbackOwner domain.OwnerID
}

func (a durableTerminalAuditor) Record(ctx context.Context, event httpapi.TerminalAuditEvent) error {
	owner := event.OwnerID
	if owner == "" {
		owner = a.fallbackOwner
	}
	metadata, err := json.Marshal(struct {
		Phase       string `json:"phase"`
		SessionID   string `json:"session_id,omitempty"`
		SessionName string `json:"session_name,omitempty"`
		Reason      string `json:"reason,omitempty"`
	}{
		Phase:       event.Phase,
		SessionID:   event.SessionID,
		SessionName: event.SessionName,
		Reason:      event.Reason,
	})
	if err != nil {
		return err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	outcome := event.Phase
	if event.Phase == httpapi.TerminalAuditPhaseEnd && event.Reason != "" {
		outcome = event.Reason
	}
	return a.store.CreateAuditEvent(ctx, domain.AuditEvent{
		ID:           domain.AuditEventID("audit-" + hex.EncodeToString(raw)),
		OwnerID:      owner,
		Actor:        "browser",
		Action:       "terminal.session",
		TargetType:   "instance",
		TargetID:     string(event.InstanceID),
		Outcome:      outcome,
		MetadataJSON: metadata,
		CreatedAt:    event.At.UTC(),
	})
}

func periodic(ctx context.Context, wg *sync.WaitGroup, interval time.Duration, immediate bool, name string, run func(context.Context) error) {
	defer wg.Done()
	runCycle := func() {
		if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("openboxd: %s: %v", name, err)
		}
	}
	if immediate {
		runCycle()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCycle()
		}
	}
}
