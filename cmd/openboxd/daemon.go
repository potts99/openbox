// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/app/recovery"
	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/httpapi"
	"github.com/openbox-dev/openbox/internal/operations"
	"github.com/openbox-dev/openbox/internal/persistence/sqlite"
	"github.com/openbox-dev/openbox/internal/reconcile"
	"github.com/openbox-dev/openbox/internal/runtime/incus"
)

type daemonConfig struct {
	DatabasePath, IncusSocket, Project, ContainerProfile, VMProfile, StoragePool string
	APIAddress, APITLSCertificate, APITLSKey                                     string
	OwnerID, OwnerName                                                           string
	WorkerConcurrency                                                            int
	OperationInterval, ReconcileInterval, Lease                                  time.Duration
}

func (c daemonConfig) validate() error {
	if c.DatabasePath == "" || c.IncusSocket == "" {
		return errors.New("database and Incus socket paths are required")
	}
	if c.WorkerConcurrency <= 0 || c.OperationInterval <= 0 || c.ReconcileInterval <= 0 || c.Lease <= 0 {
		return errors.New("worker concurrency and daemon intervals must be positive")
	}
	if c.APIAddress == "" || c.OwnerID == "" || c.OwnerName == "" {
		return errors.New("API address and local owner identity are required")
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

type daemonComponents struct {
	operations operationRunner
	reconciler reconciliationRunner
	closer     daemonCloser
	api        apiRunner
}

type componentFactory interface {
	Build(context.Context, daemonConfig) (daemonComponents, error)
}

type realComponentFactory struct{}

func (realComponentFactory) Build(ctx context.Context, config daemonConfig) (daemonComponents, error) {
	if err := os.MkdirAll(filepath.Dir(config.DatabasePath), 0o700); err != nil {
		return daemonComponents{}, fmt.Errorf("create database directory: %w", err)
	}
	store, err := sqlite.Open(ctx, config.DatabasePath)
	if err != nil {
		return daemonComponents{}, err
	}
	fail := func(err error) (daemonComponents, error) { _ = store.Close(); return daemonComponents{}, err }
	now := time.Now().UTC()
	if err := store.EnsureOwner(ctx, domain.Owner{ID: domain.OwnerID(config.OwnerID), Name: config.OwnerName, CreatedAt: now, UpdatedAt: now}); err != nil {
		return fail(err)
	}
	authManager, err := auth.New(store)
	if err != nil {
		return fail(err)
	}
	bootstrapSecret, err := authManager.EnsureBootstrap(ctx)
	if err != nil {
		return fail(err)
	}
	if bootstrapSecret != "" {
		log.Printf("openboxd: one-time owner bootstrap secret (expires in %s): %s", auth.DefaultBootstrapTTL, bootstrapSecret)
	}
	runtime, err := incus.New(incus.Options{SocketPath: config.IncusSocket, Project: config.Project, ContainerProfile: config.ContainerProfile, VMProfile: config.VMProfile, StoragePool: config.StoragePool})
	if err != nil {
		return fail(err)
	}
	mode := &operations.Mode{}
	service, err := instances.New(runtime, store, instances.Options{Mode: mode})
	if err != nil {
		return fail(err)
	}
	worker, err := operations.NewWorker(store, recovery.Executor{Instances: service}, operations.Config{WorkerID: "openboxd-local", Concurrency: config.WorkerConcurrency, Lease: config.Lease, Mode: mode})
	if err != nil {
		return fail(err)
	}
	reconciler, err := reconcile.New(runtime, store, service, reconcile.Options{Mode: mode})
	if err != nil {
		return fail(err)
	}
	handler, err := httpapi.New(service, httpapi.Options{Auth: authManager})
	if err != nil {
		return fail(err)
	}
	api := &daemonAPIServer{server: httpapi.NewServer(config.APIAddress, rootHandler(handler)), certificate: config.APITLSCertificate, key: config.APITLSKey}
	return daemonComponents{operations: worker, reconciler: reconciler, closer: store, api: api}, nil
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
	apiErrors := make(chan error, 1)
	if components.api != nil {
		go func() {
			if err := components.api.Run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				apiErrors <- err
			}
		}()
	}
	if err := components.operations.RunOnce(runCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("openboxd: startup operation recovery: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go periodic(runCtx, &wg, config.OperationInterval, false, "operation recovery", func(ctx context.Context) error {
		return components.operations.RunOnce(ctx)
	})
	go periodic(runCtx, &wg, config.ReconcileInterval, true, "reconciliation", func(ctx context.Context) error {
		_, err := components.reconciler.RunOnce(ctx)
		return err
	})
	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-apiErrors:
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
	if err := components.closer.Close(); err != nil {
		runErr = errors.Join(runErr, fmt.Errorf("close metadata store: %w", err))
	}
	return runErr
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
