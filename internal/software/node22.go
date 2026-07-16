// SPDX-License-Identifier: AGPL-3.0-only

package software

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"strings"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

//go:embed node22/nodesource.gpg
var nodeSourceGPG []byte

const (
	nodeSourceKeyPath     = "/etc/apt/keyrings/nodesource.gpg"
	nodeSourceSourcesPath = "/etc/apt/sources.list.d/nodesource.sources"
)

// installNodeJS configures the pinned NodeSource Node 22 apt repo and installs
// nodejs. Repo metadata is written via WriteFile so guest recipes never curl.
func installNodeJS(ctx context.Context, guest Guest, runtimeRef, debArch, version string) error {
	if guest == nil {
		return fmt.Errorf("nodejs install: guest is required")
	}
	if runtimeRef == "" {
		return fmt.Errorf("nodejs install: runtime ref is required")
	}
	if debArch == "" || version == "" {
		return fmt.Errorf("nodejs install: deb architecture and version are required")
	}
	env := map[string]string{
		"DEBIAN_FRONTEND": "noninteractive",
		"PATH":            "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	run := func(step []string) error {
		result, err := guest.Exec(ctx, runtimeapi.ExecRequest{
			Ref:        runtimeRef,
			Command:    step,
			WorkingDir: "/",
			Env:        env,
		})
		if err != nil {
			return fmt.Errorf("nodejs install (%s): %w", strings.Join(step, " "), err)
		}
		if result.ExitCode != 0 {
			detail := strings.TrimSpace(string(result.Stderr))
			if detail == "" {
				detail = strings.TrimSpace(string(result.Stdout))
			}
			if detail == "" {
				detail = fmt.Sprintf("exit %d", result.ExitCode)
			}
			return fmt.Errorf("nodejs install (%s): %s", strings.Join(step, " "), detail)
		}
		return nil
	}

	if err := run([]string{"apt-get", "update"}); err != nil {
		return err
	}
	if err := run([]string{"apt-get", "install", "-y", "ca-certificates", "gnupg", "apt-transport-https"}); err != nil {
		return err
	}
	if err := guest.WriteFile(ctx, runtimeapi.WriteFileRequest{
		Ref:  runtimeRef,
		Path: nodeSourceKeyPath,
		Body: bytes.NewReader(nodeSourceGPG),
		Mode: 0o644,
		UID:  0,
		GID:  0,
	}); err != nil {
		return fmt.Errorf("nodejs install write %s: %w", nodeSourceKeyPath, err)
	}
	sources := nodeSourceSources(debArch)
	if err := guest.WriteFile(ctx, runtimeapi.WriteFileRequest{
		Ref:  runtimeRef,
		Path: nodeSourceSourcesPath,
		Body: bytes.NewReader([]byte(sources)),
		Mode: 0o644,
		UID:  0,
		GID:  0,
	}); err != nil {
		return fmt.Errorf("nodejs install write %s: %w", nodeSourceSourcesPath, err)
	}
	if err := run([]string{"apt-get", "update"}); err != nil {
		return err
	}
	return run([]string{"apt-get", "install", "-y", "nodejs=" + version})
}

func nodeSourceSources(debArch string) string {
	return fmt.Sprintf(`Enabled: yes
Types: deb
URIs: https://deb.nodesource.com/node_22.x
Suites: nodistro
Components: main
Architectures: %s
Signed-By: %s
`, debArch, nodeSourceKeyPath)
}

func debArchitecture(arch string) (string, error) {
	switch arch {
	case "x86_64":
		return "amd64", nil
	case "aarch64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported architecture %q", arch)
	}
}

func nodeJSVersion(pkg Package) (string, error) {
	for _, pin := range pkg.Pins {
		if pin.Manager == "apt" && pin.Name == "nodejs" {
			return pin.Version, nil
		}
	}
	return "", fmt.Errorf("package %q is missing a nodejs apt pin", pkg.ID)
}
