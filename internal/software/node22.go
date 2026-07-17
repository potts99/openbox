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

// ConfigureNodeSource configures the NodeSource Node 22 apt repository for an
// OpenBox architecture. Repo metadata is written via WriteFile so guests never
// curl it during installation.
func ConfigureNodeSource(ctx context.Context, guest Guest, runtimeRef, architecture string) error {
	debArch, err := debArchitecture(architecture)
	if err != nil {
		return err
	}
	return configureNodeSource(ctx, guest, runtimeRef, debArch)
}

// installNodeJS configures the pinned NodeSource Node 22 apt repo and installs
// nodejs. Repo metadata is written via WriteFile so guest recipes never curl.
func installNodeJS(ctx context.Context, guest Guest, runtimeRef, debArch, version string) error {
	if debArch == "" || version == "" {
		return fmt.Errorf("nodejs install: deb architecture and version are required")
	}
	if err := configureNodeSource(ctx, guest, runtimeRef, debArch); err != nil {
		return err
	}
	return runNodeSourceCommand(ctx, guest, runtimeRef, []string{"apt-get", "install", "-y", "nodejs=" + version})
}

func configureNodeSource(ctx context.Context, guest Guest, runtimeRef, debArch string) error {
	if guest == nil {
		return fmt.Errorf("nodesource configure: guest is required")
	}
	if runtimeRef == "" {
		return fmt.Errorf("nodesource configure: runtime ref is required")
	}
	if debArch == "" {
		return fmt.Errorf("nodesource configure: deb architecture is required")
	}
	if err := runNodeSourceCommand(ctx, guest, runtimeRef, []string{"apt-get", "update"}); err != nil {
		return err
	}
	if err := runNodeSourceCommand(ctx, guest, runtimeRef, []string{"apt-get", "install", "-y", "ca-certificates", "gnupg", "apt-transport-https"}); err != nil {
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
		return fmt.Errorf("nodesource configure write %s: %w", nodeSourceKeyPath, err)
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
		return fmt.Errorf("nodesource configure write %s: %w", nodeSourceSourcesPath, err)
	}
	return runNodeSourceCommand(ctx, guest, runtimeRef, []string{"apt-get", "update"})
}

func runNodeSourceCommand(ctx context.Context, guest Guest, runtimeRef string, step []string) error {
	env := map[string]string{
		"DEBIAN_FRONTEND": "noninteractive",
		"PATH":            "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	result, err := guest.Exec(ctx, runtimeapi.ExecRequest{
		Ref:        runtimeRef,
		Command:    step,
		WorkingDir: "/",
		Env:        env,
	})
	if err != nil {
		return fmt.Errorf("nodesource (%s): %w", strings.Join(step, " "), err)
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(string(result.Stderr))
		if detail == "" {
			detail = strings.TrimSpace(string(result.Stdout))
		}
		if detail == "" {
			detail = fmt.Sprintf("exit %d", result.ExitCode)
		}
		return fmt.Errorf("nodesource (%s): %s", strings.Join(step, " "), detail)
	}
	return nil
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
