// SPDX-License-Identifier: AGPL-3.0-only

package sandbox_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/execstream"
	"github.com/openbox-dev/openbox/internal/sandbox"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

type stubExecer struct {
	last runtimeapi.ExecRequest
	fn   func(context.Context, runtimeapi.ExecRequest) (runtimeapi.ExecResult, error)
}

func (s *stubExecer) Exec(ctx context.Context, req runtimeapi.ExecRequest) (runtimeapi.ExecResult, error) {
	s.last = req
	if s.fn != nil {
		return s.fn(ctx, req)
	}
	return runtimeapi.ExecResult{}, nil
}

type frameBuf struct {
	frames []execstream.Frame
}

func (b *frameBuf) Emit(frame execstream.Frame) error {
	b.frames = append(b.frames, frame)
	return nil
}

func TestRunExecEmitsStdoutStderrAndExit(t *testing.T) {
	t.Parallel()
	execer := &stubExecer{fn: func(ctx context.Context, req runtimeapi.ExecRequest) (runtimeapi.ExecResult, error) {
		if len(req.Command) != 2 || req.Command[0] != "python" {
			t.Fatalf("command=%v", req.Command)
		}
		if req.WorkingDir != "/workspace" {
			t.Fatalf("cwd=%q", req.WorkingDir)
		}
		if req.Env["PATH"] != "/usr/bin" {
			t.Fatalf("env=%v", req.Env)
		}
		stdin, err := io.ReadAll(req.Stdin)
		if err != nil {
			t.Fatal(err)
		}
		if string(stdin) != "payload" {
			t.Fatalf("stdin=%q", stdin)
		}
		return runtimeapi.ExecResult{ExitCode: 3, Stdout: []byte("out\n"), Stderr: []byte("err\n")}, nil
	}}
	sink := &frameBuf{}
	err := sandbox.Run(context.Background(), execer, "obx-1", sandbox.ExecRequest{
		Argv:       []string{"python", "-c"},
		WorkingDir: "/workspace",
		Env:        map[string]string{"PATH": "/usr/bin", "SECRET": "x"},
		Stdin:      bytes.NewBufferString("payload"),
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := execer.last.Env["SECRET"]; ok {
		t.Fatal("secret env reached runtime")
	}
	if len(sink.frames) != 3 {
		t.Fatalf("frames=%d", len(sink.frames))
	}
	out, ok := sink.frames[0].(execstream.StdoutFrame)
	if !ok || string(out.Data) != "out\n" {
		t.Fatalf("stdout=%#v", sink.frames[0])
	}
	errFrame, ok := sink.frames[1].(execstream.StderrFrame)
	if !ok || string(errFrame.Data) != "err\n" {
		t.Fatalf("stderr=%#v", sink.frames[1])
	}
	exit, ok := sink.frames[2].(execstream.ExitFrame)
	if !ok || exit.Code != 3 {
		t.Fatalf("exit=%#v", sink.frames[2])
	}
}

func TestRunExecHonorsCancel(t *testing.T) {
	t.Parallel()
	execer := &stubExecer{fn: func(ctx context.Context, req runtimeapi.ExecRequest) (runtimeapi.ExecResult, error) {
		select {
		case <-ctx.Done():
			return runtimeapi.ExecResult{}, ctx.Err()
		case <-time.After(2 * time.Second):
			return runtimeapi.ExecResult{ExitCode: 0}, nil
		}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sink := &frameBuf{}
	err := sandbox.Run(ctx, execer, "obx-1", sandbox.ExecRequest{Argv: []string{"sleep"}}, sink)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want canceled", err)
	}
	if len(sink.frames) != 1 {
		t.Fatalf("frames=%d", len(sink.frames))
	}
	errFrame, ok := sink.frames[0].(execstream.ErrorFrame)
	if !ok || errFrame.Code != string(domain.CodeOperationCanceled) {
		t.Fatalf("frame=%#v", sink.frames[0])
	}
}

func TestRunExecTimesOut(t *testing.T) {
	t.Parallel()
	execer := &stubExecer{fn: func(ctx context.Context, req runtimeapi.ExecRequest) (runtimeapi.ExecResult, error) {
		<-ctx.Done()
		return runtimeapi.ExecResult{}, ctx.Err()
	}}
	sink := &frameBuf{}
	err := sandbox.Run(context.Background(), execer, "obx-1", sandbox.ExecRequest{
		Argv:    []string{"sleep"},
		Timeout: 20 * time.Millisecond,
	}, sink)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v want deadline", err)
	}
	errFrame, ok := sink.frames[0].(execstream.ErrorFrame)
	if !ok || errFrame.Code != "timeout" {
		t.Fatalf("frame=%#v", sink.frames[0])
	}
}

func TestRunExecChunksLargeOutput(t *testing.T) {
	t.Parallel()
	huge := bytes.Repeat([]byte("x"), (45<<10)+100)
	execer := &stubExecer{fn: func(context.Context, runtimeapi.ExecRequest) (runtimeapi.ExecResult, error) {
		return runtimeapi.ExecResult{ExitCode: 0, Stdout: huge}, nil
	}}
	sink := &frameBuf{}
	if err := sandbox.Run(context.Background(), execer, "obx-1", sandbox.ExecRequest{Argv: []string{"cat"}}, sink); err != nil {
		t.Fatal(err)
	}
	var total int
	for _, frame := range sink.frames[:len(sink.frames)-1] {
		out, ok := frame.(execstream.StdoutFrame)
		if !ok {
			t.Fatalf("frame=%T", frame)
		}
		encoded, err := execstream.Encode(out)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > execstream.MaxFrameBytes {
			t.Fatalf("encoded frame %d bytes", len(encoded))
		}
		total += len(out.Data)
	}
	if total != len(huge) {
		t.Fatalf("total=%d want=%d", total, len(huge))
	}
}
