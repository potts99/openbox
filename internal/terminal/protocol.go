// SPDX-License-Identifier: AGPL-3.0-only

// Package terminal defines the browser-terminal WebSocket frame protocol and
// session limit helpers (frame size, inbound rate, concurrent sessions, idle).
//
// Frames are JSON text messages with a discriminant "type" field. Input and
// output payloads are base64-encoded so arbitrary PTY bytes round-trip safely.
// Authentication, origin checks, and PTY runtime belong to the HTTP/runtime
// layers; this package owns typed frames, codec rules, and limit primitives.
package terminal

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// MaxFrameBytes is the maximum accepted encoded frame size. Connection-level
// rate, idle, and session limits build on this hard decode bound.
const MaxFrameBytes = 64 << 10

// Frame type discriminants.
const (
	TypeOpen      = "open"
	TypeInput     = "input"
	TypeOutput    = "output"
	TypeResize    = "resize"
	TypeSignal    = "signal"
	TypeDetach    = "detach"
	TypeReconnect = "reconnect"
	TypeExit      = "exit"
	TypeError     = "error"
)

// ErrInvalidFrame is returned when Encode or Decode rejects a malformed frame.
var ErrInvalidFrame = errors.New("invalid terminal frame")

// Frame is a typed terminal WebSocket message.
type Frame interface {
	Type() string
}

// OpenFrame starts or confirms a terminal session inside an instance.
type OpenFrame struct {
	InstanceID        string `json:"instance_id"`
	Cols              uint16 `json:"cols"`
	Rows              uint16 `json:"rows"`
	SessionName       string `json:"session_name,omitempty"`
	SessionID         string `json:"session_id,omitempty"`
	WorkingDirectory  string `json:"working_directory,omitempty"`
}

func (OpenFrame) Type() string { return TypeOpen }

// InputFrame carries client keystrokes or pasted bytes to the PTY.
type InputFrame struct {
	Data []byte `json:"-"`
}

func (InputFrame) Type() string { return TypeInput }

// OutputFrame carries PTY output to the browser.
type OutputFrame struct {
	Data []byte `json:"-"`
}

func (OutputFrame) Type() string { return TypeOutput }

// ResizeFrame updates the PTY dimensions.
type ResizeFrame struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func (ResizeFrame) Type() string { return TypeResize }

// SignalFrame delivers a named signal to the session (for example INT or TERM).
type SignalFrame struct {
	Signal string `json:"signal"`
}

func (SignalFrame) Type() string { return TypeSignal }

// DetachFrame closes the browser connection without terminating a named session.
type DetachFrame struct{}

func (DetachFrame) Type() string { return TypeDetach }

// ReconnectFrame attaches to an existing daemon-side session.
type ReconnectFrame struct {
	SessionID string `json:"session_id"`
}

func (ReconnectFrame) Type() string { return TypeReconnect }

// ExitFrame reports the terminal process exit status.
type ExitFrame struct {
	Code int `json:"code"`
}

func (ExitFrame) Type() string { return TypeExit }

// ErrorFrame reports a protocol or session error to the peer.
type ErrorFrame struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func (ErrorFrame) Type() string { return TypeError }

type envelope struct {
	Type string `json:"type"`
}

type openWire struct {
	Type              string `json:"type"`
	InstanceID        string `json:"instance_id"`
	Cols              uint16 `json:"cols"`
	Rows              uint16 `json:"rows"`
	SessionName       string `json:"session_name,omitempty"`
	SessionID         string `json:"session_id,omitempty"`
	WorkingDirectory  string `json:"working_directory,omitempty"`
}

type dataWire struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type resizeWire struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type signalWire struct {
	Type   string `json:"type"`
	Signal string `json:"signal"`
}

type reconnectWire struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
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

