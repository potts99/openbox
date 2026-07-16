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

	"github.com/openbox-dev/openbox/internal/httpapi"
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
	flag.StringVar(&config.APIAddress, "api-address", httpapi.DefaultAddress, "private OpenBox API listen address")
	flag.StringVar(&config.APITLSCertificate, "api-tls-cert", "", "optional API TLS certificate")
	flag.StringVar(&config.APITLSKey, "api-tls-key", "", "optional API TLS private key")
	flag.StringVar(&config.SSHAddress, "ssh-address", ":2222", "OpenBox SSH gateway listen address")
	flag.StringVar(&config.SSHHostKeyPath, "ssh-host-key", "/var/lib/openbox/ssh/gateway_host", "stable SSH gateway host key path")
	flag.StringVar(&config.SSHInstanceKeyPath, "ssh-instance-key", "/var/lib/openbox/ssh/instance_client", "internal instance SSH client key path")
	flag.StringVar(&config.SSHKnownHostsPath, "ssh-known-hosts", "/var/lib/openbox/ssh/known_instances", "pinned instance SSH host keys")
	flag.StringVar(&config.OwnerID, "owner-id", "owner-local", "stable local owner identifier")
	flag.StringVar(&config.OwnerName, "owner-name", "Local owner", "local owner display name")
	flag.Func("trusted-proxy-cidr", "CIDR permitted to provide X-Forwarded-* headers (repeatable; defaults to loopback)", func(value string) error {
		config.TrustedProxyCIDRs = append(config.TrustedProxyCIDRs, value)
		return nil
	})
	flag.IntVar(&config.WorkerConcurrency, "worker-concurrency", 2, "maximum concurrent durable operations")
	flag.DurationVar(&config.OperationInterval, "operation-interval", time.Second, "durable operation polling interval")
	flag.DurationVar(&config.ReconcileInterval, "reconcile-interval", 30*time.Second, "desired-state reconciliation interval")
	flag.DurationVar(&config.MetricsInterval, "metrics-interval", 10*time.Second, "instance usage sampling interval")
	flag.DurationVar(&config.Lease, "operation-lease", time.Minute, "durable operation claim lease")
	flag.Parse()
	if len(config.TrustedProxyCIDRs) == 0 {
		// Default-trust loopback so TLS-terminated reverse proxies on the same
		// host (e.g. Caddy → 127.0.0.1:8443) can set Secure cookies via
		// X-Forwarded-Proto without requiring a flag on every install.
		config.TrustedProxyCIDRs = []string{"127.0.0.0/8", "::1/128"}
	}
	if err := config.validate(); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runDaemon(ctx, config, realComponentFactory{}); err != nil {
		log.Fatal(err)
	}
}
