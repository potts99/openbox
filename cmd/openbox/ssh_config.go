// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const defaultSSHGatewayPort = 2222

type sshConfigOptions struct {
	host, alias, path string
	port              int
}

func runSSHConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "print" && args[0] != "install") {
		return usageError(stderr, "usage: openbox ssh-config (print|install) --host HOST [--port 2222] [--alias openbox] [--config PATH]")
	}
	action := args[0]
	flags := flag.NewFlagSet("openbox ssh-config "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := sshConfigOptions{port: defaultSSHGatewayPort, alias: "openbox"}
	flags.StringVar(&options.host, "host", "", "OpenBox gateway hostname or address")
	flags.IntVar(&options.port, "port", defaultSSHGatewayPort, "OpenBox SSH gateway port")
	flags.StringVar(&options.alias, "alias", "openbox", "local management alias")
	flags.StringVar(&options.path, "config", "", "OpenSSH client config path")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || !validSSHConfigOptions(options) {
		return usageError(stderr, "--host, a valid port, and a simple alias are required")
	}
	block := renderSSHConfig(options)
	if action == "print" {
		_, _ = io.WriteString(stdout, block)
		return 0
	}
	path, err := sshConfigPath(options.path)
	if err != nil {
		return commandError(stderr, err)
	}
	installed, err := installSSHConfig(path, options.alias, block)
	if err != nil {
		return commandError(stderr, err)
	}
	if installed {
		fmt.Fprintf(stdout, "Installed OpenBox SSH aliases in %s\n", path)
	} else {
		fmt.Fprintf(stdout, "OpenBox SSH aliases already exist in %s; no changes made\n", path)
	}
	return 0
}

func validSSHConfigOptions(options sshConfigOptions) bool {
	if strings.TrimSpace(options.host) != options.host || options.host == "" || strings.ContainsAny(options.host, " \t\r\n") {
		return false
	}
	if options.port < 1 || options.port > 65535 || options.alias == "" || strings.TrimSpace(options.alias) != options.alias {
		return false
	}
	for _, r := range options.alias {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func renderSSHConfig(options sshConfigOptions) string {
	return fmt.Sprintf("# BEGIN OPENBOX %s\nHost %s\n  HostName %s\n  Port %d\n  User openbox\n  IdentitiesOnly yes\n\nHost *.openbox\n  HostName %s\n  Port %d\n  User %%n\n  IdentitiesOnly yes\n# END OPENBOX %s\n", options.alias, options.alias, options.host, options.port, options.host, options.port, options.alias)
}

func sshConfigPath(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Clean(explicit), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

func installSSHConfig(path, alias, block string) (bool, error) {
	var existingInfo os.FileInfo
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, errors.New("SSH config path must be a regular file, not a symlink")
		}
		existingInfo = info
		body, err := os.ReadFile(path)
		if err != nil {
			return false, fmt.Errorf("read SSH config: %w", err)
		}
		if hasHostPattern(string(body), alias) || hasHostPattern(string(body), "*.openbox") {
			if strings.Contains(string(body), "# BEGIN OPENBOX "+alias) {
				return false, nil
			}
			return false, errors.New("SSH config already defines the requested OpenBox alias; no changes made")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("inspect SSH config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, fmt.Errorf("create SSH config directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return false, fmt.Errorf("open SSH config: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("inspect SSH config: %w", err)
	}
	pathInfo, err := os.Lstat(path)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(pathInfo, info) || (existingInfo != nil && !os.SameFile(existingInfo, info)) {
		return false, errors.New("SSH config changed while opening; no changes made")
	}
	prefix := ""
	if info.Size() > 0 {
		prefix = "\n"
	}
	if _, err := io.WriteString(file, prefix+block); err != nil {
		return false, fmt.Errorf("write SSH config: %w", err)
	}
	if err := file.Sync(); err != nil {
		return false, fmt.Errorf("sync SSH config: %w", err)
	}
	return true, nil
}

func hasHostPattern(body, wanted string) bool {
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || !strings.EqualFold(fields[0], "Host") {
			continue
		}
		for _, pattern := range fields[1:] {
			if pattern == wanted {
				return true
			}
		}
	}
	return false
}
