// SPDX-License-Identifier: AGPL-3.0-only

package sandbox

import (
	"context"
	"errors"
	"io"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/execstream"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// Execer runs an argv command inside a managed instance.
type Execer interface {
	Exec(context.Context, runtimeapi.ExecRequest) (runtimeapi.ExecResult, error)
}

// FrameSink receives framed exec output. Implementations must not retain the
// frame after Emit returns unless they copy it.
type FrameSink interface {
	Emit(execstream.Frame) error
}

// Run validates the request, applies timeout/cancellation, executes through
// the runtime boundary, and emits stdout/stderr/exit (or error) frames.
// Output is currently framed after the runtime returns; streaming belongs to
// a later task that upgrades the runtime exec path.
func Run(ctx context.Context, execer Execer, ref string, req ExecRequest, sink FrameSink) error {
	if execer == nil || sink == nil || ref == "" {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "exec"}
	}
	validated, err := ValidateExec(req)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, validated.Timeout)
	defer cancel()

	result, err := execer.Exec(runCtx, runtimeapi.ExecRequest{
		Ref:        ref,
		Command:    validated.Argv,
		WorkingDir: validated.WorkingDir,
		Env:        validated.Env,
		Stdin:      stdinOrEmpty(req.Stdin),
	})
	if err != nil {
		code := string(domain.CodeUnavailable)
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			code = "timeout"
		case errors.Is(err, context.Canceled):
			code = string(domain.CodeOperationCanceled)
		}
		_ = sink.Emit(execstream.ErrorFrame{Code: code, Message: err.Error()})
		return err
	}
	if len(result.Stdout) > 0 {
		if err := sink.Emit(execstream.StdoutFrame{Data: result.Stdout}); err != nil {
			return err
		}
	}
	if len(result.Stderr) > 0 {
		if err := sink.Emit(execstream.StderrFrame{Data: result.Stderr}); err != nil {
			return err
		}
	}
	return sink.Emit(execstream.ExitFrame{Code: result.ExitCode})
}

func stdinOrEmpty(r io.Reader) io.Reader {
	if r == nil {
		return io.MultiReader()
	}
	return r
}
