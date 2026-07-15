// SPDX-License-Identifier: AGPL-3.0-only

import { afterEach, describe, expect, it, vi } from "vitest";
import { TerminalSession } from "./session";
import { encodeFrame } from "./protocol";

class FakeWebSocket {
  static OPEN = 1;
  static CLOSED = 3;
  static instances: FakeWebSocket[] = [];

  readonly url: string;
  readyState = 0;
  onopen: ((event: Event) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  readonly sent: string[] = [];

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }

  send(data: string) {
    this.sent.push(data);
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.({ code: 1000, reason: "", wasClean: true } as CloseEvent);
  }

  open() {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.(new Event("open"));
  }

  message(data: string) {
    this.onmessage?.({ data } as MessageEvent);
  }
}

afterEach(() => {
  FakeWebSocket.instances = [];
});

describe("TerminalSession", () => {
  it("connects with csrf query, opens a session, and reports accessible states", async () => {
    const states: string[] = [];
    const session = new TerminalSession({
      instanceId: "inst-1",
      csrfToken: "csrf-secret",
      cols: 80,
      rows: 24,
      WebSocketImpl: FakeWebSocket as unknown as typeof WebSocket,
      onStateChange: (state) => states.push(state.status),
    });

    session.connect();
    expect(states.at(-1)).toBe("connecting");
    expect(FakeWebSocket.instances[0]?.url).toContain("/v1/instances/inst-1/terminal?");
    expect(FakeWebSocket.instances[0]?.url).toContain("csrf=csrf-secret");

    FakeWebSocket.instances[0]?.open();
    expect(JSON.parse(FakeWebSocket.instances[0]?.sent[0] ?? "{}")).toMatchObject({
      type: "open",
      instance_id: "inst-1",
      cols: 80,
      rows: 24,
    });

    FakeWebSocket.instances[0]?.message(encodeFrame({
      type: "open",
      instanceId: "inst-1",
      cols: 80,
      rows: 24,
    }));
    expect(states.at(-1)).toBe("connected");

    FakeWebSocket.instances[0]?.close();
    expect(states.at(-1)).toBe("disconnected");
  });

  it("reconnects after disconnect and can terminate with TERM", () => {
    const session = new TerminalSession({
      instanceId: "inst-1",
      csrfToken: "csrf",
      cols: 80,
      rows: 24,
      WebSocketImpl: FakeWebSocket as unknown as typeof WebSocket,
    });

    session.connect();
    FakeWebSocket.instances[0]?.open();
    FakeWebSocket.instances[0]?.message(encodeFrame({
      type: "open",
      instanceId: "inst-1",
      cols: 80,
      rows: 24,
    }));
    FakeWebSocket.instances[0]?.close();

    session.reconnect();
    expect(FakeWebSocket.instances).toHaveLength(2);
    FakeWebSocket.instances[1]?.open();
    expect(JSON.parse(FakeWebSocket.instances[1]?.sent[0] ?? "{}").type).toBe("open");

    FakeWebSocket.instances[1]?.message(encodeFrame({
      type: "open",
      instanceId: "inst-1",
      cols: 80,
      rows: 24,
    }));
    session.terminate();
    expect(JSON.parse(FakeWebSocket.instances[1]?.sent.at(-1) ?? "{}")).toEqual({
      type: "signal",
      signal: "TERM",
    });
  });

  it("forwards input and resize frames", () => {
    const onOutput = vi.fn();
    const session = new TerminalSession({
      instanceId: "inst-1",
      csrfToken: "csrf",
      cols: 80,
      rows: 24,
      WebSocketImpl: FakeWebSocket as unknown as typeof WebSocket,
      onOutput,
    });

    session.connect();
    FakeWebSocket.instances[0]?.open();
    FakeWebSocket.instances[0]?.message(encodeFrame({
      type: "open",
      instanceId: "inst-1",
      cols: 80,
      rows: 24,
    }));

    session.sendInput(new TextEncoder().encode("ls\n"));
    expect(JSON.parse(FakeWebSocket.instances[0]?.sent.at(-1) ?? "{}")).toEqual({
      type: "input",
      data: btoa("ls\n"),
    });

    session.resize(100, 30);
    expect(JSON.parse(FakeWebSocket.instances[0]?.sent.at(-1) ?? "{}")).toEqual({
      type: "resize",
      cols: 100,
      rows: 30,
    });

    FakeWebSocket.instances[0]?.message(encodeFrame({
      type: "output",
      data: new TextEncoder().encode("ok"),
    }));
    expect(new TextDecoder().decode(onOutput.mock.calls[0][0])).toBe("ok");
  });
});
