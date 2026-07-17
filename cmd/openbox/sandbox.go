// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	openbox "github.com/openbox-dev/openbox/pkg/openbox"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/execstream"
	"github.com/openbox-dev/openbox/internal/sandbox"
)

func runSandbox(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "usage: openbox sandbox <exec|extend> ...")
	}
	switch args[0] {
	case "exec":
		return runSandboxExec(ctx, api, args[1:], stdout, stderr)
	case "extend":
		return runSandboxExtend(ctx, api, args[1:], jsonOutput, stdout, stderr)
	default:
		return usageError(stderr, "usage: openbox sandbox <exec|extend> ...")
	}
}

func runSandboxExec(ctx context.Context, api *openbox.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox sandbox exec", flag.ContinueOnError)
	flags.SetOutput(stderr)
	workdir := flags.String("workdir", "", "absolute working directory inside the instance")
	timeout := flags.Duration("timeout", 0, "exec timeout (default server policy)")
	stdinFile := flags.String("stdin", "", "optional file to send as stdin (use - for stdin)")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) < 2 {
		return usageError(stderr, "usage: openbox sandbox exec ID ARGV...")
	}
	id, argv := positionals[0], positionals[1:]
	req := openbox.ExecInstanceRequest{Argv: argv, WorkingDir: *workdir}
	if *timeout > 0 {
		req.TimeoutSeconds = int(timeout.Seconds())
		if req.TimeoutSeconds < 1 {
			req.TimeoutSeconds = 1
		}
	}
	if *stdinFile != "" {
		var data []byte
		if *stdinFile == "-" {
			data, err = io.ReadAll(os.Stdin)
		} else {
			data, err = os.ReadFile(*stdinFile)
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		req.StdinBase64 = base64.StdEncoding.EncodeToString(data)
	}
	body, err := api.ExecInstance(ctx, id, req)
	if err != nil {
		return commandError(stderr, err)
	}
	defer body.Close()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), execstream.MaxFrameBytes+1024)
	exitCode := 0
	for scanner.Scan() {
		frame, err := execstream.Decode(scanner.Bytes())
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		switch f := frame.(type) {
		case execstream.StdoutFrame:
			_, _ = stdout.Write(f.Data)
		case execstream.StderrFrame:
			_, _ = stderr.Write(f.Data)
		case execstream.ExitFrame:
			exitCode = f.Code
		case execstream.ErrorFrame:
			fmt.Fprintf(stderr, "exec error: %s\n", f.Code)
			if f.Message != "" {
				fmt.Fprintln(stderr, f.Message)
			}
			return 1
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return exitCode
}

func runSandboxExtend(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox sandbox extend", flag.ContinueOnError)
	flags.SetOutput(stderr)
	by := flags.Duration("by", time.Hour, "duration to add to expires_at")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 {
		return usageError(stderr, "usage: openbox sandbox extend ID [--by DURATION]")
	}
	seconds := int(by.Seconds())
	if seconds < 1 {
		return usageError(stderr, "--by must be at least one second")
	}
	instance, err := api.ExtendInstance(ctx, positionals[0], seconds)
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, instance)
	}
	printInstanceStatus(stdout, instance, time.Now().UTC())
	return 0
}

func printInstanceStatus(stdout io.Writer, instance openbox.Instance, now time.Time) {
	fmt.Fprintf(stdout, "Name: %s\nID: %s\nKind: %s\nImage: %s\nDesired: %s\nObserved: %s\nIsolation: %s (requested %s)\nEgress profile: %s\nEgress: %s\nNetwork ACLs: %s\nHostname resolution: %s\nDenied flows: %d\nCPUs: %d\nMemory: %s\nDisk: %s\n",
		instance.Name, instance.ID, instance.Kind, instance.ImageID, instance.DesiredState, instance.ObservedState,
		instance.ActualIsolation, instance.RequestedIsolation, instance.EgressProfileID,
		sandbox.EgressLabel(domain.EgressMode(instance.NetworkPolicy.EgressMode)),
		strings.Join(instance.NetworkPolicy.ACLs, ", "), instance.NetworkPolicy.Resolution.State, instance.NetworkPolicy.DeniedFlows,
		instance.Resources.VCPUs, formatBytes(instance.Resources.MemoryBytes), formatBytes(instance.Resources.DiskBytes))
	if instance.Kind == "sandbox" && instance.ActualIsolation == "container" {
		fmt.Fprintln(stdout, "Isolation note: running as container (omitted request selects container when KVM is unavailable; explicit strong never downgrades)")
	}
	if instance.CloneSourceInstanceID != "" {
		fmt.Fprintf(stdout, "Clone source instance: %s\n", instance.CloneSourceInstanceID)
	}
	if instance.CloneSourceSnapshotID != "" {
		fmt.Fprintf(stdout, "Clone source checkpoint: %s\n", instance.CloneSourceSnapshotID)
	}
	if instance.CloneSourceImageID != "" {
		fmt.Fprintf(stdout, "Clone source image: %s\n", instance.CloneSourceImageID)
	}
	if instance.ExpiresAt != nil {
		fmt.Fprintf(stdout, "Expires: %s (%s)\n", instance.ExpiresAt.UTC().Format(time.RFC3339), sandbox.FormatRemaining(instance.ExpiresAt, now))
	}
	if instance.ErrorCode != "" {
		fmt.Fprintf(stdout, "Cleanup/error: %s", instance.ErrorCode)
		if instance.ErrorStage != "" {
			fmt.Fprintf(stdout, " at %s", instance.ErrorStage)
		}
		fmt.Fprintln(stdout)
	}
}
