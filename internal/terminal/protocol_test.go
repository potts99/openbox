// SPDX-License-Identifier: AGPL-3.0-only

package terminal

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		frame Frame
	}{
		{
			name: "open",
			frame: OpenFrame{
				InstanceID:  "inst-1",
				Cols:        80,
				Rows:        24,
				SessionName: "pi",
				SessionID:   "sess-1",
			},
		},
		{
			name:  "open minimal",
			frame: OpenFrame{InstanceID: "inst-2", Cols: 1, Rows: 1},
		},
		{
			name:  "input",
			frame: InputFrame{Data: []byte("hello\r")},
		},
		{
			name:  "input binary",
			frame: InputFrame{Data: []byte{0x00, 0x01, 0xff, 0x1b, '[', 'A'}},
		},
		{
			name:  "input empty",
			frame: InputFrame{Data: []byte{}},
		},
		{
			name:  "output",
			frame: OutputFrame{Data: []byte("bash-5.2$ ")},
		},
		{
			name:  "output binary",
			frame: OutputFrame{Data: []byte{0x1b, '[', '2', 'J', 0x00}},
		},
		{
			name:  "resize",
			frame: ResizeFrame{Cols: 120, Rows: 40},
		},
		{
			name:  "signal",
			frame: SignalFrame{Signal: "INT"},
		},
		{
			name:  "detach",
			frame: DetachFrame{},
		},
		{
			name:  "reconnect",
			frame: ReconnectFrame{SessionID: "sess-42"},
		},
		{
			name:  "exit",
			frame: ExitFrame{Code: 0},
		},
		{
			name:  "exit non-zero",
			frame: ExitFrame{Code: 137},
		},
		{
			name:  "error",
			frame: ErrorFrame{Code: "unauthorized", Message: "session required"},
		},
		{
			name:  "error code only",
			frame: ErrorFrame{Code: "idle_timeout"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := Encode(test.frame)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if !json.Valid(encoded) {
				t.Fatalf("Encode produced invalid JSON: %q", encoded)
			}

			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &envelope); err != nil {
				t.Fatalf("envelope: %v", err)
			}
			rawType, ok := envelope["type"]
			if !ok {
				t.Fatal("encoded frame missing type field")
			}
			var typeName string
			if err := json.Unmarshal(rawType, &typeName); err != nil {
				t.Fatalf("type field: %v", err)
			}
			if typeName != test.frame.Type() {
				t.Fatalf("type = %q, want %q", typeName, test.frame.Type())
			}

			decoded, err := Decode(encoded)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if decoded.Type() != test.frame.Type() {
				t.Fatalf("decoded type = %q, want %q", decoded.Type(), test.frame.Type())
			}
			assertFrameEqual(t, decoded, test.frame)
		})
	}
}

