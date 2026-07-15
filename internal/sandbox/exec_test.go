// SPDX-License-Identifier: AGPL-3.0-only

package sandbox_test

import (
	"errors"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/sandbox"
)

func TestValidateExecAcceptsArgvRequest(t *testing.T) {
	t.Parallel()
	req := sandbox.ExecRequest{
		Argv:       []string{"python", "-c", "print(1)"},
		WorkingDir: "/workspace",
		Env:        map[string]string{"PATH": "/usr/bin", "HOME": "/home/agent"},
		Timeout:    time.Minute,
	}
	got, err := sandbox.ValidateExec(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Argv) != 3 || got.Argv[0] != "python" {
		t.Fatalf("argv=%v", got.Argv)
	}
	if got.WorkingDir != "/workspace" {
		t.Fatalf("cwd=%q", got.WorkingDir)
	}
	if got.Timeout != time.Minute {
		t.Fatalf("timeout=%v", got.Timeout)
	}
}

func TestValidateExecDefaultsTimeoutAndFiltersEnv(t *testing.T) {
	t.Parallel()
	got, err := sandbox.ValidateExec(sandbox.ExecRequest{
		Argv: []string{"true"},
		Env: map[string]string{
			"PATH":            "/usr/bin",
			"SECRET_API_KEY":  "nope",
			"AWS_SECRET_KEY":  "nope",
			"HOME":            "/home/agent",
			"OPENBOX_DENY_ME": "x",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Timeout != sandbox.DefaultExecTimeout {
		t.Fatalf("timeout=%v", got.Timeout)
	}
	if _, ok := got.Env["SECRET_API_KEY"]; ok {
		t.Fatal("secret env leaked")
	}
	if _, ok := got.Env["AWS_SECRET_KEY"]; ok {
		t.Fatal("secret env leaked")
	}
	if got.Env["PATH"] != "/usr/bin" || got.Env["HOME"] != "/home/agent" {
		t.Fatalf("env=%v", got.Env)
	}
	if _, ok := got.Env["OPENBOX_DENY_ME"]; ok {
		t.Fatal("non-allowlisted env leaked")
	}
}

func TestValidateExecRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		req  sandbox.ExecRequest
		code domain.ErrorCode
	}{
		{name: "empty argv", req: sandbox.ExecRequest{}, code: domain.CodeInvalidArgument},
		{name: "blank argv entry", req: sandbox.ExecRequest{Argv: []string{"echo", ""}}, code: domain.CodeInvalidArgument},
		{name: "shell string", req: sandbox.ExecRequest{Argv: []string{"echo hello"}}, code: domain.CodeInvalidArgument},
		{name: "relative cwd", req: sandbox.ExecRequest{Argv: []string{"true"}, WorkingDir: "workspace"}, code: domain.CodeInvalidArgument},
		{name: "timeout over max", req: sandbox.ExecRequest{Argv: []string{"true"}, Timeout: sandbox.MaxExecTimeout + time.Second}, code: domain.CodeInvalidArgument},
		{name: "negative timeout", req: sandbox.ExecRequest{Argv: []string{"true"}, Timeout: -time.Second}, code: domain.CodeInvalidArgument},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := sandbox.ValidateExec(tt.req)
			var domainErr *domain.Error
			if !errors.As(err, &domainErr) || domainErr.Code != tt.code {
				t.Fatalf("err=%v want %s", err, tt.code)
			}
		})
	}
}
