// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/openbox-dev/openbox/internal/version"
	openbox "github.com/openbox-dev/openbox/pkg/openbox"
)

const usage = `usage: openbox COMMAND [OPTIONS]

Commands:
  doctor                 Check server and runtime capabilities
  setup                  Create the first admin (username + password)
  new NAME               Create an instance (sandbox: --lifetime, --egress-profile)
  ls                     List instances
  inspect ID             Show an instance (TTL, isolation, cleanup errors)
  start ID               Start an instance
  stop ID                Stop an instance
  restart ID             Restart an instance
  rm ID                  Delete an instance
  sandbox exec ID -- CMD Run argv inside a running sandbox (NDJSON frames)
  sandbox extend ID      Extend a Sandbox TTL (--by DURATION)
  snapshot create ID NAME  Create a disk-only checkpoint
  snapshot list ID         List checkpoints for an instance
  snapshot get ID          Inspect a checkpoint
  snapshot delete ID       Delete a checkpoint
  restore SNAPSHOT NAME    Restore a checkpoint as a new instance
  clone INSTANCE NAME      Clone a live instance
  artifact put|get|list|rm Manage instance-attached artifacts
  image build            Build the embedded Devbox image
  webhook list|create|delete|deliveries  Manage outbound webhooks
  route                  Manage HTTPS routes
  network                Manage egress profiles and attach policy
  audit list             List policy and security audit events
  backup create|verify|restore  Create, verify, or restore a backup
  forward INSTANCE PORT  SSH-tunnel an instance port to localhost
  operation watch ID     Stream operation progress
  ssh-config print       Print optional OpenSSH aliases
  ssh-config install     Install aliases without replacing existing entries

Global options:
  --server URL            OpenBox API URL (default http://127.0.0.1:8443)
  --token TOKEN           API token, including scoped tokens (or OPENBOX_TOKEN)
  --timeout DURATION      Request timeout (default 30s)
  --json                  Machine-readable output
  --version               Print the CLI version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type commonOptions struct {
	server  string
	token   string
	timeout time.Duration
	json    bool
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(stdout, version.Version)
		return 0
	}
	options, commandArgs, err := parseCommon(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if len(commandArgs) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if commandArgs[0] == "ssh-config" {
		return runSSHConfig(commandArgs[1:], stdout, stderr)
	}
	if commandArgs[0] == "forward" {
		return runForward(commandArgs[1:], stdout, stderr, exec.LookPath, func(cmd *exec.Cmd) error { return cmd.Run() })
	}
	if commandArgs[0] == "backup" {
		return runBackup(commandArgs[1:], options.json, stdout, stderr)
	}
	if commandArgs[0] == "setup" {
		return runSetup(commandArgs[1:], options, stdout, stderr)
	}
	httpClient := &http.Client{Timeout: options.timeout}
	api, err := openbox.New(openbox.Options{BaseURL: options.server, HTTPClient: httpClient, UserAgent: "openbox-cli/" + version.Version, Token: options.token})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx := context.Background()
	if _, err := api.Negotiate(ctx); err != nil {
		printError(stderr, err)
		return 1
	}

	command, rest := commandArgs[0], commandArgs[1:]
	switch command {
	case "doctor":
		return runDoctor(ctx, api, rest, options.json, stdout, stderr)
	case "new":
		return runNew(ctx, api, rest, options.json, stdout, stderr)
	case "ls":
		return runList(ctx, api, rest, options.json, stdout, stderr)
	case "inspect":
		return runInspect(ctx, api, rest, options.json, stdout, stderr)
	case "start", "stop", "restart", "rm":
		return runLifecycle(ctx, api, command, rest, options.json, stdout, stderr)
	case "operation":
		return runOperation(ctx, api, rest, options.json, stdout, stderr)
	case "route":
		return runRoute(ctx, api, rest, options.json, stdout, stderr)
	case "network":
		return runNetwork(ctx, api, rest, options.json, stdout, stderr)
	case "audit":
		return runAudit(ctx, api, rest, options.json, stdout, stderr)
	case "sandbox":
		return runSandbox(ctx, api, rest, options.json, stdout, stderr)
	case "snapshot":
		return runSnapshot(ctx, api, rest, options.json, stdout, stderr)
	case "restore":
		return runRestore(ctx, api, rest, options.json, stdout, stderr)
	case "clone":
		return runClone(ctx, api, rest, options.json, stdout, stderr)
	case "artifact":
		return runArtifact(ctx, api, rest, options.json, stdout, stderr)
	case "image":
		return runImage(ctx, api, rest, options.json, stdout, stderr)
	case "webhook":
		return runWebhook(ctx, api, rest, options.json, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", command, usage)
		return 2
	}
}

func parseCommon(args []string) (commonOptions, []string, error) {
	options := commonOptions{server: envOr("OPENBOX_SERVER", openbox.DefaultBaseURL), token: os.Getenv("OPENBOX_TOKEN"), timeout: 30 * time.Second}
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		argument := args[i]
		switch {
		case argument == "--json":
			options.json = true
		case argument == "--server" || argument == "--token" || argument == "--timeout":
			if i+1 >= len(args) {
				return options, nil, fmt.Errorf("%s requires a value", argument)
			}
			i++
			if argument == "--server" {
				options.server = args[i]
			} else if argument == "--token" {
				options.token = args[i]
			} else {
				duration, err := time.ParseDuration(args[i])
				if err != nil || duration <= 0 {
					return options, nil, fmt.Errorf("invalid timeout %q", args[i])
				}
				options.timeout = duration
			}
		case strings.HasPrefix(argument, "--server="):
			options.server = strings.TrimPrefix(argument, "--server=")
		case strings.HasPrefix(argument, "--token="):
			options.token = strings.TrimPrefix(argument, "--token=")
		case strings.HasPrefix(argument, "--timeout="):
			duration, err := time.ParseDuration(strings.TrimPrefix(argument, "--timeout="))
			if err != nil || duration <= 0 {
				return options, nil, fmt.Errorf("invalid timeout %q", argument)
			}
			options.timeout = duration
		default:
			remaining = append(remaining, argument)
		}
	}
	return options, remaining, nil
}

func runDoctor(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		return usageError(stderr, "doctor does not accept positional arguments")
	}
	health, err := api.Health(ctx)
	if err != nil {
		return commandError(stderr, err)
	}
	capabilities, err := api.Capabilities(ctx)
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, struct {
			Health       openbox.Health       `json:"health"`
			Capabilities openbox.Capabilities `json:"capabilities"`
		}{Health: health, Capabilities: capabilities})
	}
	fmt.Fprintf(stdout, "OpenBox %s (%s)\n", health.ServerVersion, health.Status)
	fmt.Fprintf(stdout, "Containers:       %s\n", availability(capabilities.Containers))
	fmt.Fprintf(stdout, "Strong isolation: %s\n", availability(capabilities.VirtualMachines))
	if capabilities.VMReason != "" {
		fmt.Fprintf(stdout, "Reason:           %s\n", capabilities.VMReason)
	}
	return 0
}

func runNew(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox new", flag.ContinueOnError)
	flags.SetOutput(stderr)
	kind := flags.String("kind", "vps", "instance kind")
	image := flags.String("image", "ubuntu", "image alias")
	isolation := flags.String("isolation", "", "isolation request: strong|container (omit for server default)")
	vcpus := flags.Int("cpus", 2, "virtual CPUs")
	memory := flags.String("memory", "8GiB", "memory size")
	disk := flags.String("disk", "20GiB", "disk size")
	lifetime := flags.Duration("lifetime", 0, "sandbox TTL from create (default 1h, max 24h)")
	egressProfile := flags.String("egress-profile", "", "system egress profile id")
	publicKey := flags.String("ssh-key", "", "owner SSH public key")
	idempotencyKey := flags.String("idempotency-key", "", "retry-safe request key")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 {
		return usageError(stderr, "usage: openbox new NAME [OPTIONS]")
	}
	switch *isolation {
	case "", "strong", "container":
	default:
		return usageError(stderr, "invalid --isolation: use strong, container, or omit")
	}
	if *lifetime != 0 && *kind != "sandbox" {
		return usageError(stderr, "--lifetime is only valid with --kind sandbox")
	}
	if *lifetime < 0 || *lifetime > 24*time.Hour {
		return usageError(stderr, "--lifetime must be between 1s and 24h")
	}
	if *kind == "sandbox" {
		if *image == "ubuntu" {
			*image = "openbox:sandbox/ubuntu/24.04"
		}
		if *memory == "8GiB" {
			*memory = "2GiB"
		}
		if *disk == "20GiB" {
			*disk = "10GiB"
		}
	}
	memoryBytes, err := parseBytes(*memory)
	if err != nil {
		return usageError(stderr, "invalid --memory: "+err.Error())
	}
	diskBytes, err := parseBytes(*disk)
	if err != nil {
		return usageError(stderr, "invalid --disk: "+err.Error())
	}
	key, err := mutationKey(*idempotencyKey)
	if err != nil {
		return commandError(stderr, err)
	}
	ownerPublicKey, err := resolvePublicKey(*publicKey)
	if err != nil {
		return usageError(stderr, err.Error())
	}
	req := openbox.CreateInstanceRequest{
		Name: positionals[0], Kind: *kind, Image: *image, RequestedIsolation: *isolation,
		Resources:       openbox.Resources{VCPUs: *vcpus, MemoryBytes: memoryBytes, DiskBytes: diskBytes},
		OwnerPublicKey:  ownerPublicKey,
		EgressProfileID: *egressProfile,
	}
	if *lifetime > 0 {
		req.LifetimeSeconds = int(lifetime.Seconds())
		if req.LifetimeSeconds < 1 {
			req.LifetimeSeconds = 1
		}
	}
	result, err := api.CreateInstance(ctx, req, key)
	if err != nil {
		return commandError(stderr, err)
	}
	return printMutation(result, jsonOutput, stdout, stderr)
}

func runList(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		return usageError(stderr, "ls does not accept positional arguments")
	}
	instances, err := api.ListInstances(ctx)
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, struct {
			Instances []openbox.Instance `json:"instances"`
		}{Instances: instances})
	}
	writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tID\tKIND\tSTATE\tISOLATION")
	for _, instance := range instances {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", instance.Name, instance.ID, instance.Kind, strings.ToUpper(instance.ObservedState), instance.ActualIsolation)
	}
	_ = writer.Flush()
	return 0
}

func runInspect(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return usageError(stderr, "usage: openbox inspect ID")
	}
	instance, err := api.GetInstance(ctx, args[0])
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, instance)
	}
	printInstanceStatus(stdout, instance, time.Now().UTC())
	return 0
}

func runLifecycle(ctx context.Context, api *openbox.Client, command string, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	idempotencyKey := flags.String("idempotency-key", "", "retry-safe request key")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 {
		return usageError(stderr, "usage: openbox "+command+" ID")
	}
	key, err := mutationKey(*idempotencyKey)
	if err != nil {
		return commandError(stderr, err)
	}
	var result openbox.MutationResult
	switch command {
	case "start":
		result, err = api.StartInstance(ctx, positionals[0], key)
	case "stop":
		result, err = api.StopInstance(ctx, positionals[0], key)
	case "restart":
		result, err = api.RestartInstance(ctx, positionals[0], key)
	case "rm":
		result, err = api.DeleteInstance(ctx, positionals[0], key)
	}
	if err != nil {
		return commandError(stderr, err)
	}
	return printMutation(result, jsonOutput, stdout, stderr)
}

func runOperation(ctx context.Context, api *openbox.Client, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "watch" {
		return usageError(stderr, "usage: openbox operation watch ID")
	}
	events, errs := api.WatchOperation(ctx, args[1], openbox.WatchOptions{Reconnect: true})
	for event := range events {
		if jsonOutput {
			if code := encodeJSON(stdout, stderr, event); code != 0 {
				return code
			}
		} else {
			fmt.Fprintf(stdout, "[%3d%%] %-10s %s\n", event.Progress, strings.ToUpper(string(event.Status)), event.Stage)
			if event.Message != "" {
				fmt.Fprintf(stdout, "       %s\n", event.Message)
			}
		}
	}
	if err := <-errs; err != nil && !errors.Is(err, context.Canceled) {
		return commandError(stderr, err)
	}
	return 0
}

func printMutation(result openbox.MutationResult, jsonOutput bool, stdout, stderr io.Writer) int {
	if jsonOutput {
		return encodeJSON(stdout, stderr, result)
	}
	if result.Instance != nil {
		fmt.Fprintf(stdout, "Instance %s (%s)\n", result.Instance.Name, result.Instance.ID)
	}
	fmt.Fprintf(stdout, "Operation %s: %s\n", result.Operation.ID, result.Operation.Status)
	return 0
}

func parseInterspersed(flags *flag.FlagSet, args []string) ([]string, error) {
	var flagArgs, positionals []string
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if !strings.HasPrefix(argument, "-") {
			positionals = append(positionals, argument)
			continue
		}
		flagArgs = append(flagArgs, argument)
		if !strings.Contains(argument, "=") {
			name := strings.TrimLeft(argument, "-")
			valueFlag := flags.Lookup(name)
			if valueFlag != nil {
				if _, boolean := valueFlag.Value.(interface{ IsBoolFlag() bool }); !boolean {
					if index+1 >= len(args) {
						return nil, fmt.Errorf("%s requires a value", argument)
					}
					index++
					flagArgs = append(flagArgs, args[index])
				}
			}
		}
	}
	if err := flags.Parse(flagArgs); err != nil {
		return nil, err
	}
	return positionals, nil
}

func mutationKey(value string) (string, error) {
	if value != "" {
		return value, nil
	}
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate idempotency key: %w", err)
	}
	return "cli-" + hex.EncodeToString(buffer), nil
}

func resolvePublicKey(value string) (string, error) {
	if value == "" {
		value = os.Getenv("OPENBOX_SSH_PUBLIC_KEY")
	}
	if value != "" {
		if strings.HasPrefix(value, "ssh-") || strings.HasPrefix(value, "sk-") {
			return strings.TrimSpace(value), nil
		}
		contents, err := os.ReadFile(value)
		if err != nil {
			return "", fmt.Errorf("read SSH public key %q: %w", value, err)
		}
		return strings.TrimSpace(string(contents)), nil
	}
	home, err := os.UserHomeDir()
	if err == nil {
		for _, name := range []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"} {
			contents, readErr := os.ReadFile(filepath.Join(home, ".ssh", name))
			if readErr == nil && strings.TrimSpace(string(contents)) != "" {
				return strings.TrimSpace(string(contents)), nil
			}
		}
	}
	return "", errors.New("no SSH public key found; use --ssh-key PATH or set OPENBOX_SSH_PUBLIC_KEY")
}

func parseBytes(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	units := []struct {
		suffix string
		factor int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"TB", 1_000_000_000_000}, {"GB", 1_000_000_000}, {"MB", 1_000_000}, {"KB", 1_000}, {"B", 1}}
	for _, unit := range units {
		if strings.HasSuffix(strings.ToUpper(trimmed), strings.ToUpper(unit.suffix)) {
			number := strings.TrimSpace(trimmed[:len(trimmed)-len(unit.suffix)])
			parsed, err := strconv.ParseFloat(number, 64)
			if err != nil || parsed <= 0 {
				return 0, fmt.Errorf("invalid byte size %q", value)
			}
			return int64(parsed * float64(unit.factor)), nil
		}
	}
	return 0, fmt.Errorf("byte size %q needs a unit such as GiB", value)
}

func formatBytes(value int64) string {
	for _, unit := range []struct {
		name   string
		factor int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if value >= unit.factor {
			return fmt.Sprintf("%.1f %s", float64(value)/float64(unit.factor), unit.name)
		}
	}
	return fmt.Sprintf("%d B", value)
}

func encodeJSON(stdout, stderr io.Writer, value any) int {
	encoder := json.NewEncoder(stdout)
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintf(stderr, "write JSON: %v\n", err)
		return 1
	}
	return 0
}

func commandError(stderr io.Writer, err error) int {
	printError(stderr, err)
	return 1
}

func printError(stderr io.Writer, err error) {
	var apiErr *openbox.APIError
	if errors.As(err, &apiErr) && apiErr.RequestID != "" {
		fmt.Fprintf(stderr, "error: %v (request %s)\n", err, apiErr.RequestID)
		return
	}
	fmt.Fprintf(stderr, "error: %v\n", err)
}

func usageError(stderr io.Writer, message string) int {
	fmt.Fprintln(stderr, message)
	return 2
}

func availability(value bool) string {
	if value {
		return "available"
	}
	return "unavailable"
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
