// SPDX-License-Identifier: AGPL-3.0-only

package software

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"strings"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// Guest runs argv commands and writes files inside a managed guest instance.
// File content must go through WriteFile (not Exec Stdin).
type Guest interface {
	Exec(context.Context, runtimeapi.ExecRequest) (runtimeapi.ExecResult, error)
	WriteFile(context.Context, runtimeapi.WriteFileRequest) error
}

// GuestExecer runs argv commands inside a managed guest instance.
// Deprecated: prefer Guest; kept as a name alias for call sites that only Exec.
type GuestExecer = Guest

// InstallOptions configures host-side release fetch and guest architecture.
type InstallOptions struct {
	Architecture string // "x86_64" or "aarch64" for github-release packages
	Fetcher      ReleaseFetcher
}

// Install runs a catalog package's install recipe then verify steps via guest
// Exec. github-release packages are fetched on the host, digest-verified, and
// written into the guest through WriteFile. Other packages use argv-only steps.
func Install(ctx context.Context, guest Guest, runtimeRef string, pkg Package, opts InstallOptions) error {
	if guest == nil {
		return fmt.Errorf("software install: guest is required")
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
			req := runtimeapi.ExecRequest{
				Ref:        runtimeRef,
				Command:    step,
				WorkingDir: "/",
				Env:        env,
			}
			result, err := guest.Exec(ctx, req)
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

	if pin, ok := githubReleasePin(pkg); ok {
		if opts.Architecture == "" {
			return fmt.Errorf("software install %q: architecture is required", pkg.ID)
		}
		asset, err := assetForArch(pin, opts.Architecture)
		if err != nil {
			return fmt.Errorf("software install %q: %w", pkg.ID, err)
		}
		fetcher := opts.Fetcher
		if fetcher == nil {
			fetcher = defaultReleaseFetcher
		}
		url := ReleaseURL(pin.Name, pin.Version, asset.Filename)
		body, err := fetcher.Fetch(ctx, url)
		if err != nil {
			return fmt.Errorf("software install %q: fetch: %w", pkg.ID, err)
		}
		if err := verifySHA256(body, asset.SHA256); err != nil {
			return fmt.Errorf("software install %q: %w", pkg.ID, err)
		}
		dest := path.Join("/usr/local/bin", pkg.ID)
		tmp := dest + ".openbox-tmp"
		if err := guest.WriteFile(ctx, runtimeapi.WriteFileRequest{
			Ref:  runtimeRef,
			Path: tmp,
			Body: bytes.NewReader(body),
			Mode: 0o755,
			UID:  0,
			GID:  0,
		}); err != nil {
			return fmt.Errorf("software install %q write %s: %w", pkg.ID, tmp, err)
		}
		if err := run("install", [][]string{{"mv", tmp, dest}}); err != nil {
			return err
		}
		return run("verify", pkg.Verify)
	}

	if err := run("install", pkg.Install); err != nil {
		return err
	}
	return run("verify", pkg.Verify)
}
