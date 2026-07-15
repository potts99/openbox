// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	openbox "github.com/openbox-dev/openbox/internal/client"
)

const routeUsage = `usage: openbox route COMMAND

Commands:
  add INSTANCE --port PORT [--hostname HOST]
  ls
  rm ROUTE_ID
  publish ROUTE_ID
`

func runRoute(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, routeUsage)
		return 2
	}
	switch args[0] {
	case "add":
		return runRouteAdd(ctx, api, args[1:], jsonOutput, stdout, stderr)
	case "ls", "list":
		return runRouteList(ctx, api, args[1:], jsonOutput, stdout, stderr)
	case "rm", "delete":
		return runRouteDelete(ctx, api, args[1:], jsonOutput, stdout, stderr)
	case "publish":
		return runRoutePublish(ctx, api, args[1:], jsonOutput, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown route command %q\n\n%s", args[0], routeUsage)
		return 2
	}
}

func runRouteAdd(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	instanceID := ""
	hostname := ""
	port := 0
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--port" || arg == "--hostname":
			if i+1 >= len(args) {
				return usageError(stderr, arg+" requires a value")
			}
			i++
			if arg == "--port" {
				value, err := strconv.Atoi(args[i])
				if err != nil || value < 1 || value > 65535 {
					return usageError(stderr, "invalid --port")
				}
				port = value
			} else {
				hostname = args[i]
			}
		case strings.HasPrefix(arg, "--port="):
			value, err := strconv.Atoi(strings.TrimPrefix(arg, "--port="))
			if err != nil || value < 1 || value > 65535 {
				return usageError(stderr, "invalid --port")
			}
			port = value
		case strings.HasPrefix(arg, "--hostname="):
			hostname = strings.TrimPrefix(arg, "--hostname=")
		case strings.HasPrefix(arg, "-"):
			return usageError(stderr, "unknown option "+arg)
		default:
			remaining = append(remaining, arg)
		}
	}
	if len(remaining) != 1 || port == 0 {
		return usageError(stderr, "usage: openbox route add INSTANCE --port PORT [--hostname HOST]")
	}
	instanceID = remaining[0]
	if hostname == "" {
		hostname = instanceID
	}
	route, err := api.CreateRoute(ctx, openbox.CreateRouteRequest{
		InstanceID: instanceID, Hostname: hostname, TargetPort: port,
	})
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, route)
	}
	fmt.Fprintf(stdout, "%s\t%s\t%d\t%s\n", route.ID, route.Hostname, route.TargetPort, route.Visibility)
	return 0
}

func runRouteList(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		return usageError(stderr, "route ls does not accept positional arguments")
	}
	items, err := api.ListRoutes(ctx)
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, items)
	}
	writer := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tHOSTNAME\tPORT\tVISIBILITY\tINSTANCE")
	for _, route := range items {
		fmt.Fprintf(writer, "%s\t%s\t%d\t%s\t%s\n", route.ID, route.Hostname, route.TargetPort, route.Visibility, route.InstanceID)
	}
	_ = writer.Flush()
	return 0
}

func runRouteDelete(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return usageError(stderr, "usage: openbox route rm ROUTE_ID")
	}
	if err := api.DeleteRoute(ctx, args[0]); err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, map[string]string{"id": args[0], "status": "deleted"})
	}
	fmt.Fprintf(stdout, "deleted %s\n", args[0])
	return 0
}

func runRoutePublish(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return usageError(stderr, "usage: openbox route publish ROUTE_ID")
	}
	route, err := api.PublishRoute(ctx, args[0])
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, route)
	}
	fmt.Fprintf(stdout, "%s\t%s\t%d\t%s\n", route.ID, route.Hostname, route.TargetPort, route.Visibility)
	return 0
}
