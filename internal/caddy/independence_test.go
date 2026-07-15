// SPDX-License-Identifier: AGPL-3.0-only

package caddy_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/openbox-dev/openbox/internal/caddy"
)

// TestGatewayApplyFailureDoesNotRequireInstanceLifecycle proves the HTTPS
// gateway apply path has no instance stop/start/delete coupling: a validate or
// reload failure only rolls back gateway config files.
func TestGatewayApplyFailureDoesNotRequireInstanceLifecycle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "routes.caddyfile")
	if err := os.WriteFile(path, []byte("# previous\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	instanceTouched := false
	err := caddy.Apply([]byte("# next\n"), caddy.ApplyOptions{
		ConfigPath: path,
		Validate: func(string) error {
			return errors.New("caddy validate failed")
		},
		Reload: func() error {
			instanceTouched = true
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected validate failure")
	}
	if instanceTouched {
		t.Fatal("reload/instance path must not run when validate fails")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "# previous\n" {
		t.Fatalf("active config changed on validate failure: %q", got)
	}
}

func TestGatewayReloadFailureRestoresConfigWithoutInstanceHooks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "routes.caddyfile")
	if err := os.WriteFile(path, []byte("# previous\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reloads := 0
	err := caddy.Apply([]byte("# next\n"), caddy.ApplyOptions{
		ConfigPath: path,
		Validate:   func(string) error { return nil },
		Reload: func() error {
			reloads++
			if reloads == 1 {
				return errors.New("reload failed")
			}
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected reload failure")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "# previous\n" {
		t.Fatalf("reload failure must restore previous config: %q", got)
	}
	if reloads != 2 {
		t.Fatalf("reloads=%d, want validate-ok reload + restore reload", reloads)
	}
}
