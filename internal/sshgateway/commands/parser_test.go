// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/openbox-dev/openbox/internal/domain"
)

func TestParseSupportedCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  Command
	}{
		{name: "new defaults", input: "new my-vm", want: New{
			InstanceName: "my-vm", Kind: domain.KindVPS, Image: "ubuntu",
			Isolation: "",
			Resources: domain.Resources{VCPUs: 2, MemoryBytes: 8 << 30, DiskBytes: 20 << 30},
		}},
		{name: "new options", input: `new box --kind sandbox --image "ubuntu:24.04" --isolation strong --cpus 4 --memory 16GiB --disk=40GB --idempotency-key ssh-123`, want: New{
			InstanceName: "box", Kind: domain.KindSandbox, Image: "ubuntu:24.04",
			Isolation:      domain.IsolationStrong,
			Resources:      domain.Resources{VCPUs: 4, MemoryBytes: 16 << 30, DiskBytes: 40_000_000_000},
			IdempotencyKey: "ssh-123",
		}},
		{name: "list", input: "ls", want: List{}},
		{name: "list json", input: "ls --json", want: List{JSON: true}},
		{name: "inspect", input: "inspect box-1", want: Inspect{Target: "box-1"}},
		{name: "inspect json", input: "inspect --json box-1", want: Inspect{Target: "box-1", JSON: true}},
		{name: "start", input: "start box-1", want: Start{Target: "box-1"}},
		{name: "stop key", input: "stop box-1 --idempotency-key=retry-1", want: Stop{Target: "box-1", IdempotencyKey: "retry-1"}},
		{name: "restart", input: "restart box-1", want: Restart{Target: "box-1"}},
		{name: "copy", input: "cp base-box copy-box", want: Copy{Source: "base-box", Destination: "copy-box"}},
		{name: "copy key", input: "cp base-box copy-box --idempotency-key clone-1", want: Copy{Source: "base-box", Destination: "copy-box", IdempotencyKey: "clone-1"}},
		{name: "remove", input: "rm box-1", want: Remove{Target: "box-1"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(test.input)
			if err != nil {
				t.Fatalf("Parse(%q): %v", test.input, err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Parse(%q) = %#v, want %#v", test.input, got, test.want)
			}
		})
	}
}

func TestParseRejectsUnsafeOrMalformedInput(t *testing.T) {
	t.Parallel()

	tests := []string{
		"", "   ", "shell", "openbox ls", "LS", "ls extra", "inspect", "inspect one two",
		"new", "new Bad_Name", "new box --kind unknown", "new box --cpus 0", "new box --memory 8",
		"new box --disk -1GiB", "new box --memory NaNB", "new box --disk 0.1B", "new box --wat value", "new box --image", "new box --kind=vps --kind=sandbox",
		"start --wat box", "stop box --idempotency-key", "restart box extra", "cp one", "cp one two three", "rm",
		`inspect "unterminated`, `inspect 'unterminated`, `inspect ""`, `inspect box\ name`,
		"ls; id", "ls && id", "inspect $(id)", "inspect `id`", "inspect box|cat", "inspect box>file",
		"inspect box\nrm other", "inspect box\x00other", "inspect box#comment", "new box --image '*.*'",
		"inspect " + strings.Repeat("a", maxCommandBytes),
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			if got, err := Parse(input); err == nil {
				t.Fatalf("Parse(%q) unexpectedly succeeded: %#v", input, got)
			} else if !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("Parse(%q) error = %v, want ErrInvalidCommand", input, err)
			}
		})
	}
}

func FuzzParseNeverTreatsShellSyntaxAsData(f *testing.F) {
	for _, seed := range []string{
		"ls", "new box", "inspect box", "cp one two", "rm box",
		"ls;id", "inspect $(whoami)", "new box\nrm box", "\x00", `inspect "box"`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		command, err := Parse(input)
		if containsUnsafeSyntax(input) {
			if err == nil {
				t.Fatalf("unsafe input %q produced %#v", input, command)
			}
			return
		}
		if err == nil && !isSupported(command) {
			t.Fatalf("input %q produced unsupported type %T", input, command)
		}
	})
}

func isSupported(command Command) bool {
	switch command.(type) {
	case New, List, Inspect, Start, Stop, Restart, Copy, Remove:
		return true
	default:
		return false
	}
}
