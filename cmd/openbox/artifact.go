// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	openbox "github.com/openbox-dev/openbox/pkg/openbox"
)

func runArtifact(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "usage: openbox artifact <put|get|list|rm> ...")
	}
	switch args[0] {
	case "put":
		return runArtifactPut(ctx, api, args[1:], jsonOutput, stdout, stderr)
	case "get":
		return runArtifactGet(ctx, api, args[1:], stdout, stderr)
	case "list":
		return runArtifactList(ctx, api, args[1:], jsonOutput, stdout, stderr)
	case "rm":
		return runArtifactRemove(ctx, api, args[1:], stderr)
	default:
		return usageError(stderr, "usage: openbox artifact <put|get|list|rm> ...")
	}
}

func runArtifactPut(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox artifact put", flag.ContinueOnError)
	flags.SetOutput(stderr)
	contentType := flags.String("content-type", "application/octet-stream", "artifact MIME type")
	idempotencyKey := flags.String("idempotency-key", "", "retry-safe request key")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 3 {
		return usageError(stderr, "usage: openbox artifact put INSTANCE PATH LOCAL_FILE [--content-type TYPE]")
	}
	file, err := os.Open(positionals[2])
	if err != nil {
		return commandError(stderr, fmt.Errorf("open %q: %w", positionals[2], err))
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return commandError(stderr, fmt.Errorf("stat %q: %w", positionals[2], err))
	}
	if !info.Mode().IsRegular() {
		return usageError(stderr, "LOCAL_FILE must be a regular file")
	}
	key, err := mutationKey(*idempotencyKey)
	if err != nil {
		return commandError(stderr, err)
	}
	artifact, err := api.PutArtifact(ctx, positionals[0], positionals[1], file, info.Size(), *contentType, key)
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, artifact)
	}
	fmt.Fprintf(stdout, "artifact %s (%s)\n", artifact.Path, artifact.ID)
	return 0
}

func runArtifactGet(ctx context.Context, api *openbox.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox artifact get", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "write bytes to FILE (default stdout)")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 2 {
		return usageError(stderr, "usage: openbox artifact get INSTANCE PATH [--output FILE]")
	}
	result, err := api.GetArtifact(ctx, positionals[0], positionals[1])
	if err != nil {
		return commandError(stderr, err)
	}
	defer result.Body.Close()
	if *output == "" {
		if _, err := io.Copy(stdout, result.Body); err != nil {
			return commandError(stderr, err)
		}
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		return commandError(stderr, err)
	}
	file, err := os.Create(*output)
	if err != nil {
		return commandError(stderr, err)
	}
	_, copyErr := io.Copy(file, result.Body)
	closeErr := file.Close()
	if copyErr != nil {
		return commandError(stderr, copyErr)
	}
	if closeErr != nil {
		return commandError(stderr, closeErr)
	}
	return 0
}

func runArtifactList(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox artifact list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	prefix := flags.String("prefix", "", "logical path prefix")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 {
		return usageError(stderr, "usage: openbox artifact list INSTANCE [--prefix PREFIX]")
	}
	items, err := api.ListArtifacts(ctx, positionals[0], *prefix)
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, struct {
			Items []openbox.Artifact `json:"items"`
		}{Items: items})
	}
	writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "PATH\tSIZE\tSHA256")
	for _, item := range items {
		fmt.Fprintf(writer, "%s\t%d\t%s\n", item.Path, item.SizeBytes, item.SHA256)
	}
	_ = writer.Flush()
	return 0
}

func runArtifactRemove(ctx context.Context, api *openbox.Client, args []string, stderr io.Writer) int {
	if len(args) != 2 {
		return usageError(stderr, "usage: openbox artifact rm INSTANCE PATH")
	}
	if err := api.DeleteArtifact(ctx, args[0], args[1]); err != nil {
		return commandError(stderr, err)
	}
	return 0
}
