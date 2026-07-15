// SPDX-License-Identifier: AGPL-3.0-only

package pi

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/openbox-dev/openbox/internal/domain"
)

// InstanceTarget is one managed instance that should receive a profile apply.
type InstanceTarget struct {
	ID         domain.InstanceID
	RuntimeRef string
}

// GuestWriter writes files into a guest filesystem via the runtime boundary.
type GuestWriter interface {
	HomeDir() string
	WriteAtomic(ctx context.Context, runtimeRef, path string, content []byte) error
}

// ProfileSource loads the current profile settings for apply.
type ProfileSource interface {
	Get(ctx context.Context, ownerID domain.OwnerID, id domain.PiProfileID) (domain.PiProfile, error)
}

// Applier materializes a shared Pi profile into selected instances.
type Applier struct {
	profiles ProfileSource
	guest    GuestWriter
}

// NewApplier constructs an Applier.
func NewApplier(profiles ProfileSource, guest GuestWriter) *Applier {
	return &Applier{profiles: profiles, guest: guest}
}

// Apply writes the profile's settings.json atomically into each instance's
// global Pi agent directory. It never writes trust.json or project .pi files.
func (a *Applier) Apply(ctx context.Context, ownerID domain.OwnerID, profileID domain.PiProfileID, targets []InstanceTarget) error {
	if a == nil || a.profiles == nil || a.guest == nil {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "applier"}
	}
	if len(targets) == 0 {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "targets"}
	}
	profile, err := a.profiles.Get(ctx, ownerID, profileID)
	if err != nil {
		return err
	}
	if _, err := ParseSettings(profile.SettingsJSON); err != nil {
		return err
	}
	dest := MaterializeTargets(a.guest.HomeDir(), "").GlobalSettings
	for _, target := range targets {
		if strings.TrimSpace(target.RuntimeRef) == "" {
			return &domain.Error{Code: domain.CodeInvalidArgument, Field: "runtime_ref"}
		}
		if err := a.guest.WriteAtomic(ctx, target.RuntimeRef, dest, append([]byte(nil), profile.SettingsJSON...)); err != nil {
			return fmt.Errorf("apply profile to %s: %w", target.ID, err)
		}
	}
	return nil
}

// ExecFunc runs a command inside a guest instance, optionally with stdin.
type ExecFunc func(ctx context.Context, runtimeRef string, command []string, stdin []byte) error

// ExecGuestWriter materializes files with mkdir + temp write + rename via exec.
type ExecGuestWriter struct {
	exec ExecFunc
	home string
}

// NewExecGuestWriter returns a GuestWriter backed by guest exec.
func NewExecGuestWriter(exec ExecFunc, home string) *ExecGuestWriter {
	return &ExecGuestWriter{exec: exec, home: home}
}

// HomeDir returns the guest home used for Pi paths.
func (w *ExecGuestWriter) HomeDir() string { return w.home }

// WriteAtomic writes content to path via a sibling .tmp file then rename.
func (w *ExecGuestWriter) WriteAtomic(ctx context.Context, runtimeRef, path string, content []byte) error {
	if w == nil || w.exec == nil {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "guest_writer"}
	}
	if runtimeRef == "" || path == "" {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "path"}
	}
	dir := filepath.Dir(path)
	tmp := path + ".tmp"
	if err := w.exec(ctx, runtimeRef, []string{"mkdir", "-p", dir}, nil); err != nil {
		return err
	}
	if err := w.exec(ctx, runtimeRef, []string{"sh", "-c", "cat > \"$1\"", "openbox-pi-write", tmp}, content); err != nil {
		return err
	}
	if err := w.exec(ctx, runtimeRef, []string{"mv", "-f", tmp, path}, nil); err != nil {
		return err
	}
	return nil
}
