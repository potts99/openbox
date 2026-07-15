// SPDX-License-Identifier: AGPL-3.0-only

// Package pi owns Launch Pi argv contracts and Pi-enabled instance policy.
package pi

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/terminal"
)

// SessionName is the fixed tmux session used by Launch Pi.
const SessionName = "pi"

// AttachOrCreateCommand returns the guest argv that starts or attaches Pi in a
// named tmux session. Closing the browser detaches tmux; it does not kill Pi.
//
//	tmux new-session -A -s pi [-c <workdir>] -- pi
func AttachOrCreateCommand(workdir string) ([]string, error) {
	workdir = strings.TrimSpace(workdir)
	cmd := []string{"tmux", "new-session", "-A", "-s", SessionName}
	if workdir != "" {
		if err := validateWorkdir(workdir); err != nil {
			return nil, err
		}
		cmd = append(cmd, "-c", workdir)
	}
	cmd = append(cmd, "--", "pi")
	return cmd, nil
}

// LaunchAvailable reports whether the dashboard may show Launch Pi.
// VPS/sandbox instances that include Pi qualify.
func LaunchAvailable(kind domain.InstanceKind, includesPi bool) bool {
	if !includesPi {
		return false
	}
	switch kind {
	case domain.KindVPS, domain.KindSandbox:
		return true
	default:
		return false
	}
}

// PersistentGuestPaths lists Devbox filesystem locations that retain Pi state
// across instance stop/start (sessions and local unsupported product logins).
func PersistentGuestPaths(home string) []string {
	agent := filepath.Join(home, ".pi", "agent")
	return []string{
		filepath.Join(agent, "sessions"),
		filepath.Join(agent, "auth"),
		filepath.Join(agent, "settings.json"),
	}
}

// IsLaunchSession reports whether sessionName is the Launch Pi tmux session.
func IsLaunchSession(sessionName string) bool {
	return strings.TrimSpace(sessionName) == SessionName
}

func validateWorkdir(workdir string) error {
	if !filepath.IsAbs(workdir) {
		return fmt.Errorf("%w: working_directory must be absolute", terminal.ErrInvalidFrame)
	}
	clean := filepath.Clean(workdir)
	if clean != workdir || strings.Contains(workdir, "..") {
		return fmt.Errorf("%w: working_directory must be a clean absolute path", terminal.ErrInvalidFrame)
	}
	if strings.IndexByte(workdir, 0) >= 0 {
		return fmt.Errorf("%w: working_directory contains NUL", terminal.ErrInvalidFrame)
	}
	return nil
}
