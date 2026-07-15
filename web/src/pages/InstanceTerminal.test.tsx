// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useRef, type ReactElement } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { afterEach, describe, expect, it, vi } from "vitest";
import { InstanceTerminal } from "./InstanceTerminal";
import { encodeFrame } from "../terminal/protocol";
import type { TerminalSurfaceHandle, TerminalSurfaceProps } from "../terminal/TerminalSurface";

/** Lightweight surface used in unit tests (no canvas / xterm). */
function TestTerminalSurface({ onData, onResize, onReady }: TerminalSurfaceProps): ReactElement {
  const handleRef = useRef<TerminalSurfaceHandle>({
    write() { /* test surface ignores PTY output */ },
    focus() { /* focus is handled by the host element */ },
    dispose() { /* nothing to dispose */ },
  });

  useEffect(() => {
    onResize(80, 24);
    onReady?.(handleRef.current);
    const onWindowResize = () => onResize(100, 30);
    window.addEventListener("resize", onWindowResize);
    return () => window.removeEventListener("resize", onWindowResize);
  }, [onData, onReady, onResize]);

  return (
    <div
      className="terminal-surface"
      role="application"
      aria-label="Instance terminal"
      tabIndex={0}
      onKeyDown={(event) => {
        if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
          event.preventDefault();
          onData(event.key);
        } else if (event.key === "Enter") {
          event.preventDefault();
          onData("\r");
        } else if (event.key === "Backspace") {
          event.preventDefault();
          onData("\x7f");
        }
      }}
      onPaste={(event) => {
        event.preventDefault();
        const text = event.clipboardData?.getData("text") ?? "";
        if (text) onData(text);
      }}
    />
  );
}

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

function openConnectedSocket() {
  const socket = FakeWebSocket.instances.at(-1);
  socket?.open();
  socket?.message(encodeFrame({
    type: "open",
    instanceId: "box-1",
    cols: 80,
    rows: 24,
  }));
  return socket;
}

function renderTerminal(onBack: () => void = () => undefined) {
  return render(
    <InstanceTerminal
      instanceId="box-1"
      instanceName="workbench"
      csrfToken="csrf-token"
      WebSocketImpl={FakeWebSocket as unknown as typeof WebSocket}
      Surface={TestTerminalSurface}
      onBack={onBack}
    />,
  );
}

