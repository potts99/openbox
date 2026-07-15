// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// buildForwardSSHArgs constructs `ssh -N -L …` for tunnelling an instance port
// through the OpenBox SSH gateway without a public domain.
func buildForwardSSHArgs(host string, sshPort, localPort, remotePort int, instance string) ([]string, error) {
	host = strings.TrimSpace(host)
	instance = strings.TrimSpace(instance)
	if host == "" || instance == "" {
		return nil, errors.New("host and instance are required")
	}
	if strings.ContainsAny(host, " \t\r\n") || strings.ContainsAny(instance, " \t\r\n@") {
		return nil, errors.New("invalid host or instance name")
	}
	if sshPort < 1 || sshPort > 65535 || localPort < 1 || localPort > 65535 || remotePort < 1 || remotePort > 65535 {
		return nil, errors.New("ports must be between 1 and 65535")
	}
	return []string{
		"-N",
		"-L", fmt.Sprintf("%d:127.0.0.1:%d", localPort, remotePort),
		"-p", strconv.Itoa(sshPort),
		fmt.Sprintf("%s@%s", instance, host),
	}, nil
}

func runForward(args []string, stdout, stderr io.Writer, lookPath func(string) (string, error), runCmd func(*exec.Cmd) error) int {
	flags := flag.NewFlagSet("openbox forward", flag.ContinueOnError)
	flags.SetOutput(stderr)
	host := flags.String("host", "", "OpenBox SSH gateway host")
	sshPort := flags.Int("ssh-port", defaultSSHGatewayPort, "OpenBox SSH gateway port")
	localPort := flags.Int("local", 0, "local listen port (default: same as remote)")
	printOnly := flags.Bool("print", false, "print the ssh command instead of running it")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	rest := flags.Args()
	if len(rest) != 2 {
		return usageError(stderr, "usage: openbox forward INSTANCE PORT [--host HOST] [--ssh-port 2222] [--local PORT] [--print]")
	}
	remotePort, err := strconv.Atoi(rest[1])
	if err != nil {
		return commandError(stderr, errors.New("PORT must be an integer"))
	}
	local := *localPort
	if local == 0 {
		local = remotePort
	}
	sshArgs, err := buildForwardSSHArgs(*host, *sshPort, local, remotePort, rest[0])
	if err != nil {
		return commandError(stderr, err)
	}
	if *printOnly {
		fmt.Fprintf(stdout, "ssh %s\n", strings.Join(sshArgs, " "))
		return 0
	}
	if strings.TrimSpace(*host) == "" {
		return usageError(stderr, "--host is required unless --print is used with a host")
	}
	path, err := lookPath("ssh")
	if err != nil {
		return commandError(stderr, errors.New("ssh client not found on PATH"))
	}
	cmd := exec.Command(path, sshArgs...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = os.Stdin
	fmt.Fprintf(stderr, "Forwarding localhost:%d → %s:%d via %s@%s (Ctrl-C to stop)\n", local, rest[0], remotePort, rest[0], *host)
	if err := runCmd(cmd); err != nil {
		return commandError(stderr, err)
	}
	return 0
}
