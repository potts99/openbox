// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Browser-terminal WebSocket frame codec mirroring internal/terminal/protocol.go.
 * Input/output payloads are base64 so arbitrary PTY bytes round-trip as JSON text.
 */

export type TerminalFrame =
  | { type: "open"; instanceId: string; cols: number; rows: number; sessionName?: string; sessionId?: string; workingDirectory?: string }
  | { type: "input"; data: Uint8Array }
  | { type: "output"; data: Uint8Array }
  | { type: "resize"; cols: number; rows: number }
  | { type: "signal"; signal: string }
  | { type: "detach" }
  | { type: "reconnect"; sessionId: string }
  | { type: "exit"; code: number }
  | { type: "error"; code: string; message?: string };

function bytesToBase64(data: Uint8Array): string {
  let binary = "";
  for (const byte of data) binary += String.fromCharCode(byte);
  return btoa(binary);
}

function base64ToBytes(value: string): Uint8Array {
  const binary = atob(value);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) out[i] = binary.charCodeAt(i);
  return out;
}

export function encodeFrame(frame: TerminalFrame): string {
  switch (frame.type) {
    case "open": {
      const payload: Record<string, unknown> = {
        type: "open",
        instance_id: frame.instanceId,
        cols: frame.cols,
        rows: frame.rows,
      };
      if (frame.sessionName) payload.session_name = frame.sessionName;
      if (frame.sessionId) payload.session_id = frame.sessionId;
      if (frame.workingDirectory) payload.working_directory = frame.workingDirectory;
      return JSON.stringify(payload);
    }
    case "input":
      return JSON.stringify({ type: "input", data: bytesToBase64(frame.data) });
    case "output":
      return JSON.stringify({ type: "output", data: bytesToBase64(frame.data) });
    case "resize":
      return JSON.stringify({ type: "resize", cols: frame.cols, rows: frame.rows });
    case "signal":
      return JSON.stringify({ type: "signal", signal: frame.signal });
    case "detach":
      return JSON.stringify({ type: "detach" });
    case "reconnect":
      return JSON.stringify({ type: "reconnect", session_id: frame.sessionId });
    case "exit":
      return JSON.stringify({ type: "exit", code: frame.code });
    case "error":
      return JSON.stringify({ type: "error", code: frame.code, message: frame.message });
    default: {
      const _exhaustive: never = frame;
      throw new Error(`unsupported frame ${JSON.stringify(_exhaustive)}`);
    }
  }
}

export function decodeFrame(raw: string): TerminalFrame {
  const value = JSON.parse(raw) as Record<string, unknown>;
  const type = value.type;
  switch (type) {
    case "open":
      return {
        type: "open",
        instanceId: String(value.instance_id ?? ""),
        cols: Number(value.cols),
        rows: Number(value.rows),
        sessionName: typeof value.session_name === "string" ? value.session_name : undefined,
        sessionId: typeof value.session_id === "string" ? value.session_id : undefined,
        workingDirectory: typeof value.working_directory === "string" ? value.working_directory : undefined,
      };
    case "input":
      return { type: "input", data: base64ToBytes(String(value.data ?? "")) };
    case "output":
      return { type: "output", data: base64ToBytes(String(value.data ?? "")) };
    case "resize":
      return { type: "resize", cols: Number(value.cols), rows: Number(value.rows) };
    case "signal":
      return { type: "signal", signal: String(value.signal ?? "") };
    case "detach":
      return { type: "detach" };
    case "reconnect":
      return { type: "reconnect", sessionId: String(value.session_id ?? "") };
    case "exit":
      return { type: "exit", code: Number(value.code) };
    case "error":
      return {
        type: "error",
        code: String(value.code ?? ""),
        message: typeof value.message === "string" ? value.message : undefined,
      };
    default:
      throw new Error(`unknown frame type ${String(type)}`);
  }
}
