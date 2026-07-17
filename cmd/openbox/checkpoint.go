// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	openbox "github.com/openbox-dev/openbox/pkg/openbox"
)

func runSnapshot(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "usage: openbox snapshot <create|list|get|delete> ...")
	}
	switch args[0] {
	case "create":
		return runSnapshotCreate(ctx, api, args[1:], jsonOutput, stdout, stderr)
	case "list":
		return runSnapshotList(ctx, api, args[1:], jsonOutput, stdout, stderr)
	case "get":
		return runSnapshotGet(ctx, api, args[1:], jsonOutput, stdout, stderr)
	case "delete":
		return runSnapshotDelete(ctx, api, args[1:], jsonOutput, stdout, stderr)
	default:
		return usageError(stderr, "usage: openbox snapshot <create|list|get|delete> ...")
	}
}

func runSnapshotCreate(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox snapshot create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	idempotencyKey := flags.String("idempotency-key", "", "retry-safe request key")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 2 {
		return usageError(stderr, "usage: openbox snapshot create INSTANCE NAME")
	}
	key, err := mutationKey(*idempotencyKey)
	if err != nil {
		return commandError(stderr, err)
	}
	result, err := api.CreateSnapshot(ctx, positionals[0], positionals[1], key)
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, result)
	}
	if result.Snapshot != nil {
		fmt.Fprintf(stdout, "snapshot %s (%s)\n", result.Snapshot.Name, result.Snapshot.ID)
	}
	fmt.Fprintf(stdout, "operation %s (%s)\n", result.Operation.ID, result.Operation.Status)
	return 0
}

func runSnapshotList(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return usageError(stderr, "usage: openbox snapshot list INSTANCE")
	}
	items, err := api.ListSnapshots(ctx, args[0])
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, struct {
			Snapshots []openbox.Snapshot `json:"snapshots"`
		}{Snapshots: items})
	}
	writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tID\tCREATED")
	for _, item := range items {
		fmt.Fprintf(writer, "%s\t%s\t%s\n", item.Name, item.ID, item.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"))
	}
	_ = writer.Flush()
	return 0
}

func runSnapshotGet(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return usageError(stderr, "usage: openbox snapshot get SNAPSHOT_ID")
	}
	item, err := api.GetSnapshot(ctx, args[0])
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, item)
	}
	fmt.Fprintf(stdout, "ID:         %s\n", item.ID)
	fmt.Fprintf(stdout, "Instance:   %s\n", item.InstanceID)
	fmt.Fprintf(stdout, "Name:       %s\n", item.Name)
	fmt.Fprintf(stdout, "Created:    %s\n", item.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"))
	return 0
}

func runSnapshotDelete(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox snapshot delete", flag.ContinueOnError)
	flags.SetOutput(stderr)
	idempotencyKey := flags.String("idempotency-key", "", "retry-safe request key")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 {
		return usageError(stderr, "usage: openbox snapshot delete SNAPSHOT_ID")
	}
	key, err := mutationKey(*idempotencyKey)
	if err != nil {
		return commandError(stderr, err)
	}
	operation, err := api.DeleteSnapshot(ctx, positionals[0], key)
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, operation)
	}
	fmt.Fprintf(stdout, "operation %s (%s)\n", operation.ID, operation.Status)
	return 0
}

func runRestore(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox restore", flag.ContinueOnError)
	flags.SetOutput(stderr)
	publicKey := flags.String("ssh-key", "", "owner SSH public key")
	idempotencyKey := flags.String("idempotency-key", "", "retry-safe request key")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 2 {
		return usageError(stderr, "usage: openbox restore SNAPSHOT_ID NEW_NAME [--ssh-key PATH]")
	}
	key, err := mutationKey(*idempotencyKey)
	if err != nil {
		return commandError(stderr, err)
	}
	ownerPublicKey, err := resolvePublicKey(*publicKey)
	if err != nil {
		return usageError(stderr, err.Error())
	}
	result, err := api.RestoreSnapshot(ctx, positionals[0], openbox.RestoreSnapshotRequest{
		Name: positionals[1], OwnerPublicKey: ownerPublicKey,
	}, key)
	if err != nil {
		return commandError(stderr, err)
	}
	return printDerive(result, jsonOutput, stdout, stderr)
}

func runClone(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox clone", flag.ContinueOnError)
	flags.SetOutput(stderr)
	publicKey := flags.String("ssh-key", "", "owner SSH public key")
	idempotencyKey := flags.String("idempotency-key", "", "retry-safe request key")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 2 {
		return usageError(stderr, "usage: openbox clone INSTANCE NEW_NAME [--ssh-key PATH]")
	}
	key, err := mutationKey(*idempotencyKey)
	if err != nil {
		return commandError(stderr, err)
	}
	ownerPublicKey, err := resolvePublicKey(*publicKey)
	if err != nil {
		return usageError(stderr, err.Error())
	}
	result, err := api.CloneInstance(ctx, positionals[0], openbox.CloneInstanceRequest{
		Name: positionals[1], OwnerPublicKey: ownerPublicKey,
	}, key)
	if err != nil {
		return commandError(stderr, err)
	}
	return printDerive(result, jsonOutput, stdout, stderr)
}

func printDerive(result openbox.DeriveInstanceResult, jsonOutput bool, stdout, stderr io.Writer) int {
	if jsonOutput {
		return encodeJSON(stdout, stderr, result)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stdout, "warning\t%s\n", warning)
	}
	fmt.Fprintf(stdout, "storage_efficiency\t%s\n", result.StorageEfficiency)
	if result.StorageEfficiency == "confirmed" {
		fmt.Fprintln(stdout, "note\tcopy-on-write confirmed by runtime storage capabilities")
	} else {
		fmt.Fprintln(stdout, "note\tOpenBox does not claim copy-on-write for this copy")
	}
	if result.Instance != nil {
		fmt.Fprintf(stdout, "instance %s (%s)\n", result.Instance.Name, result.Instance.ID)
		if result.Instance.CloneSourceInstanceID != "" {
			fmt.Fprintf(stdout, "source_instance\t%s\n", result.Instance.CloneSourceInstanceID)
		}
		if result.Instance.CloneSourceSnapshotID != "" {
			fmt.Fprintf(stdout, "source_snapshot\t%s\n", result.Instance.CloneSourceSnapshotID)
		}
	}
	fmt.Fprintf(stdout, "operation %s (%s)\n", result.Operation.ID, result.Operation.Status)
	return 0
}
