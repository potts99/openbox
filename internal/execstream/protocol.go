// SPDX-License-Identifier: AGPL-3.0-only

// Package execstream defines NDJSON/WebSocket frames for sandbox command
// execution: stdout, stderr, exit, and error. Binary payloads are base64 so
// arbitrary process bytes round-trip safely. HTTP streaming and runtime exec
// belong to other packages; this one owns typed frames and codec rules.
package execstream

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// MaxFrameBytes is the maximum accepted encoded frame size.
const MaxFrameBytes = 64 << 10

// Frame type discriminants.
const (
	TypeStdout = "stdout"
	TypeStderr = "stderr"
	TypeExit   = "exit"
	TypeError  = "error"
)

// ErrInvalidFrame is returned when Encode or Decode rejects a malformed frame.
var ErrInvalidFrame = errors.New("invalid exec frame")

// Frame is a typed exec stream message.
type Frame interface {
	Type() string
}

// StdoutFrame carries process stdout bytes.
type StdoutFrame struct {
	Data []byte `json:"-"`
}

func (StdoutFrame) Type() string { return TypeStdout }

// StderrFrame carries process stderr bytes.
type StderrFrame struct {
	Data []byte `json:"-"`
}

func (StderrFrame) Type() string { return TypeStderr }

// ExitFrame reports the process exit status.
type ExitFrame struct {
	Code int `json:"code"`
}

func (ExitFrame) Type() string { return TypeExit }

// ErrorFrame reports a transport or policy failure before a clean exit.
type ErrorFrame struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func (ErrorFrame) Type() string { return TypeError }

type envelope struct {
	Type string `json:"type"`
}

type dataWire struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type exitWire struct {
	Type string `json:"type"`
	Code *int   `json:"code"`
}

type errorWire struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// Encode serializes a frame as a JSON text payload.
func Encode(frame Frame) ([]byte, error) {
	if frame == nil {
		return nil, invalid("frame is nil")
	}
	var payload any
	switch f := frame.(type) {
	case StdoutFrame:
		payload = dataWire{Type: TypeStdout, Data: base64.StdEncoding.EncodeToString(f.Data)}
	case StderrFrame:
		payload = dataWire{Type: TypeStderr, Data: base64.StdEncoding.EncodeToString(f.Data)}
	case ExitFrame:
		code := f.Code
		payload = exitWire{Type: TypeExit, Code: &code}
	case ErrorFrame:
		if f.Code == "" {
			return nil, invalid("error code is required")
		}
		payload = errorWire{Type: TypeError, Code: f.Code, Message: f.Message}
	default:
		return nil, invalid("unsupported frame type %T", frame)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, invalid("marshal: %v", err)
	}
	if len(encoded) > MaxFrameBytes {
		return nil, invalid("frame exceeds %d bytes", MaxFrameBytes)
	}
	return encoded, nil
}

// Decode parses a JSON text payload into a typed frame.
func Decode(raw []byte) (Frame, error) {
	if len(raw) == 0 {
		return nil, invalid("empty frame")
	}
	if len(raw) > MaxFrameBytes {
		return nil, invalid("frame exceeds %d bytes", MaxFrameBytes)
	}
	var meta envelope
	if err := json.Unmarshal(raw, &meta); err != nil || meta.Type == "" {
		return nil, invalid("missing type")
	}
	switch meta.Type {
	case TypeStdout, TypeStderr:
		if !jsonHasDataKey(raw) {
			return nil, invalid("%s data is required", meta.Type)
		}
		var wire dataWire
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, invalid("%s data is required", meta.Type)
		}
		decoded, err := base64.StdEncoding.DecodeString(wire.Data)
		if err != nil {
			return nil, invalid("data is not base64")
		}
		if meta.Type == TypeStdout {
			return StdoutFrame{Data: decoded}, nil
		}
		return StderrFrame{Data: decoded}, nil
	case TypeExit:
		var wire exitWire
		if err := json.Unmarshal(raw, &wire); err != nil || wire.Code == nil {
			return nil, invalid("exit code is required")
		}
		return ExitFrame{Code: *wire.Code}, nil
	case TypeError:
		var wire errorWire
		if err := json.Unmarshal(raw, &wire); err != nil || wire.Code == "" {
			return nil, invalid("error code is required")
		}
		return ErrorFrame{Code: wire.Code, Message: wire.Message}, nil
	default:
		return nil, invalid("unknown type %q", meta.Type)
	}
}

func jsonHasDataKey(raw []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	_, ok := probe["data"]
	return ok
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidFrame, fmt.Sprintf(format, args...))
}