// Encode serializes a frame as a JSON text WebSocket payload.
func Encode(frame Frame) ([]byte, error) {
	if frame == nil {
		return nil, invalid("frame is nil")
	}

	var (
		payload any
		err     error
	)
	switch f := frame.(type) {
	case OpenFrame:
		if err = validateOpen(f); err != nil {
			return nil, err
		}
		payload = openWire{
			Type: TypeOpen, InstanceID: f.InstanceID, Cols: f.Cols, Rows: f.Rows,
			SessionName: f.SessionName, SessionID: f.SessionID, WorkingDirectory: f.WorkingDirectory,
		}
	case InputFrame:
		payload = dataWire{Type: TypeInput, Data: base64.StdEncoding.EncodeToString(f.Data)}
	case OutputFrame:
		payload = dataWire{Type: TypeOutput, Data: base64.StdEncoding.EncodeToString(f.Data)}
	case ResizeFrame:
		if err = validateResize(f.Cols, f.Rows); err != nil {
			return nil, err
		}
		payload = resizeWire{Type: TypeResize, Cols: f.Cols, Rows: f.Rows}
	case SignalFrame:
		if f.Signal == "" {
			return nil, invalid("signal is required")
		}
		payload = signalWire{Type: TypeSignal, Signal: f.Signal}
	case DetachFrame:
		payload = envelope{Type: TypeDetach}
	case ReconnectFrame:
		if f.SessionID == "" {
			return nil, invalid("session_id is required")
		}
		payload = reconnectWire{Type: TypeReconnect, SessionID: f.SessionID}
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

// Decode parses a JSON text WebSocket payload into a typed frame.
func Decode(raw []byte) (Frame, error) {
	if len(raw) == 0 {
		return nil, invalid("frame is empty")
	}
	if len(raw) > MaxFrameBytes {
		return nil, invalid("frame exceeds %d bytes", MaxFrameBytes)
	}

	var meta envelope
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, invalid("malformed json: %v", err)
	}

	switch meta.Type {
	case TypeOpen:
		var wire openWire
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, invalid("malformed open: %v", err)
		}
		frame := OpenFrame{
			InstanceID: wire.InstanceID, Cols: wire.Cols, Rows: wire.Rows,
			SessionName: wire.SessionName, SessionID: wire.SessionID,
			WorkingDirectory: wire.WorkingDirectory,
		}
		if err := validateOpen(frame); err != nil {
			return nil, err
		}
		return frame, nil
	case TypeInput:
		data, err := decodeDataPayload(raw)
		if err != nil {
			return nil, err
		}
		return InputFrame{Data: data}, nil
	case TypeOutput:
		data, err := decodeDataPayload(raw)
		if err != nil {
			return nil, err
		}
		return OutputFrame{Data: data}, nil
	case TypeResize:
		var wire resizeWire
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, invalid("malformed resize: %v", err)
		}
		if err := validateResize(wire.Cols, wire.Rows); err != nil {
			return nil, err
		}
		return ResizeFrame{Cols: wire.Cols, Rows: wire.Rows}, nil
	case TypeSignal:
		var wire signalWire
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, invalid("malformed signal: %v", err)
		}
		if wire.Signal == "" {
			return nil, invalid("signal is required")
		}
		return SignalFrame{Signal: wire.Signal}, nil
	case TypeDetach:
		return DetachFrame{}, nil
	case TypeReconnect:
		var wire reconnectWire
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, invalid("malformed reconnect: %v", err)
		}
		if wire.SessionID == "" {
			return nil, invalid("session_id is required")
		}
		return ReconnectFrame{SessionID: wire.SessionID}, nil
	case TypeExit:
		var wire exitWire
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, invalid("malformed exit: %v", err)
		}
		if wire.Code == nil {
			return nil, invalid("exit code is required")
		}
		return ExitFrame{Code: *wire.Code}, nil
	case TypeError:
		var wire errorWire
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, invalid("malformed error: %v", err)
		}
		if wire.Code == "" {
			return nil, invalid("error code is required")
		}
		return ErrorFrame{Code: wire.Code, Message: wire.Message}, nil
	case "":
		return nil, invalid("type is required")
	default:
		return nil, invalid("unknown type %q", meta.Type)
	}
}

func decodeDataPayload(raw []byte) ([]byte, error) {
	var wire struct {
		Data *string `json:"data"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, invalid("malformed data frame: %v", err)
	}
	if wire.Data == nil {
		return nil, invalid("data is required")
	}
	decoded, err := base64.StdEncoding.DecodeString(*wire.Data)
	if err != nil {
		return nil, invalid("data is not base64: %v", err)
	}
	if decoded == nil {
		decoded = []byte{}
	}
	return decoded, nil
}

func validateOpen(frame OpenFrame) error {
	if frame.InstanceID == "" {
		return invalid("instance_id is required")
	}
	return validateResize(frame.Cols, frame.Rows)
}

func validateResize(cols, rows uint16) error {
	if cols == 0 {
		return invalid("cols must be greater than zero")
	}
	if rows == 0 {
		return invalid("rows must be greater than zero")
	}
	return nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidFrame, fmt.Sprintf(format, args...))
}
