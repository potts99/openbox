// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"

	"golang.org/x/term"
)

func runSetup(args []string, options commonOptions, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox setup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	username := flags.String("username", "", "admin username to create")
	passwordFile := flags.String("password-file", "", "file containing the admin password")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 0 {
		return usageError(stderr, "usage: openbox setup [--username NAME] [--password-file PATH]")
	}

	client := &http.Client{Timeout: options.timeout}
	status, err := fetchBootstrapStatus(client, options.server)
	if err != nil {
		return commandError(stderr, err)
	}
	if !status.Required {
		if options.json {
			return encodeJSON(stdout, stderr, map[string]any{"required": false, "message": "already configured"})
		}
		fmt.Fprintf(stdout, "OpenBox is already configured. Sign in at %s with your admin username and password.\n", options.server)
		return 0
	}

	name := strings.TrimSpace(*username)
	if name == "" {
		name, err = promptLine(stderr, "Admin username: ")
		if err != nil {
			return commandError(stderr, err)
		}
	}
	password, err := readSetupPassword(*passwordFile, stderr)
	if err != nil {
		return commandError(stderr, err)
	}
	session, err := postBootstrap(client, options.server, name, password)
	if err != nil {
		return commandError(stderr, err)
	}
	if options.json {
		return encodeJSON(stdout, stderr, map[string]any{
			"required": false,
			"username": session.Username,
			"role":     session.Role,
			"owner_id": session.OwnerID,
		})
	}
	fmt.Fprintf(stdout, "Admin created: %s\n", session.Username)
	fmt.Fprintf(stdout, "Sign in at %s\n", options.server)
	fmt.Fprintf(stdout, "Next: open Settings in the console to create an API token, export OPENBOX_TOKEN, then run openbox doctor.\n")
	return 0
}

type bootstrapStatusResponse struct {
	Required bool `json:"required"`
}

type bootstrapSessionResponse struct {
	OwnerID  string `json:"owner_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

func fetchBootstrapStatus(client *http.Client, server string) (bootstrapStatusResponse, error) {
	request, err := http.NewRequest(http.MethodGet, strings.TrimRight(server, "/")+"/v1/bootstrap", nil)
	if err != nil {
		return bootstrapStatusResponse{}, err
	}
	request.Header.Set("X-OpenBox-API-Version", "v1")
	response, err := client.Do(request)
	if err != nil {
		return bootstrapStatusResponse{}, err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		return bootstrapStatusResponse{}, fmt.Errorf("bootstrap status: %s", strings.TrimSpace(string(body)))
	}
	var status bootstrapStatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return bootstrapStatusResponse{}, err
	}
	return status, nil
}

func postBootstrap(client *http.Client, server, username, password string) (bootstrapSessionResponse, error) {
	payload, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return bootstrapSessionResponse{}, err
	}
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(server, "/")+"/v1/bootstrap", bytes.NewReader(payload))
	if err != nil {
		return bootstrapSessionResponse{}, err
	}
	request.Header.Set("X-OpenBox-API-Version", "v1")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return bootstrapSessionResponse{}, err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusCreated {
		return bootstrapSessionResponse{}, fmt.Errorf("bootstrap failed: %s", strings.TrimSpace(string(body)))
	}
	var session bootstrapSessionResponse
	if err := json.Unmarshal(body, &session); err != nil {
		return bootstrapSessionResponse{}, err
	}
	return session, nil
}

func readSetupPassword(passwordFile string, stderr io.Writer) (string, error) {
	if passwordFile != "" {
		contents, err := os.ReadFile(passwordFile)
		if err != nil {
			return "", err
		}
		password := strings.TrimRight(string(contents), "\r\n")
		if password == "" {
			return "", errors.New("password file is empty")
		}
		return password, nil
	}
	password, err := promptPassword(stderr, "Password: ")
	if err != nil {
		return "", err
	}
	confirm, err := promptPassword(stderr, "Confirm password: ")
	if err != nil {
		return "", err
	}
	if password != confirm {
		return "", errors.New("passwords do not match")
	}
	return password, nil
}

func promptLine(stderr io.Writer, prompt string) (string, error) {
	fmt.Fprint(stderr, prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return "", errors.New("username is required")
	}
	return value, nil
}

func promptPassword(stderr io.Writer, prompt string) (string, error) {
	fmt.Fprint(stderr, prompt)
	fd := int(syscall.Stdin)
	if !term.IsTerminal(fd) {
		return "", errors.New("password prompt requires a terminal; use --password-file")
	}
	raw, err := term.ReadPassword(fd)
	fmt.Fprintln(stderr)
	if err != nil {
		return "", err
	}
	password := string(raw)
	if password == "" {
		return "", errors.New("password is required")
	}
	return password, nil
}
