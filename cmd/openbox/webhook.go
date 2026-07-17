// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	openbox "github.com/openbox-dev/openbox/pkg/openbox"
)

const webhookUsage = `usage: openbox webhook <list|create|delete|deliveries> ...

Examples:
  openbox webhook list
  openbox webhook create https://receiver.example/hooks --event operation.terminal
  openbox webhook deliveries --status dead
`

type eventFlags []string

func (events *eventFlags) String() string { return strings.Join(*events, ",") }
func (events *eventFlags) Set(value string) error {
	*events = append(*events, value)
	return nil
}

func runWebhook(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, webhookUsage)
	}
	switch args[0] {
	case "list", "ls":
		if len(args) != 1 {
			return usageError(stderr, "usage: openbox webhook list")
		}
		items, err := api.ListWebhookSubscriptions(ctx)
		if err != nil {
			return commandError(stderr, err)
		}
		if jsonOutput {
			return encodeJSON(stdout, stderr, items)
		}
		writer := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(writer, "ID\tENABLED\tEVENTS\tURL\tDESCRIPTION")
		for _, item := range items {
			fmt.Fprintf(writer, "%s\t%t\t%s\t%s\t%s\n", item.ID, item.Enabled, strings.Join(item.Events, ","), item.URL, item.Description)
		}
		_ = writer.Flush()
		return 0
	case "create":
		return runWebhookCreate(ctx, api, args[1:], jsonOutput, stdout, stderr)
	case "delete", "rm":
		if len(args) != 2 {
			return usageError(stderr, "usage: openbox webhook delete SUBSCRIPTION_ID")
		}
		if err := api.DeleteWebhookSubscription(ctx, args[1]); err != nil {
			return commandError(stderr, err)
		}
		if jsonOutput {
			return encodeJSON(stdout, stderr, map[string]string{"id": args[1], "status": "deleted"})
		}
		fmt.Fprintf(stdout, "deleted %s\n", args[1])
		return 0
	case "deliveries":
		return runWebhookDeliveries(ctx, api, args[1:], jsonOutput, stdout, stderr)
	default:
		return usageError(stderr, webhookUsage)
	}
}

func runWebhookCreate(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox webhook create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: openbox webhook create URL --event TYPE [--event TYPE] [--description TEXT]")
		fmt.Fprintln(stderr, "\nExamples:")
		fmt.Fprintln(stderr, "  openbox webhook create https://receiver.example/hooks --event operation.terminal")
	}
	var events eventFlags
	flags.Var(&events, "event", "event type (repeatable)")
	description := flags.String("description", "", "operator label")
	disabled := flags.Bool("disabled", false, "create disabled")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 || len(events) == 0 {
		return usageError(stderr, "usage: openbox webhook create URL --event TYPE [--event TYPE] [--description TEXT]")
	}
	enabled := !*disabled
	item, err := api.CreateWebhookSubscription(ctx, openbox.CreateWebhookSubscriptionRequest{
		URL: positionals[0], Description: *description, Events: events, Enabled: &enabled,
	})
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, item)
	}
	fmt.Fprintf(stdout, "created %s\nsecret: %s\n", item.ID, item.Secret)
	return 0
}

func runWebhookDeliveries(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox webhook deliveries", flag.ContinueOnError)
	flags.SetOutput(stderr)
	status := flags.String("status", "", "pending, delivered, failed, dead, or canceled")
	subscriptionID := flags.String("subscription-id", "", "subscription id")
	limit := flags.Int("limit", 100, "maximum records (1-1000)")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 0 {
		return usageError(stderr, "usage: openbox webhook deliveries [--status STATUS] [--subscription-id ID] [--limit N]")
	}
	items, err := api.ListWebhookDeliveries(ctx, openbox.ListWebhookDeliveriesOptions{Status: *status, SubscriptionID: *subscriptionID, Limit: *limit})
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, items)
	}
	writer := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tEVENT\tSUBSCRIPTION\tATTEMPT\tSTATUS\tHTTP\tERROR")
	for _, item := range items {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%d\t%s\t%d\t%s\n", item.ID, item.EventID, item.SubscriptionID, item.Attempt, item.Status, item.HTTPStatus, item.ErrorClass)
	}
	_ = writer.Flush()
	return 0
}
