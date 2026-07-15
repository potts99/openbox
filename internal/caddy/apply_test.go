// SPDX-License-Identifier: AGPL-3.0-only

package caddy_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/openbox-dev/openbox/internal/caddy"
)

func TestApplyWritesConfigAtomically(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(path, []byte("old config\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var validated atomic.Bool
	var reloaded atomic.Bool
	err := caddy.Apply([]byte("new config\n"), caddy.ApplyOptions{
		ConfigPath: path,
		Validate: func(configPath string) error {
			validated.Store(true)
			data, err := os.ReadFile(configPath)
			if err != nil {
				return err
			}
			if string(data) != "new config\n" {
				t.Fatalf("validate saw %q", data)
			}
			// Active path must still be old until rename succeeds.
			active, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if string(active) != "old config\n" {
				t.Fatalf("active mutated before validate finished: %q", active)
			}
			return nil
		},
		Reload: func() error {
			reloaded.Store(true)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !validated.Load() || !reloaded.Load() {
		t.Fatalf("validated=%v reloaded=%v", validated.Load(), reloaded.Load())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new config\n" {
		t.Fatalf("active config = %q", got)
	}
}

func TestApplyRollsBackWhenValidateFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(path, []byte("old config\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := caddy.Apply([]byte("bad config\n"), caddy.ApplyOptions{
		ConfigPath: path,
		Validate:   func(string) error { return errors.New("caddy validate failed") },
		Reload: func() error {
			t.Fatal("reload must not run after validate failure")
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected validate error")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "old config\n" {
		t.Fatalf("active config changed on validate failure: %q", got)
	}
}

func TestApplyRollsBackWhenReloadFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(path, []byte("old config\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var reloadCalls atomic.Int32
	err := caddy.Apply([]byte("new config\n"), caddy.ApplyOptions{
		ConfigPath: path,
		Validate:   func(string) error { return nil },
		Reload: func() error {
			n := reloadCalls.Add(1)
			if n == 1 {
				return errors.New("caddy reload failed")
			}
			// Second call is the post-rollback reload of the previous config.
			active, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if string(active) != "old config\n" {
				t.Fatalf("rollback reload saw %q, want old config", active)
			}
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected reload error")
	}
	if reloadCalls.Load() < 2 {
		t.Fatalf("reload calls=%d, want >=2 (failed apply + rollback)", reloadCalls.Load())
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "old config\n" {
		t.Fatalf("active config after rollback = %q", got)
	}
}

func TestApplyCreatesConfigWhenMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")

	err := caddy.Apply([]byte("first config\n"), caddy.ApplyOptions{
		ConfigPath: path,
		Validate:   func(string) error { return nil },
		Reload:     func() error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first config\n" {
		t.Fatalf("got %q", got)
	}
}
