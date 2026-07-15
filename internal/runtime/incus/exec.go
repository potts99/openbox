// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// Exec runs an argv command inside a managed instance using Incus recorded
// output (no websockets). Interactive/streaming upgrades can replace this path
// later without changing the OpenBox Runtime contract.
//
// Non-empty stdin is delivered by wrapping the command in a POSIX sh pipeline
// that base64-decodes argv-carried bytes into the child stdin. That keeps small
// sandbox/apply payloads working without Incus exec websockets. Do not use this
// for file content — use WriteFile (Incus files API) as the default guest write
// path; argv wrapping is capped well below typical binary sizes.
func (a *Adapter) Exec(ctx context.Context, request runtimeapi.ExecRequest) (runtimeapi.ExecResult, error) {
	if err := ctx.Err(); err != nil {
		return runtimeapi.ExecResult{}, err
	}
	if request.Ref == "" || runtimeapi.IsHostConsoleTarget(request.Ref) {
		return runtimeapi.ExecResult{}, runtimeapi.ErrHostTarget
	}
	if len(request.Command) == 0 {
		return runtimeapi.ExecResult{}, fmt.Errorf("exec command is required")
	}
	command := append([]string(nil), request.Command...)
	if request.Stdin != nil {
		stdin, err := io.ReadAll(io.LimitReader(request.Stdin, 1<<20))
		if err != nil {
			return runtimeapi.ExecResult{}, fmt.Errorf("read exec stdin: %w", err)
		}
		if len(stdin) > 0 {
			command = wrapExecStdin(command, stdin)
		}
	}
	payload := map[string]any{
		"command":            command,
		"wait-for-websocket": false,
		"record-output":      true,
		"interactive":        false,
	}
	if request.WorkingDir != "" {
		payload["cwd"] = request.WorkingDir
	}
	if len(request.Env) > 0 {
		payload["environment"] = request.Env
	}
	query := url.Values{"project": {a.project}}
	envelope, err := a.call(ctx, a.timeout, http.MethodPost, "/1.0/instances/"+url.PathEscape(request.Ref)+"/exec", query, payload, nil)
	if err != nil {
		if isNotFound(err) {
			return runtimeapi.ExecResult{}, runtimeapi.ErrNotFound
		}
		return runtimeapi.ExecResult{}, fmt.Errorf("start Incus exec: %w", err)
	}
	if envelope.Type != "async" || envelope.Operation == "" {
		return runtimeapi.ExecResult{}, fmt.Errorf("Incus exec did not return an async operation")
	}
	var result struct {
		StatusCode int `json:"status_code"`
		Metadata   struct {
			Return int               `json:"return"`
			Output map[string]string `json:"output"`
		} `json:"metadata"`
		Err string `json:"err"`
	}
	if err := a.waitOperationResult(ctx, envelope.Operation, &result); err != nil {
		return runtimeapi.ExecResult{}, err
	}
	if result.StatusCode >= 400 || result.Err != "" {
		return runtimeapi.ExecResult{}, fmt.Errorf("Incus exec failed: %s", result.Err)
	}
	out := runtimeapi.ExecResult{ExitCode: result.Metadata.Return}
	if path := result.Metadata.Output["1"]; path != "" {
		out.Stdout, err = a.readOperationLog(ctx, path)
		if err != nil {
			return runtimeapi.ExecResult{}, err
		}
	}
	if path := result.Metadata.Output["2"]; path != "" {
		out.Stderr, err = a.readOperationLog(ctx, path)
		if err != nil {
			return runtimeapi.ExecResult{}, err
		}
	}
	return out, nil
}

func (a *Adapter) waitOperationResult(ctx context.Context, operation string, output any) error {
	envelope, err := a.call(ctx, a.operationTimeout, http.MethodGet, operation+"/wait", nil, nil, nil)
	if err != nil {
		return fmt.Errorf("wait for Incus operation: %w", err)
	}
	if envelope.Type == "async" {
		return fmt.Errorf("wait for Incus operation returned another async operation")
	}
	if len(envelope.Metadata) == 0 || string(envelope.Metadata) == "null" {
		return fmt.Errorf("Incus operation metadata missing")
	}
	if err := json.Unmarshal(envelope.Metadata, output); err != nil {
		return fmt.Errorf("decode Incus operation metadata: %w", err)
	}
	return nil
}

func (a *Adapter) readOperationLog(ctx context.Context, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	endpoint := "http://incus" + path
	if a.project != "" && !containsQuery(path) {
		endpoint += "?project=" + url.QueryEscape(a.project)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create Incus log request: %w", err)
	}
	response, err := a.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch Incus exec log: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch Incus exec log: %s", response.Status)
	}
	return io.ReadAll(io.LimitReader(response.Body, 8<<20))
}

func containsQuery(path string) bool {
	for i := 0; i < len(path); i++ {
		if path[i] == '?' {
			return true
		}
	}
	return false
}

// wrapExecStdin rewrites argv so guest sh feeds decoded stdin into the original
// command without Incus websockets. $1 is base64 payload; remaining args are
// the real argv after shift.
func wrapExecStdin(command []string, stdin []byte) []string {
	wrapped := []string{
		"sh", "-c", `b64=$1; shift; printf '%s' "$b64" | base64 -d | "$@"`,
		"openbox-exec-stdin",
		base64.StdEncoding.EncodeToString(stdin),
	}
	return append(wrapped, command...)
}
