// SPDX-License-Identifier: AGPL-3.0-only

package terminal_test

import (
	"slices"
	"testing"

	"github.com/openbox-dev/openbox/internal/terminal"
)

func TestCommandForSessionGenericShellIndependentOfTmux(t *testing.T) {
	for _, name := range []string{"", "  ", "\t"} {
		cmd, err := terminal.CommandForSession(name)
		if err != nil {
			t.Fatalf("sessionName=%q: unexpected err: %v", name, err)
		}
		if slices.Contains(cmd, "tmux") {
			t.Fatalf("sessionName=%q: command %v must not invoke tmux", name, cmd)
		}
		if len(cmd) == 0 {
			t.Fatalf("sessionName=%q: empty command", name)
		}
	}
}

func TestCommandForSessionNamedUsesTmuxAttachOrCreate(t *testing.T) {
	cmd, err := terminal.CommandForSession("pi")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tmux", "new-session", "-A", "-s", "pi"}
	if !slices.Equal(cmd, want) {
		t.Fatalf("command=%v want %v", cmd, want)
	}
}

func TestCommandForSessionRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{
		"has:colon",
		"has.dot",
		"has space",
		"bad/name",
		string([]byte{'a', 0, 'b'}),
		"x" + string(rune(0x7f)),
	} {
		_, err := terminal.CommandForSession(name)
		if err == nil {
			t.Fatalf("sessionName=%q: want error", name)
		}
	}
}

func TestValidSessionName(t *testing.T) {
	for _, name := range []string{"pi", "main", "dev-1", "Work_Bench", "a"} {
		if !terminal.ValidSessionName(name) {
			t.Fatalf("%q should be valid", name)
		}
	}
	for _, name := range []string{"", " ", "a.b", "a:b", "a b", "../x"} {
		if terminal.ValidSessionName(name) {
			t.Fatalf("%q should be invalid", name)
		}
	}
}