func TestDecodeRejectsMalformedFrames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "not json", input: "not-json"},
		{name: "array", input: `[]`},
		{name: "missing type", input: `{"instance_id":"inst-1","cols":80,"rows":24}`},
		{name: "unknown type", input: `{"type":"ping"}`},
		{name: "empty type", input: `{"type":""}`},
		{name: "null type", input: `{"type":null}`},
		{name: "open missing instance", input: `{"type":"open","cols":80,"rows":24}`},
		{name: "open empty instance", input: `{"type":"open","instance_id":"","cols":80,"rows":24}`},
		{name: "open zero cols", input: `{"type":"open","instance_id":"inst-1","cols":0,"rows":24}`},
		{name: "open zero rows", input: `{"type":"open","instance_id":"inst-1","cols":80,"rows":0}`},
		{name: "open missing cols", input: `{"type":"open","instance_id":"inst-1","rows":24}`},
		{name: "input missing data", input: `{"type":"input"}`},
		{name: "input bad base64", input: `{"type":"input","data":"!!!"}`},
		{name: "output missing data", input: `{"type":"output"}`},
		{name: "output bad base64", input: `{"type":"output","data":"@@@"}`},
		{name: "resize zero cols", input: `{"type":"resize","cols":0,"rows":24}`},
		{name: "resize zero rows", input: `{"type":"resize","cols":80,"rows":0}`},
		{name: "resize missing cols", input: `{"type":"resize","rows":24}`},
		{name: "signal empty", input: `{"type":"signal","signal":""}`},
		{name: "signal missing", input: `{"type":"signal"}`},
		{name: "reconnect empty session", input: `{"type":"reconnect","session_id":""}`},
		{name: "reconnect missing session", input: `{"type":"reconnect"}`},
		{name: "exit missing code", input: `{"type":"exit"}`},
		{name: "error missing code", input: `{"type":"error","message":"nope"}`},
		{name: "error empty code", input: `{"type":"error","code":""}`},
		{name: "oversized", input: `{"type":"input","data":"` + strings.Repeat("A", MaxFrameBytes) + `"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := Decode([]byte(test.input))
			if err == nil {
				t.Fatalf("Decode(%q) unexpectedly succeeded: %#v", test.input, got)
			}
			if !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("Decode(%q) error = %v, want ErrInvalidFrame", test.input, err)
			}
		})
	}
}

func TestEncodeRejectsInvalidFrames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		frame Frame
	}{
		{name: "open empty instance", frame: OpenFrame{Cols: 80, Rows: 24}},
		{name: "open zero cols", frame: OpenFrame{InstanceID: "inst-1", Cols: 0, Rows: 24}},
		{name: "open zero rows", frame: OpenFrame{InstanceID: "inst-1", Cols: 80, Rows: 0}},
		{name: "resize zero cols", frame: ResizeFrame{Cols: 0, Rows: 24}},
		{name: "resize zero rows", frame: ResizeFrame{Cols: 80, Rows: 0}},
		{name: "signal empty", frame: SignalFrame{}},
		{name: "reconnect empty", frame: ReconnectFrame{}},
		{name: "error empty code", frame: ErrorFrame{Message: "x"}},
		{name: "nil frame", frame: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := Encode(test.frame)
			if err == nil {
				t.Fatalf("Encode unexpectedly succeeded: %q", encoded)
			}
			if !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("Encode error = %v, want ErrInvalidFrame", err)
			}
		})
	}
}

func TestFrameTypeNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		frame Frame
		want  string
	}{
		{OpenFrame{}, TypeOpen},
		{InputFrame{}, TypeInput},
		{OutputFrame{}, TypeOutput},
		{ResizeFrame{}, TypeResize},
		{SignalFrame{}, TypeSignal},
		{DetachFrame{}, TypeDetach},
		{ReconnectFrame{}, TypeReconnect},
		{ExitFrame{}, TypeExit},
		{ErrorFrame{}, TypeError},
	}

	for _, test := range tests {
		t.Run(test.want, func(t *testing.T) {
			t.Parallel()
			if got := test.frame.Type(); got != test.want {
				t.Fatalf("Type() = %q, want %q", got, test.want)
			}
		})
	}
}

func assertFrameEqual(t *testing.T, got, want Frame) {
	t.Helper()

	switch want := want.(type) {
	case OpenFrame:
		got, ok := got.(OpenFrame)
		if !ok {
			t.Fatalf("got %T, want OpenFrame", got)
		}
		if got != want {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	case InputFrame:
		got, ok := got.(InputFrame)
		if !ok {
			t.Fatalf("got %T, want InputFrame", got)
		}
		if !bytes.Equal(got.Data, want.Data) {
			t.Fatalf("got data %#v, want %#v", got.Data, want.Data)
		}
	case OutputFrame:
		got, ok := got.(OutputFrame)
		if !ok {
			t.Fatalf("got %T, want OutputFrame", got)
		}
		if !bytes.Equal(got.Data, want.Data) {
			t.Fatalf("got data %#v, want %#v", got.Data, want.Data)
		}
	case ResizeFrame:
		got, ok := got.(ResizeFrame)
		if !ok {
			t.Fatalf("got %T, want ResizeFrame", got)
		}
		if got != want {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	case SignalFrame:
		got, ok := got.(SignalFrame)
		if !ok {
			t.Fatalf("got %T, want SignalFrame", got)
		}
		if got != want {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	case DetachFrame:
		if _, ok := got.(DetachFrame); !ok {
			t.Fatalf("got %T, want DetachFrame", got)
		}
	case ReconnectFrame:
		got, ok := got.(ReconnectFrame)
		if !ok {
			t.Fatalf("got %T, want ReconnectFrame", got)
		}
		if got != want {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	case ExitFrame:
		got, ok := got.(ExitFrame)
		if !ok {
			t.Fatalf("got %T, want ExitFrame", got)
		}
		if got != want {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	case ErrorFrame:
		got, ok := got.(ErrorFrame)
		if !ok {
			t.Fatalf("got %T, want ErrorFrame", got)
		}
		if got != want {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	default:
		t.Fatalf("unexpected want type %T", want)
	}
}
