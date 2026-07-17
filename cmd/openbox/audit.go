// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	openbox "github.com/openbox-dev/openbox/internal/client"
)

const auditUsage = `usage: openbox audit list [--limit N]
`

func runAudit(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, auditUsage)
		return 2
	}
	switch args[0] {
	case "list", "ls":
		limit := 100
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			if rest[i] == "--limit" {
				if i+1 >= len(rest) {
					return usageError(stderr, "usage: openbox audit list [--limit N]")
				}
				parsed, err := strconv.Atoi(rest[i+1])
				if err != nil || parsed < 1 {
					return usageError(stderr, "usage: openbox audit list [--limit N]")
				}
				limit = parsed
				i++
			}
		}
		events, err := api.ListAuditEvents(ctx, limit)
		if err != nil {
			return commandError(stderr, err)
		}
		if jsonOutput {
			return encodeJSON(stdout, stderr, events)
		}
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "CREATED\tACTION\tOUTCOME\tTARGET\tID")
		for _, event := range events {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s/%s\t%s\n",
				event.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
				event.Action, event.Outcome, event.TargetType, event.TargetID, event.ID)
		}
		_ = writer.Flush()
		return 0
	default:
		fmt.Fprintf(stderr, "unknown audit command %q\n\n%s", args[0], auditUsage)
		return 2
	}
}
