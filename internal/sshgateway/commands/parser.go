// SPDX-License-Identifier: AGPL-3.0-only

// Package commands parses the deliberately small OpenBox SSH command language.
// It is not, and must never become, a shell parser.
package commands

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/openbox-dev/openbox/internal/domain"
)

const maxCommandBytes = 4096

var (
	ErrInvalidCommand = errors.New("invalid SSH command")
	referencePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,127}$`)
	imagePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,255}$`)
	keyPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

// Command is one of the allow-listed management requests. The unexported
// marker prevents other packages from extending the set accidentally.
type Command interface{ managementCommand() }

type New struct {
	InstanceName   string
	Kind           domain.InstanceKind
	Image          string
	Isolation      domain.IsolationRequest
	Resources      domain.Resources
	IdempotencyKey string
	JSON           bool
}

type List struct{ JSON bool }
type Inspect struct {
	Target string
	JSON   bool
}
type Start struct {
	Target         string
	IdempotencyKey string
	JSON           bool
}
type Stop struct {
	Target         string
	IdempotencyKey string
	JSON           bool
}
type Restart struct {
	Target         string
	IdempotencyKey string
	JSON           bool
}
type Copy struct {
	Source         string
	Destination    string
	IdempotencyKey string
	JSON           bool
}
type Remove struct {
	Target         string
	IdempotencyKey string
	JSON           bool
}

func (New) managementCommand()     {}
func (List) managementCommand()    {}
func (Inspect) managementCommand() {}
func (Start) managementCommand()   {}
func (Stop) managementCommand()    {}
func (Restart) managementCommand() {}
func (Copy) managementCommand()    {}
func (Remove) managementCommand()  {}

// Parse converts an SSH exec request into a typed management command. It
// rejects shell syntax before tokenization and performs no expansion,
// substitution, globbing, environment lookup, or command execution.
func Parse(input string) (Command, error) {
	if len(input) > maxCommandBytes {
		return nil, invalid("command is too long")
	}
	if containsUnsafeSyntax(input) {
		return nil, invalid("unsafe syntax")
	}
	words, err := tokenize(input)
	if err != nil {
		return nil, err
	}
	if len(words) == 0 {
		return nil, invalid("command is required")
	}

	switch words[0] {
	case "new":
		return parseNew(words[1:])
	case "ls":
		return parseList(words[1:])
	case "inspect":
		return parseInspect(words[1:])
	case "start":
		return parseLifecycle(words[1:], func(target, key string, json bool) Command {
			return Start{Target: target, IdempotencyKey: key, JSON: json}
		})
	case "stop":
		return parseLifecycle(words[1:], func(target, key string, json bool) Command {
			return Stop{Target: target, IdempotencyKey: key, JSON: json}
		})
	case "restart":
		return parseLifecycle(words[1:], func(target, key string, json bool) Command {
			return Restart{Target: target, IdempotencyKey: key, JSON: json}
		})
	case "cp":
		return parseCopy(words[1:])
	case "rm":
		return parseLifecycle(words[1:], func(target, key string, json bool) Command {
			return Remove{Target: target, IdempotencyKey: key, JSON: json}
		})
	default:
		return nil, invalid("unknown command")
	}
}

func parseNew(args []string) (Command, error) {
	values, positional, jsonOutput, err := parseOptions(args, map[string]bool{
		"kind": true, "image": true, "isolation": true, "cpus": true,
		"memory": true, "disk": true, "idempotency-key": true, "json": false,
	})
	if err != nil {
		return nil, err
	}
	if len(positional) != 1 || domain.ValidateInstanceName(positional[0]) != nil {
		return nil, invalid("new requires one valid instance name")
	}

	command := New{
		InstanceName: positional[0], Kind: domain.KindVPS, Image: "ubuntu",
		Isolation: "", // server resolves strong vs container from capabilities
		Resources: domain.Resources{VCPUs: 2, MemoryBytes: 8 << 30, DiskBytes: 20 << 30},
		JSON:      jsonOutput,
	}
	if value, ok := values["kind"]; ok {
		command.Kind = domain.InstanceKind(value)
	}
	switch command.Kind {
	case domain.KindSandbox, domain.KindVPS:
	default:
		return nil, invalid("invalid --kind")
	}
	if value, ok := values["image"]; ok {
		if !imagePattern.MatchString(value) {
			return nil, invalid("invalid --image")
		}
		command.Image = value
	}
	if value, ok := values["isolation"]; ok {
		command.Isolation = domain.IsolationRequest(value)
	}
	switch command.Isolation {
	case "", domain.IsolationStrong, domain.IsolationContainerReq:
	default:
		return nil, invalid("invalid --isolation")
	}
	if value, ok := values["cpus"]; ok {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed <= 0 {
			return nil, invalid("invalid --cpus")
		}
		command.Resources.VCPUs = parsed
	}
	if value, ok := values["memory"]; ok {
		command.Resources.MemoryBytes, err = parseBytes(value)
		if err != nil {
			return nil, invalid("invalid --memory")
		}
	}
	if value, ok := values["disk"]; ok {
		command.Resources.DiskBytes, err = parseBytes(value)
		if err != nil {
			return nil, invalid("invalid --disk")
		}
	}
	if value, ok := values["idempotency-key"]; ok {
		if !keyPattern.MatchString(value) {
			return nil, invalid("invalid --idempotency-key")
		}
		command.IdempotencyKey = value
	}
	return command, nil
}

