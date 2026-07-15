// SPDX-License-Identifier: AGPL-3.0-only

import { decodeFrame, encodeFrame } from "./protocol";

export type ConnectionStatus = "connecting" | "connected" | "disconnected" | "error";

export interface ConnectionState {
  status: ConnectionStatus;
  detail?: string;
}

export interface TerminalSessionOptions {
  instanceId: string;
  csrfToken: string;
  cols: number;
  rows: number;
  /** Named tmux session inside the guest; omitted for ephemeral shells. */
  sessionName?: string;
  /** Absolute guest working directory for Launch Pi. */
  workingDirectory?: string;
  WebSocketImpl?: typeof WebSocket;
  location?: Pick<Location, "protocol" | "host">;
  onStateChange?: (state: ConnectionState) => void;
  onOutput?: (data: Uint8Array) => void;
  onExit?: (code: number) => void;
}

export function terminalWebSocketUrl(
  instanceId: string,
  csrfToken: string,
  location: Pick<Location, "protocol" | "host"> = window.location,
): string {
  const protocol = location.protocol === "https:" ? "wss:" : "ws:";
  const params = new URLSearchParams({ csrf: csrfToken });
  return `${protocol}//${location.host}/v1/instances/${encodeURIComponent(instanceId)}/terminal?${params}`;
}

export class TerminalSession {
  private readonly options: TerminalSessionOptions;
  private socket: WebSocket | null = null;
  private cols: number;
  private rows: number;
  private state: ConnectionState = { status: "disconnected" };
  private intentionalClose = false;
  private sessionId: string | undefined;
  private preferReconnect = false;

  constructor(options: TerminalSessionOptions) {
    this.options = options;
    this.cols = options.cols;
    this.rows = options.rows;
  }

  getState(): ConnectionState {
    return this.state;
  }

  connect(): void {
    this.preferReconnect = false;
    this.openSocket();
  }

  reconnect(): void {
    this.preferReconnect = true;
    this.disposeSocket();
    this.openSocket();
  }

  sendInput(data: Uint8Array): void {
    this.send({ type: "input", data });
  }

  resize(cols: number, rows: number): void {
    this.cols = cols;
    this.rows = rows;
    if (this.state.status !== "connected") return;
    this.send({ type: "resize", cols, rows });
  }

  terminate(): void {
    this.send({ type: "signal", signal: "TERM" });
  }

  detach(): void {
    this.send({ type: "detach" });
    this.intentionalClose = true;
    this.socket?.close();
  }

  dispose(): void {
    this.intentionalClose = true;
    this.disposeSocket();
    this.setState({ status: "disconnected" });
  }

  private openSocket(): void {
    this.intentionalClose = false;
    this.disposeSocket();
    this.setState({ status: "connecting" });

    const WebSocketImpl = this.options.WebSocketImpl ?? WebSocket;
    const location = this.options.location ?? window.location;
    const socket = new WebSocketImpl(
      terminalWebSocketUrl(this.options.instanceId, this.options.csrfToken, location),
    );
    this.socket = socket;

    socket.onopen = () => {
      if (this.preferReconnect && this.sessionId) {
        this.send({ type: "reconnect", sessionId: this.sessionId });
        return;
      }
      this.send({
        type: "open",
        instanceId: this.options.instanceId,
        cols: this.cols,
        rows: this.rows,
        sessionName: this.options.sessionName,
        workingDirectory: this.options.workingDirectory,
      });
    };

    socket.onmessage = (event) => {
      if (typeof event.data !== "string") return;
      try {
        const frame = decodeFrame(event.data);
        switch (frame.type) {
          case "open":
            if (frame.sessionId) {
              this.sessionId = frame.sessionId;
            }
            this.preferReconnect = false;
            this.setState({ status: "connected" });
            break;
          case "output":
            this.options.onOutput?.(frame.data);
            break;
          case "exit":
            this.sessionId = undefined;
            this.setState({ status: "disconnected", detail: `exited ${frame.code}` });
            this.options.onExit?.(frame.code);
            break;
          case "error":
            this.setState({
              status: "error",
              detail: frame.message || frame.code,
            });
            break;
          default:
            break;
        }
      } catch (error) {
        this.setState({
          status: "error",
          detail: error instanceof Error ? error.message : "invalid frame",
        });
      }
    };

    socket.onerror = () => {
      this.setState({ status: "error", detail: "WebSocket error" });
    };

    socket.onclose = () => {
      if (this.intentionalClose) {
        this.setState({ status: "disconnected" });
        return;
      }
      if (this.state.status !== "error") {
        this.setState({ status: "disconnected" });
      }
    };
  }

  private disposeSocket(): void {
    if (!this.socket) return;
    this.socket.onopen = null;
    this.socket.onmessage = null;
    this.socket.onerror = null;
    this.socket.onclose = null;
    if (this.socket.readyState === WebSocket.OPEN || this.socket.readyState === WebSocket.CONNECTING) {
      this.socket.close();
    }
    this.socket = null;
  }

  private send(frame: Parameters<typeof encodeFrame>[0]): void {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) return;
    this.socket.send(encodeFrame(frame));
  }

  private setState(state: ConnectionState): void {
    this.state = state;
    this.options.onStateChange?.(state);
  }
}
