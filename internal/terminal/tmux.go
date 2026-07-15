// SPDX-License-Identifier: AGPL-3.0-only

package terminal

import (
	"fmt"
	"strings"
	"unicode"
)

// DefaultShell is the interactive shell argv used when no persistent
// session_name is requested. Generic terminals must not invoke tmux.
var DefaultShell = []string{"/bin/bash"}

// MaxSessionNameLen caps persistent tmux session names.
const MaxSessionNameLen = 64

// ValidSessionName reports whether name is safe for tmux -s / target-session.
// Empty names are invalid here; callers treat empty as "no named session".
// Rejected characters include tmux metacharacters (':' and '.') and anything
// outside [A-Za-z0-9_-].
func ValidSessionName(name string) bool {
	if name == "" || len(name) > MaxSessionNameLen {
		return false
	}
	for _, r := range name {
		if r > unicode.MaxASCII || (!unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_') {
			return false
		}
	}
	return true
}

// CommandForSession returns the guest console argv for a browser terminal open.
//
// When sessionName is empty (after trim), the result is DefaultShell and does
// not mention tmux — ordinary unnamed shells stay independent of tmux.
//
// When sessionName is set, the result is:
//
//	tmux new-session -A -s <sessionName>
//
// The -A flag attaches to an existing session of that name, or creates it.
// Invalid non-empty names return an error; callers should surface a protocol
// error rather than falling back to a host shell.
func CommandForSession(sessionName string) ([]string, error) {
	name := strings.TrimSpace(sessionName)
	if name == "" {
		return append([]string(nil), DefaultShell...), nil
	}
	if !ValidSessionName(name) {
		return nil, fmt.Errorf("%w: invalid session_name", ErrInvalidFrame)
	}
	return []string{"tmux", "new-session", "-A", "-s", name}, nil
}