func parseList(args []string) (Command, error) {
	_, positional, jsonOutput, err := parseOptions(args, map[string]bool{"json": false})
	if err != nil {
		return nil, err
	}
	if len(positional) != 0 {
		return nil, invalid("ls accepts no arguments")
	}
	return List{JSON: jsonOutput}, nil
}

func parseInspect(args []string) (Command, error) {
	_, positional, jsonOutput, err := parseOptions(args, map[string]bool{"json": false})
	if err != nil {
		return nil, err
	}
	if len(positional) != 1 || !referencePattern.MatchString(positional[0]) {
		return nil, invalid("inspect requires one valid target")
	}
	return Inspect{Target: positional[0], JSON: jsonOutput}, nil
}

func parseLifecycle(args []string, build func(string, string, bool) Command) (Command, error) {
	values, positional, jsonOutput, err := parseOptions(args, map[string]bool{"idempotency-key": true, "json": false})
	if err != nil {
		return nil, err
	}
	if len(positional) != 1 || !referencePattern.MatchString(positional[0]) {
		return nil, invalid("command requires one valid target")
	}
	key := values["idempotency-key"]
	if key != "" && !keyPattern.MatchString(key) {
		return nil, invalid("invalid --idempotency-key")
	}
	return build(positional[0], key, jsonOutput), nil
}

func parseCopy(args []string) (Command, error) {
	values, positional, jsonOutput, err := parseOptions(args, map[string]bool{"idempotency-key": true, "json": false})
	if err != nil {
		return nil, err
	}
	if len(positional) != 2 || !referencePattern.MatchString(positional[0]) || domain.ValidateInstanceName(positional[1]) != nil {
		return nil, invalid("cp requires a valid source and destination name")
	}
	key := values["idempotency-key"]
	if key != "" && !keyPattern.MatchString(key) {
		return nil, invalid("invalid --idempotency-key")
	}
	return Copy{Source: positional[0], Destination: positional[1], IdempotencyKey: key, JSON: jsonOutput}, nil
}

// parseOptions recognizes only the options explicitly passed in allowed. A
// true value means that the option consumes one value. Options may be
// interspersed with positional arguments, matching the native CLI.
func parseOptions(args []string, allowed map[string]bool) (map[string]string, []string, bool, error) {
	values := make(map[string]string)
	positionals := make([]string, 0, len(args))
	jsonOutput := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if !strings.HasPrefix(argument, "-") {
			positionals = append(positionals, argument)
			continue
		}
		if !strings.HasPrefix(argument, "--") || argument == "--" {
			return nil, nil, false, invalid("unknown option")
		}
		nameValue := strings.TrimPrefix(argument, "--")
		name, value, hasEquals := strings.Cut(nameValue, "=")
		requiresValue, ok := allowed[name]
		if !ok {
			return nil, nil, false, invalid("unknown option")
		}
		if _, duplicate := values[name]; duplicate || (name == "json" && jsonOutput) {
			return nil, nil, false, invalid("duplicate option")
		}
		if !requiresValue {
			if hasEquals {
				return nil, nil, false, invalid("flag does not take a value")
			}
			jsonOutput = true
			continue
		}
		if !hasEquals {
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
				return nil, nil, false, invalid("option requires a value")
			}
			index++
			value = args[index]
		}
		if value == "" {
			return nil, nil, false, invalid("option requires a value")
		}
		values[name] = value
	}
	return values, positionals, jsonOutput, nil
}

func tokenize(input string) ([]string, error) {
	var words []string
	for index := 0; index < len(input); {
		for index < len(input) && (input[index] == ' ' || input[index] == '\t') {
			index++
		}
		if index == len(input) {
			break
		}
		start := index
		if input[index] == '\'' || input[index] == '"' {
			quote := input[index]
			index++
			start = index
			for index < len(input) && input[index] != quote {
				index++
			}
			if index == len(input) || index == start {
				return nil, invalid("malformed quoting")
			}
			words = append(words, input[start:index])
			index++
			if index < len(input) && input[index] != ' ' && input[index] != '\t' {
				return nil, invalid("quoted argument must be separate")
			}
			continue
		}
		for index < len(input) && input[index] != ' ' && input[index] != '\t' {
			if input[index] == '\'' || input[index] == '"' {
				return nil, invalid("quote must begin an argument")
			}
			index++
		}
		words = append(words, input[start:index])
	}
	return words, nil
}

func containsUnsafeSyntax(input string) bool {
	for _, character := range input {
		if unicode.IsControl(character) && character != '\t' {
			return true
		}
		switch character {
		case '\\', ';', '&', '|', '<', '>', '$', '`', '(', ')', '{', '}', '[', ']', '*', '?', '~', '!', '#':
			return true
		}
	}
	return false
}

func parseBytes(value string) (int64, error) {
	units := []struct {
		suffix string
		factor float64
	}{
		{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
		{"TB", 1_000_000_000_000}, {"GB", 1_000_000_000}, {"MB", 1_000_000}, {"KB", 1_000}, {"B", 1},
	}
	for _, unit := range units {
		if len(value) <= len(unit.suffix) || !strings.EqualFold(value[len(value)-len(unit.suffix):], unit.suffix) {
			continue
		}
		number := value[:len(value)-len(unit.suffix)]
		parsed, err := strconv.ParseFloat(number, 64)
		bytes := parsed * unit.factor
		if err != nil || parsed <= 0 || math.IsNaN(bytes) || math.IsInf(bytes, 0) || bytes < 1 || bytes > math.MaxInt64 {
			return 0, errors.New("invalid byte size")
		}
		return int64(bytes), nil
	}
	return 0, errors.New("byte size requires a unit")
}

func invalid(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidCommand, reason)
}
