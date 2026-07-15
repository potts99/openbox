// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/app/recovery"
	"github.com/openbox-dev/openbox/internal/operations"
	"github.com/openbox-dev/openbox/internal/persistence/sqlite"
	"github.com/openbox-dev/openbox/internal/reconcile"
	"github.com/openbox-dev/openbox/internal/runtime/incus"
)

type daemonConfig struct {
	DatabasePath, IncusSocket, Project, ContainerProfile, VMProfile, StoragePool string
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
	return nil
}

type operationRunner interface{ RunOnce(context.Context) error }
type reconciliationRunner interface {
	RunOnce(context.Context) (reconcile.Report, error)
}
type daemonCloser interface{ Close() error }

type daemonComponents struct {
	operations operationRunner
	reconciler reconciliationRunner
	closer     daemonCloser
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
	runtime, err := incus.New(incus.Options{SocketPath: config.IncusSocket, Project: config.Project, ContainerProfile: config.ContainerProfile, VMProfile: config.VMProfile, StoragePool: config.StoragePool})
	if err != nil {
		return fail(err)
	}
	service, err := instances.New(runtime, store, instances.Options{})
	if err != nil {
		return fail(err)
	}
	mode := &operations.Mode{}
	worker, err := operations.NewWorker(store, recovery.Executor{Instances: service}, operations.Config{WorkerID: "openboxd-local", Concurrency: config.WorkerConcurrency, Lease: config.Lease, Mode: mode})
	if err != nil {
		return fail(err)
	}
	reconciler, err := reconcile.New(runtime, store, service, reconcile.Options{Mode: mode})
	if err != nil {
		return fail(err)
	}
	return daemonComponents{operations: worker, reconciler: reconciler, closer: store}, nil
}

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
	if err := components.operations.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("openboxd: startup operation recovery: %v", err)
	}
	if ctx.Err() != nil {
		return components.closer.Close()
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go periodic(ctx, &wg, config.OperationInterval, false, "operation recovery", func(ctx context.Context) error {
		return components.operations.RunOnce(ctx)
	})
	go periodic(ctx, &wg, config.ReconcileInterval, true, "reconciliation", func(ctx context.Context) error {
		_, err := components.reconciler.RunOnce(ctx)
		return err
	})
	<-ctx.Done()
	wg.Wait()
	if err := components.closer.Close(); err != nil {
		return fmt.Errorf("close metadata store: %w", err)
	}
	return nil
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
