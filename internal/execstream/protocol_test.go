// SPDX-License-Identifier: AGPL-3.0-only

package execstream_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/openbox-dev/openbox/internal/execstream"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		frame execstream.Frame
	}{
		{name: "stdout", frame: execstream.StdoutFrame{Data: []byte("hello\n")}},
		{name: "stdout binary", frame: execstream.StdoutFrame{Data: []byte{0x00, 0xff, 0x1b}}},
		{name: "stderr", frame: execstream.StderrFrame{Data: []byte("warn\n")}},
		{name: "exit zero", frame: execstream.ExitFrame{Code: 0}},
		{name: "exit nonzero", frame: execstream.ExitFrame{Code: 127}},
		{name: "error", frame: execstream.ErrorFrame{Code: "timeout", Message: "exec exceeded timeout"}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			encoded, err := execstream.Encode(tt.frame)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(encoded) {
				t.Fatalf("invalid json: %s", encoded)
			}
			decoded, err := execstream.Decode(encoded)
			if err != nil {
				t.Fatal(err)
			}
			assertFrameEqual(t, decoded, tt.frame)
		})
	}
}

func TestDecodeRejectsMalformed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "not object", input: `"stdout"`},
		{name: "missing type", input: `{"data":"YQ=="}`},
		{name: "unknown type", input: `{"type":"stdin","data":"YQ=="}`},
		{name: "stdout missing data", input: `{"type":"stdout"}`},
		{name: "exit missing code", input: `{"type":"exit"}`},
		{name: "error missing code", input: `{"type":"error","message":"x"}`},
		{name: "oversized", input: `{"type":"stdout","data":"` + strings.Repeat("A", execstream.MaxFrameBytes) + `"}`},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := execstream.Decode([]byte(tt.input))
			if !errors.Is(err, execstream.ErrInvalidFrame) {
				t.Fatalf("err=%v want ErrInvalidFrame", err)
			}
		})
	}
}

func TestEncodeRejectsOversizedPayload(t *testing.T) {
	t.Parallel()
	huge := bytes.Repeat([]byte("x"), execstream.MaxFrameBytes)
	_, err := execstream.Encode(execstream.StdoutFrame{Data: huge})
	if !errors.Is(err, execstream.ErrInvalidFrame) {
		t.Fatalf("err=%v want ErrInvalidFrame", err)
	}
}

func assertFrameEqual(t *testing.T, got, want execstream.Frame) {
	t.Helper()
	if got.Type() != want.Type() {
		t.Fatalf("type=%q want=%q", got.Type(), want.Type())
	}
	switch want := want.(type) {
	case execstream.StdoutFrame:
		gotFrame, ok := got.(execstream.StdoutFrame)
		if !ok || !bytes.Equal(gotFrame.Data, want.Data) {
			t.Fatalf("got=%#v want=%#v", got, want)
		}
	case execstream.StderrFrame:
		gotFrame, ok := got.(execstream.StderrFrame)
		if !ok || !bytes.Equal(gotFrame.Data, want.Data) {
			t.Fatalf("got=%#v want=%#v", got, want)
		}
	case execstream.ExitFrame:
		gotFrame, ok := got.(execstream.ExitFrame)
		if !ok || gotFrame.Code != want.Code {
			t.Fatalf("got=%#v want=%#v", got, want)
		}
	case execstream.ErrorFrame:
		gotFrame, ok := got.(execstream.ErrorFrame)
		if !ok || gotFrame.Code != want.Code || gotFrame.Message != want.Message {
			t.Fatalf("got=%#v want=%#v", got, want)
		}
	default:
		t.Fatalf("unexpected want type %T", want)
	}
}
