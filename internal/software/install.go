// SPDX-License-Identifier: AGPL-3.0-only

package software

import (
	"context"
	"fmt"
	"strings"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// GuestExecer runs argv commands inside a managed guest instance.
type GuestExecer interface {
	Exec(context.Context, runtimeapi.ExecRequest) (runtimeapi.ExecResult, error)
}

// Install runs a catalog package's install recipe then verify steps via guest
// Exec. Steps are argv-only; non-zero exit codes fail the install.
func Install(ctx context.Context, execer GuestExecer, runtimeRef string, pkg Package) error {
	if execer == nil {
		return fmt.Errorf("software install: execer is required")
	}
	if runtimeRef == "" {
		return fmt.Errorf("software install: runtime ref is required")
	}
	if err := pkg.Validate(); err != nil {
		return fmt.Errorf("software install: %w", err)
	}
	env := map[string]string{
		"DEBIAN_FRONTEND": "noninteractive",
		"PATH":            "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	run := func(phase string, steps [][]string) error {
		for i, step := range steps {
			result, err := execer.Exec(ctx, runtimeapi.ExecRequest{
				Ref:        runtimeRef,
				Command:    step,
				WorkingDir: "/",
				Env:        env,
			})
			if err != nil {
				return fmt.Errorf("software %s %q step %d (%s): %w", phase, pkg.ID, i, strings.Join(step, " "), err)
			}
			if result.ExitCode != 0 {
				detail := strings.TrimSpace(string(result.Stderr))
				if detail == "" {
					detail = strings.TrimSpace(string(result.Stdout))
				}
				if detail == "" {
					detail = fmt.Sprintf("exit %d", result.ExitCode)
				}
				return fmt.Errorf("software %s %q step %d (%s): %s", phase, pkg.ID, i, strings.Join(step, " "), detail)
			}
		}
		return nil
	}
	if err := run("install", pkg.Install); err != nil {
		return err
	}
	if err := run("verify", pkg.Verify); err != nil {
		return err
	}
	return nil
}
