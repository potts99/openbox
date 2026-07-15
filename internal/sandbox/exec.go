// SPDX-License-Identifier: AGPL-3.0-only

package sandbox

import (
	"io"
	"path"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

const (
	DefaultExecTimeout = 5 * time.Minute
	MaxExecTimeout     = 30 * time.Minute
)

// DefaultEnvAllowlist is the set of environment variable names callers may set
// on sandbox exec. Secrets and arbitrary host env must not pass through.
var DefaultEnvAllowlist = []string{
	"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "LC_CTYPE", "TERM", "TMPDIR", "TMP", "TEMP",
}

// ExecRequest is the caller-facing argv exec specification before validation.
type ExecRequest struct {
	Argv       []string
	WorkingDir string
	Env        map[string]string
	Timeout    time.Duration
	Stdin      io.Reader
}

// ValidatedExec is a sanitized exec request safe to hand to the runtime.
type ValidatedExec struct {
	Argv       []string
	WorkingDir string
	Env        map[string]string
	Timeout    time.Duration
}

// ValidateExec enforces argv (no shell strings), absolute working directory,
// environment allowlisting, and timeout bounds.
func ValidateExec(req ExecRequest) (ValidatedExec, error) {
	if len(req.Argv) == 0 {
		return ValidatedExec{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "argv"}
	}
	argv := make([]string, len(req.Argv))
	for i, arg := range req.Argv {
		if arg == "" || strings.ContainsAny(arg, " \t\n\r") {
			return ValidatedExec{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "argv"}
		}
		argv[i] = arg
	}
	cwd := req.WorkingDir
	if cwd != "" {
		if !path.IsAbs(cwd) || strings.Contains(cwd, "\x00") {
			return ValidatedExec{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "working_dir"}
		}
	}
	timeout := req.Timeout
	if timeout == 0 {
		timeout = DefaultExecTimeout
	}
	if timeout < 0 || timeout > MaxExecTimeout {
		return ValidatedExec{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "timeout"}
	}
	return ValidatedExec{
		Argv:       argv,
		WorkingDir: cwd,
		Env:        filterEnv(req.Env, DefaultEnvAllowlist),
		Timeout:    timeout,
	}, nil
}

func filterEnv(env map[string]string, allowlist []string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(allowlist))
	for _, key := range allowlist {
		allowed[key] = struct{}{}
	}
	out := make(map[string]string)
	for key, value := range env {
		if _, ok := allowed[key]; !ok {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
