// SPDX-License-Identifier: AGPL-3.0-only

package pi_test

import (
	"strings"
	"testing"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/pi"
)

func TestAttachOrCreateCommandStartOrAttachInWorkdir(t *testing.T) {
	t.Parallel()
	cmd, err := pi.AttachOrCreateCommand("/home/owner/src")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tmux", "new-session", "-A", "-s", pi.SessionName, "-c", "/home/owner/src", "--", "pi"}
	if strings.Join(cmd, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v want %v", cmd, want)
	}
}

func TestAttachOrCreateCommandDefaultWorkdir(t *testing.T) {
	t.Parallel()
	cmd, err := pi.AttachOrCreateCommand("")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tmux", "new-session", "-A", "-s", pi.SessionName, "--", "pi"}
	if strings.Join(cmd, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v want %v", cmd, want)
	}
}

func TestAttachOrCreateCommandRejectsUnsafeWorkdir(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{"relative", "../etc", "/home/../etc", "\x00"} {
		if _, err := pi.AttachOrCreateCommand(dir); err == nil {
			t.Fatalf("expected error for %q", dir)
		}
	}
}

func TestLaunchAvailable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind domain.InstanceKind
		pi   bool
		want bool
	}{
		{domain.KindDevbox, true, true},
		{domain.KindSandbox, true, true},
		{domain.KindSandbox, false, false},
		{domain.KindVPS, false, false},
		{domain.KindVPS, true, false}, // plain VPS stays clean even if mis-flagged
	}
	for _, tc := range cases {
		if got := pi.LaunchAvailable(tc.kind, tc.pi); got != tc.want {
			t.Fatalf("kind=%s pi=%v: got %v want %v", tc.kind, tc.pi, got, tc.want)
		}
	}
}

func TestPersistentGuestPathsStayOnDevboxFilesystem(t *testing.T) {
	t.Parallel()
	paths := pi.PersistentGuestPaths("/home/owner")
	for _, p := range paths {
		if strings.HasPrefix(p, "/tmp") || strings.Contains(p, "/run/") {
			t.Fatalf("persistent path must survive stop/start, got %q", p)
		}
		if !strings.HasPrefix(p, "/home/owner/.pi") {
			t.Fatalf("expected under ~/.pi, got %q", p)
		}
	}
}
