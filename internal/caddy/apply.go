// SPDX-License-Identifier: AGPL-3.0-only

package caddy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ApplyOptions configures atomic config install for the HTTPS gateway.
type ApplyOptions struct {
	// ConfigPath is the active Caddyfile (or JSON) path on disk.
	ConfigPath string
	// Validate checks a candidate config path before it replaces the active file.
	// Nil means skip validation (tests may supply a fake).
	Validate func(configPath string) error
	// Reload activates the new config in the running Caddy process.
	// Nil means skip reload (tests may supply a fake).
	Reload func() error
}

// Apply writes config atomically: temp → validate → rename → reload.
// On validate or reload failure the previous active config is restored when
// one existed. Reload is attempted again after a successful rollback so the
// running process matches the restored file.
func Apply(config []byte, opts ApplyOptions) error {
	if strings.TrimSpace(opts.ConfigPath) == "" {
		return fmt.Errorf("config path is required")
	}
	dir := filepath.Dir(opts.ConfigPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	previous, hadPrevious, err := readOptional(opts.ConfigPath)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".caddyfile-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(config); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return fmt.Errorf("chmod temp config: %w", err)
	}

	if opts.Validate != nil {
		if err := opts.Validate(tmpPath); err != nil {
			return fmt.Errorf("validate config: %w", err)
		}
	}

	if err := os.Rename(tmpPath, opts.ConfigPath); err != nil {
		return fmt.Errorf("install config: %w", err)
	}

	if opts.Reload != nil {
		if err := opts.Reload(); err != nil {
			if hadPrevious {
				if restoreErr := os.WriteFile(opts.ConfigPath, previous, 0o644); restoreErr != nil {
					return fmt.Errorf("reload failed (%v); restore also failed: %w", err, restoreErr)
				}
				if reloadErr := opts.Reload(); reloadErr != nil {
					return fmt.Errorf("reload failed (%v); rollback reload failed: %w", err, reloadErr)
				}
			} else {
				_ = os.Remove(opts.ConfigPath)
			}
			return fmt.Errorf("reload config: %w", err)
		}
	}
	return nil
}

func readOptional(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read existing config: %w", err)
	}
	return data, true, nil
}
