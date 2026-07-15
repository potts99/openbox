// SPDX-License-Identifier: AGPL-3.0-only

package pi_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pi "github.com/openbox-dev/openbox/internal/profiles/pi"
)

func TestApplyMaterializesAtomicallyIntoGlobalSettingsOnly(t *testing.T) {
	t.Parallel()
	repo := newMemoryRepo()
	svc, err := pi.New(repo, pi.Options{
		Now:   func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) },
		NewID: func() string { return "profile-1" },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	profile, err := svc.Create(ctx, "owner-1", pi.CreateInput{
		Name: "default",
		Settings: pi.Settings{
			Theme:           "dark",
			DefaultProvider: "anthropic",
			DefaultModel:    "claude-sonnet-4-20250514",
			Packages:        []pi.PackageRef{{Source: "pi-skills"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	guest := &recordingGuest{home: "/home/owner"}
	applier := pi.NewApplier(svc, guest)
	err = applier.Apply(ctx, "owner-1", profile.ID, []pi.InstanceTarget{
		{ID: "inst-a", RuntimeRef: "ref-a"},
		{ID: "inst-b", RuntimeRef: "ref-b"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(guest.writes) != 2 {
		t.Fatalf("writes=%d, want 2", len(guest.writes))
	}
	wantPath := filepath.Join("/home/owner", ".pi", "agent", "settings.json")
	for _, w := range guest.writes {
		if w.Path != wantPath {
			t.Fatalf("path=%q, want %q", w.Path, wantPath)
		}
		if !w.Atomic {
			t.Fatal("write must be atomic")
		}
		if strings.Contains(strings.ToLower(w.Path), "trust") {
			t.Fatal("must not write trust.json")
		}
		settings, err := pi.ParseSettings(w.Content)
		if err != nil {
			t.Fatal(err)
		}
		if settings.Theme != "dark" || settings.DefaultProvider != "anthropic" {
			t.Fatalf("content=%+v", settings)
		}
	}
}

func TestApplyRequiresManagedRuntimeRefs(t *testing.T) {
	t.Parallel()
	repo := newMemoryRepo()
	svc, err := pi.New(repo, pi.Options{NewID: func() string { return "profile-1" }})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := svc.Create(context.Background(), "owner-1", pi.CreateInput{
		Name: "default", Settings: pi.Settings{Theme: "dark"},
	})
	if err != nil {
		t.Fatal(err)
	}
	applier := pi.NewApplier(svc, &recordingGuest{home: "/home/owner"})
	err = applier.Apply(context.Background(), "owner-1", profile.ID, []pi.InstanceTarget{
		{ID: "inst-a", RuntimeRef: ""},
	})
	if err == nil {
		t.Fatal("expected error for empty runtime ref")
	}
}

func TestFileGuestWriterUsesWriteFileThenRename(t *testing.T) {
	t.Parallel()
	var writes []string
	var calls [][]string
	write := func(_ context.Context, ref, path string, content []byte, mode os.FileMode) error {
		if ref != "ref-1" {
			return fmt.Errorf("unexpected ref %q", ref)
		}
		if mode != 0o644 {
			return fmt.Errorf("mode=%o", mode)
		}
		if string(content) != `{"theme":"dark"}` {
			return fmt.Errorf("content=%q", content)
		}
		writes = append(writes, path)
		return nil
	}
	exec := func(_ context.Context, ref string, command []string, stdin []byte) error {
		if ref != "ref-1" {
			return fmt.Errorf("unexpected ref %q", ref)
		}
		if stdin != nil {
			return fmt.Errorf("exec must not carry file content")
		}
		calls = append(calls, append([]string{}, command...))
		return nil
	}
	w := pi.NewFileGuestWriter(write, exec, "/home/owner")
	path := filepath.Join("/home/owner", ".pi", "agent", "settings.json")
	if err := w.WriteAtomic(context.Background(), "ref-1", path, []byte(`{"theme":"dark"}`)); err != nil {
		t.Fatal(err)
	}
	if len(writes) != 1 || !strings.HasSuffix(writes[0], ".tmp") {
		t.Fatalf("writes=%v", writes)
	}
	if len(calls) != 2 {
		t.Fatalf("calls=%d, want 2 (mkdir, mv); %#v", len(calls), calls)
	}
	if calls[0][0] != "mkdir" {
		t.Fatalf("first call=%v, want mkdir", calls[0])
	}
	if calls[1][0] != "mv" {
		t.Fatalf("second call=%v, want mv", calls[1])
	}
}

func TestExecGuestWriterUsesTempThenRename(t *testing.T) {
	t.Parallel()
	var calls [][]string
	exec := func(_ context.Context, ref string, command []string, _ []byte) error {
		if ref != "ref-1" {
			return fmt.Errorf("unexpected ref %q", ref)
		}
		calls = append(calls, append([]string{}, command...))
		return nil
	}
	w := pi.NewExecGuestWriter(exec, "/home/owner")
	path := filepath.Join("/home/owner", ".pi", "agent", "settings.json")
	if err := w.WriteAtomic(context.Background(), "ref-1", path, []byte(`{"theme":"dark"}`)); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls=%d, want 3 (mkdir, write, mv); %#v", len(calls), calls)
	}
	if calls[0][0] != "mkdir" {
		t.Fatalf("first call=%v, want mkdir", calls[0])
	}
	tmpSeen, mvSeen := false, false
	for _, c := range calls {
		for _, arg := range c {
			if strings.HasSuffix(arg, ".tmp") {
				tmpSeen = true
			}
		}
		if c[0] == "mv" {
			mvSeen = true
		}
	}
	if !tmpSeen || !mvSeen {
		t.Fatalf("atomic write missing temp/mv: %#v", calls)
	}
}

type recordingGuest struct {
	mu     sync.Mutex
	home   string
	writes []guestWrite
}

type guestWrite struct {
	Ref     string
	Path    string
	Content []byte
	Atomic  bool
}

func (g *recordingGuest) WriteAtomic(_ context.Context, ref, path string, content []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.writes = append(g.writes, guestWrite{Ref: ref, Path: path, Content: append([]byte(nil), content...), Atomic: true})
	return nil
}

func (g *recordingGuest) HomeDir() string { return g.home }
