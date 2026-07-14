// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/openbox-dev/openbox/internal/doctor"
	incusruntime "github.com/openbox-dev/openbox/internal/runtime/incus"
	"github.com/openbox-dev/openbox/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, defaultDiscoverer))
}

type discovererFactory func(string, time.Duration) (doctor.Discoverer, error)

func defaultDiscoverer(socket string, timeout time.Duration) (doctor.Discoverer, error) {
	return incusruntime.New(incusruntime.Options{SocketPath: socket, Timeout: timeout})
}

func run(args []string, stdout, stderr io.Writer, factory discovererFactory) int {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(stdout, version.Version)
		return 0
	}
	if len(args) == 0 || args[0] != "doctor" {
		fmt.Fprintln(stderr, "usage: openbox doctor [--json] [--socket PATH] [--timeout DURATION]")
		return 2
	}
	flags := flag.NewFlagSet("openbox doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	socket := flags.String("socket", incusruntime.DefaultSocket, "local Incus Unix socket")
	timeout := flags.Duration("timeout", 10*time.Second, "Incus request timeout")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "doctor does not accept positional arguments")
		return 2
	}
	discoverer, err := factory(*socket, *timeout)
	if err != nil {
		fmt.Fprintf(stderr, "configure doctor: %v\n", err)
		return 2
	}
	report := doctor.Run(context.Background(), discoverer)
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "write doctor report: %v\n", err)
			return 2
		}
	} else {
		_, _ = io.WriteString(stdout, doctor.FormatHuman(report))
	}
	if report.HasFatal() {
		return 1
	}
	return 0
}