describe("InstanceTerminal", () => {
  it("announces connection state for screen readers and exposes reconnect after disconnect", async () => {
    const user = userEvent.setup();
    renderTerminal();

    expect(screen.getByRole("status", { name: "Terminal connection state" })).toHaveTextContent(/connecting/i);
    openConnectedSocket();
    expect(await screen.findByRole("status", { name: "Terminal connection state" })).toHaveTextContent(/connected/i);

    FakeWebSocket.instances[0]?.close();
    expect(await screen.findByRole("status", { name: "Terminal connection state" })).toHaveTextContent(/disconnected/i);

    await user.click(screen.getByRole("button", { name: "Reconnect" }));
    expect(FakeWebSocket.instances).toHaveLength(2);
    openConnectedSocket();
    expect(await screen.findByRole("status", { name: "Terminal connection state" })).toHaveTextContent(/connected/i);
  });

  it("sends keyboard input and resize updates through the session", async () => {
    const user = userEvent.setup();
    renderTerminal();
    openConnectedSocket();
    await screen.findByRole("status", { name: "Terminal connection state" });

    const surface = screen.getByRole("application", { name: "Instance terminal" });
    surface.focus();
    await user.keyboard("echo hi");

    await waitFor(() => {
      const payloads = FakeWebSocket.instances[0]?.sent.slice(1) ?? [];
      expect(payloads.some((payload) => {
        const frame = JSON.parse(payload) as { type?: string; data?: string };
        return frame.type === "input" && typeof frame.data === "string" && atob(frame.data).includes("e");
      })).toBe(true);
    });

    window.dispatchEvent(new Event("resize"));
    await waitFor(() => {
      expect(FakeWebSocket.instances[0]?.sent.some((payload) => JSON.parse(payload).type === "resize")).toBe(true);
    });
  });

  it("terminates the session explicitly and supports paste into the PTY", async () => {
    const user = userEvent.setup();
    renderTerminal();
    openConnectedSocket();

    const surface = screen.getByRole("application", { name: "Instance terminal" });
    surface.focus();
    await user.paste("paste-me");

    await waitFor(() => {
      expect(FakeWebSocket.instances[0]?.sent.some((payload) => {
        const frame = JSON.parse(payload) as { type?: string; data?: string };
        return frame.type === "input" && frame.data === btoa("paste-me");
      })).toBe(true);
    });

    await user.click(screen.getByRole("button", { name: "Terminate" }));
    expect(JSON.parse(FakeWebSocket.instances[0]?.sent.at(-1) ?? "{}")).toEqual({
      type: "signal",
      signal: "TERM",
    });
  });

  it("links back to the instance list and has no axe violations", async () => {
    const onBack = vi.fn();
    const view = renderTerminal(onBack);
    openConnectedSocket();
    await screen.findByRole("heading", { name: /workbench/i });

    expect((await axe.run(view.container)).violations).toEqual([]);
    await userEvent.setup().click(screen.getByRole("button", { name: "Back to instance" }));
    expect(onBack).toHaveBeenCalledOnce();
  });

  it("always opens the persistent main session and has no session name controls", async () => {
    renderTerminal();

    expect(screen.queryByRole("checkbox", { name: /persistent session/i })).toBeNull();
    expect(screen.queryByRole("textbox", { name: "Session name" })).toBeNull();

    const socket = FakeWebSocket.instances.at(-1);
    socket?.open();
    expect(JSON.parse(socket?.sent[0] ?? "{}")).toMatchObject({
      type: "open",
      session_name: "main",
    });
    socket?.message(encodeFrame({
      type: "open",
      instanceId: "box-1",
      cols: 80,
      rows: 24,
      sessionName: "main",
    }));
    expect(await screen.findByRole("status", { name: "Terminal connection state" })).toHaveTextContent(/connected/i);
  });

  it("auto-hides chrome after connect and reveals it on Escape or disconnect", async () => {
    const user = userEvent.setup();
    renderTerminal();

    const toolbar = document.querySelector(".terminal-toolbar");
    expect(toolbar).not.toHaveClass("terminal-toolbar--hidden");

    openConnectedSocket();
    expect(await screen.findByRole("status", { name: "Terminal connection state" })).toHaveTextContent(/connected/i);

    await waitFor(() => {
      expect(toolbar).toHaveClass("terminal-toolbar--hidden");
    }, { timeout: 2500 });

    await user.keyboard("{Escape}");
    expect(toolbar).not.toHaveClass("terminal-toolbar--hidden");

    await user.keyboard("{Escape}");
    expect(toolbar).toHaveClass("terminal-toolbar--hidden");

    FakeWebSocket.instances[0]?.close();
    expect(await screen.findByRole("status", { name: "Terminal connection state" })).toHaveTextContent(/disconnected/i);
    expect(toolbar).not.toHaveClass("terminal-toolbar--hidden");
    expect(screen.getByRole("button", { name: "Reconnect" })).toBeInTheDocument();
  });

  it("reveals chrome when the top affordance is activated", async () => {
    const user = userEvent.setup();
    renderTerminal();
    openConnectedSocket();
    expect(await screen.findByRole("status", { name: "Terminal connection state" })).toHaveTextContent(/connected/i);

    await waitFor(() => {
      expect(document.querySelector(".terminal-toolbar")).toHaveClass("terminal-toolbar--hidden");
    }, { timeout: 2500 });

    await user.click(screen.getByRole("button", { name: "Show terminal controls" }));
    expect(document.querySelector(".terminal-toolbar")).not.toHaveClass("terminal-toolbar--hidden");
  });
});
