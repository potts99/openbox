// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	openbox "github.com/openbox-dev/openbox/pkg/openbox"
)

func runImage(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "build" {
		return usageError(stderr, "usage: openbox image build [--architecture x86_64|aarch64] [--runtime container|vm] [--idempotency-key KEY]")
	}
	flags := flag.NewFlagSet("openbox image build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: openbox image build [--architecture x86_64|aarch64] [--runtime container|vm] [--idempotency-key KEY]")
		fmt.Fprintln(stderr, "\nExamples:")
		fmt.Fprintln(stderr, "  openbox image build")
		fmt.Fprintln(stderr, "  openbox image build --runtime vm --idempotency-key devbox-v1")
	}
	architecture := flags.String("architecture", "", "target architecture: x86_64 or aarch64")
	runtime := flags.String("runtime", "container", "target runtime: container or vm")
	idempotencyKey := flags.String("idempotency-key", "", "retry-safe request key")
	positionals, err := parseInterspersed(flags, args[1:])
	if err != nil {
		return 2
	}
	if len(positionals) != 0 {
		return usageError(stderr, "usage: openbox image build [OPTIONS]")
	}
	if *architecture != "" && *architecture != "x86_64" && *architecture != "aarch64" {
		return usageError(stderr, "invalid --architecture: use x86_64 or aarch64")
	}
	switch *runtime {
	case "container":
	case "vm":
		*runtime = "virtual_machine"
	default:
		return usageError(stderr, "invalid --runtime: use container or vm")
	}
	key, err := mutationKey(*idempotencyKey)
	if err != nil {
		return commandError(stderr, err)
	}
	operation, err := api.BuildImage(ctx, openbox.BuildImageRequest{Architecture: *architecture, Runtime: *runtime}, key)
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, operation)
	}
	fmt.Fprintf(stdout, "Operation %s: %s\n", operation.ID, operation.Status)
	fmt.Fprintf(stdout, "Watch logs: openbox operation watch %s\n", operation.ID)
	return 0
}
