// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openbox-dev/openbox/internal/version"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(version.Version)
		return
	}
	config := daemonConfig{}
	flag.StringVar(&config.DatabasePath, "database", "/var/lib/openbox/openbox.db", "SQLite database path")
	flag.StringVar(&config.IncusSocket, "incus-socket", "/var/lib/incus/unix.socket", "Incus Unix socket")
	flag.StringVar(&config.Project, "incus-project", "openbox", "Incus project")
	flag.StringVar(&config.ContainerProfile, "container-profile", "openbox-container", "Incus container profile")
	flag.StringVar(&config.VMProfile, "vm-profile", "openbox-vm", "Incus VM profile")
	flag.StringVar(&config.StoragePool, "storage-pool", "", "Incus storage pool used for explicit disk limits")
	flag.IntVar(&config.WorkerConcurrency, "worker-concurrency", 2, "maximum concurrent durable operations")
	flag.DurationVar(&config.OperationInterval, "operation-interval", time.Second, "durable operation polling interval")
	flag.DurationVar(&config.ReconcileInterval, "reconcile-interval", 30*time.Second, "desired-state reconciliation interval")
	flag.DurationVar(&config.Lease, "operation-lease", time.Minute, "durable operation claim lease")
	flag.Parse()
	if err := config.validate(); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runDaemon(ctx, config, realComponentFactory{}); err != nil {
		log.Fatal(err)
	}
}
