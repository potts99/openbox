// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	openbox "github.com/openbox-dev/openbox/internal/client"
)

const networkUsage = `usage: openbox network COMMAND

Commands:
  profiles ls
  profiles show PROFILE
  profiles create NAME --mode MODE [--allow DEST,...]
  profiles edit PROFILE [--mode MODE] [--allow DEST,...]
  profiles delete PROFILE
  attach INSTANCE PROFILE
`

func runNetwork(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, networkUsage)
		return 2
	}
	switch args[0] {
	case "profiles":
		return runNetworkProfiles(ctx, api, args[1:], jsonOutput, stdout, stderr)
	case "attach":
		return runNetworkAttach(ctx, api, args[1:], jsonOutput, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown network command %q\n\n%s", args[0], networkUsage)
		return 2
	}
}

func runNetworkProfiles(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "usage: openbox network profiles ls|show|create|edit|delete")
	}
	switch args[0] {
	case "ls", "list":
		profiles, err := api.ListEgressProfiles(ctx)
		if err != nil {
			return commandError(stderr, err)
		}
		if jsonOutput {
			return encodeJSON(stdout, stderr, profiles)
		}
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "NAME\tMODE\tSYSTEM\tDESTINATIONS\tATTACHED\tID")
		for _, profile := range profiles {
			system := "no"
			if profile.System {
				system = "yes"
			}
			fmt.Fprintf(writer, "%s\t%s\t%s\t%d\t%d\t%s\n",
				profile.Name, profile.Mode, system, len(profile.AllowedDestinations), profile.AttachedInstanceCount, profile.ID)
		}
		_ = writer.Flush()
		return 0
	case "show":
		if len(args) != 2 {
			return usageError(stderr, "usage: openbox network profiles show PROFILE")
		}
		profile, err := resolveEgressProfile(ctx, api, args[1])
		if err != nil {
			return commandError(stderr, err)
		}
		if jsonOutput {
			return encodeJSON(stdout, stderr, profile)
		}
		fmt.Fprintf(stdout, "ID: %s\nName: %s\nMode: %s\nDNS policy: %s\nSystem: %v\nDestinations: %s\nAttached: %d\n",
			profile.ID, profile.Name, profile.Mode, profile.DNSPolicy, profile.System, strings.Join(profile.AllowedDestinations, ", "), profile.AttachedInstanceCount)
		return 0
	case "create":
		return runNetworkProfileCreate(ctx, api, args[1:], jsonOutput, stdout, stderr)
	case "edit":
		return runNetworkProfileEdit(ctx, api, args[1:], jsonOutput, stdout, stderr)
	case "delete", "rm":
		if len(args) != 2 {
			return usageError(stderr, "usage: openbox network profiles delete PROFILE")
		}
		profile, err := resolveEgressProfile(ctx, api, args[1])
		if err != nil {
			return commandError(stderr, err)
		}
		if err := api.DeleteEgressProfile(ctx, profile.ID); err != nil {
			return commandError(stderr, err)
		}
		if !jsonOutput {
			fmt.Fprintf(stdout, "deleted %s\n", profile.ID)
		}
		return 0
	default:
		return usageError(stderr, "usage: openbox network profiles ls|show|create|edit|delete")
	}
}

func runNetworkProfileCreate(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	name := ""
	mode := ""
	var destinations []string
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--mode":
			if i+1 >= len(args) {
				return usageError(stderr, "--mode requires a value")
			}
			i++
			mode = args[i]
		case strings.HasPrefix(arg, "--mode="):
			mode = strings.TrimPrefix(arg, "--mode=")
		case arg == "--allow":
			if i+1 >= len(args) {
				return usageError(stderr, "--allow requires a value")
			}
			i++
			destinations = splitCSV(args[i])
		case strings.HasPrefix(arg, "--allow="):
			destinations = splitCSV(strings.TrimPrefix(arg, "--allow="))
		case strings.HasPrefix(arg, "-"):
			return usageError(stderr, "unknown option "+arg)
		default:
			remaining = append(remaining, arg)
		}
	}
	if len(remaining) != 1 || mode == "" {
		return usageError(stderr, "usage: openbox network profiles create NAME --mode MODE [--allow DEST,...]")
	}
	name = remaining[0]
	profile, err := api.CreateEgressProfile(ctx, name, mode, destinations)
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, profile)
	}
	fmt.Fprintf(stdout, "%s\t%s\t%s\n", profile.ID, profile.Name, profile.Mode)
	return 0
}

func runNetworkProfileEdit(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "usage: openbox network profiles edit PROFILE [--mode MODE] [--allow DEST,...]")
	}
	profileRef := args[0]
	patch := map[string]any{}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--mode":
			if i+1 >= len(args) {
				return usageError(stderr, "--mode requires a value")
			}
			i++
			patch["mode"] = args[i]
		case strings.HasPrefix(arg, "--mode="):
			patch["mode"] = strings.TrimPrefix(arg, "--mode=")
		case arg == "--allow":
			if i+1 >= len(args) {
				return usageError(stderr, "--allow requires a value")
			}
			i++
			patch["allowed_destinations"] = splitCSV(args[i])
		case strings.HasPrefix(arg, "--allow="):
			patch["allowed_destinations"] = splitCSV(strings.TrimPrefix(arg, "--allow="))
		default:
			return usageError(stderr, "unknown option "+arg)
		}
	}
	if len(patch) == 0 {
		return usageError(stderr, "usage: openbox network profiles edit PROFILE [--mode MODE] [--allow DEST,...]")
	}
	profile, err := resolveEgressProfile(ctx, api, profileRef)
	if err != nil {
		return commandError(stderr, err)
	}
	updated, applyErrors, err := api.UpdateEgressProfile(ctx, profile.ID, patch)
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, map[string]any{"profile": updated, "apply_errors": applyErrors})
	}
	fmt.Fprintf(stdout, "%s\t%s\t%s\n", updated.ID, updated.Name, updated.Mode)
	for _, applyErr := range applyErrors {
		fmt.Fprintf(stderr, "apply error %s: %s\n", applyErr["instance_id"], applyErr["message"])
	}
	return 0
}

func runNetworkAttach(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		return usageError(stderr, "usage: openbox network attach INSTANCE PROFILE")
	}
	profile, err := resolveEgressProfile(ctx, api, args[1])
	if err != nil {
		return commandError(stderr, err)
	}
	instance, err := api.AttachEgressProfile(ctx, args[0], profile.ID)
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, instance)
	}
	fmt.Fprintf(stdout, "%s\t%s\t%s\n", instance.ID, instance.EgressProfileID, instance.NetworkPolicy.EgressMode)
	return 0
}

func resolveEgressProfile(ctx context.Context, api *openbox.Client, ref string) (openbox.EgressProfile, error) {
	profile, err := api.GetEgressProfile(ctx, ref)
	if err == nil {
		return profile, nil
	}
	profiles, listErr := api.ListEgressProfiles(ctx)
	if listErr != nil {
		return openbox.EgressProfile{}, err
	}
	for _, candidate := range profiles {
		if candidate.Name == ref || candidate.ID == ref {
			return candidate, nil
		}
	}
	return openbox.EgressProfile{}, err
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
