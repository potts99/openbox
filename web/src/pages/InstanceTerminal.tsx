// SPDX-License-Identifier: AGPL-3.0-only

import { useCallback, useEffect, useRef, useState } from "react";
import type { FocusEvent, ReactElement } from "react";
import type { ConnectionState } from "../terminal/session";
import { TerminalSession } from "../terminal/session";
import type { TerminalSurfaceHandle, TerminalSurfaceProps } from "../terminal/TerminalSurface";
import { XtermTerminalSurface } from "../terminal/TerminalSurface";

export interface InstanceTerminalProps {
  instanceId: string;
  instanceName: string;
  csrfToken: string;
  onBack(): void;
  WebSocketImpl?: typeof WebSocket;
  /** Override the PTY surface (tests inject a lightweight stub). */
  Surface?: (props: TerminalSurfaceProps) => ReactElement;
}

function statusLabel(state: ConnectionState): string {
  switch (state.status) {
    case "connecting":
      return "Connecting";
    case "connected":
      return "Connected";
    case "disconnected":
      return state.detail ? `Disconnected (${state.detail})` : "Disconnected";
    case "error":
      return state.detail ? `Error: ${state.detail}` : "Error";
    default: {
      const _exhaustive: never = state.status;
      return _exhaustive;
    }
  }
}

const textEncoder = new TextEncoder();
const PERSISTENT_SESSION_NAME = "main";
const CHROME_HIDE_MS = 1500;

export function InstanceTerminal({
  instanceId,
  instanceName,
  csrfToken,
  onBack,
  WebSocketImpl,
  Surface = XtermTerminalSurface,
}: InstanceTerminalProps) {
  const [connection, setConnection] = useState<ConnectionState>({ status: "connecting" });
  const [chromeRevealed, setChromeRevealed] = useState(true);
  const sessionRef = useRef<TerminalSession | null>(null);
  const surfaceRef = useRef<TerminalSurfaceHandle | null>(null);
  const colsRef = useRef(80);
  const rowsRef = useRef(24);
  const chromeFocusRef = useRef(false);
  const surfaceHostRef = useRef<HTMLElement | null>(null);
  const chromeForced = connection.status !== "connected";
  const chromeVisible = chromeForced || chromeRevealed;

  const attachSession = useCallback(() => {
    sessionRef.current?.dispose();
    const session = new TerminalSession({
      instanceId,
      csrfToken,
      cols: colsRef.current,
      rows: rowsRef.current,
      sessionName: PERSISTENT_SESSION_NAME,
      WebSocketImpl,
      onStateChange: setConnection,
      onOutput: (data) => surfaceRef.current?.write(data),
    });
    sessionRef.current = session;
    session.connect();
  }, [WebSocketImpl, csrfToken, instanceId]);

  useEffect(() => {
    attachSession();
    return () => {
      sessionRef.current?.dispose();
      sessionRef.current = null;
    };
  }, [attachSession]);

  useEffect(() => {
    const { classList } = document.body;
    classList.add("terminal-open");
    return () => classList.remove("terminal-open");
  }, []);

  useEffect(() => {
    if (connection.status !== "connected") {
      setChromeRevealed(true);
      return;
    }
    if (chromeFocusRef.current) return;
    const timer = window.setTimeout(() => {
      if (!chromeFocusRef.current) {
        setChromeRevealed(false);
        surfaceHostRef.current?.querySelector<HTMLElement>(".terminal-surface")?.focus();
      }
    }, CHROME_HIDE_MS);
    return () => window.clearTimeout(timer);
  }, [connection.status]);

  const revealChrome = useCallback(() => {
    setChromeRevealed(true);
  }, []);

  const hideChromeIfConnected = useCallback(() => {
    if (connection.status === "connected" && !chromeFocusRef.current) {
      setChromeRevealed(false);
      surfaceHostRef.current?.querySelector<HTMLElement>(".terminal-surface")?.focus();
    }
  }, [connection.status]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      if (!chromeVisible) {
        event.preventDefault();
        setChromeRevealed(true);
        return;
      }
      if (connection.status === "connected") {
        event.preventDefault();
        setChromeRevealed(false);
        surfaceHostRef.current?.querySelector<HTMLElement>(".terminal-surface")?.focus();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [chromeVisible, connection.status]);

  const onData = useCallback((data: string) => {
    sessionRef.current?.sendInput(textEncoder.encode(data));
  }, []);

  const onResize = useCallback((cols: number, rows: number) => {
    colsRef.current = cols;
    rowsRef.current = rows;
    sessionRef.current?.resize(cols, rows);
  }, []);

  const onReady = useCallback((handle: TerminalSurfaceHandle) => {
    surfaceRef.current = handle;
  }, []);

  const onChromeFocusCapture = useCallback(() => {
    chromeFocusRef.current = true;
    setChromeRevealed(true);
  }, []);

  const onChromeBlurCapture = useCallback((event: FocusEvent<HTMLElement>) => {
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
      chromeFocusRef.current = false;
      hideChromeIfConnected();
    }
  }, [hideChromeIfConnected]);

  const canReconnect = connection.status === "disconnected" || connection.status === "error";
  const canTerminate = connection.status === "connected";

  const onReconnect = useCallback(() => {
    if (!sessionRef.current) {
      attachSession();
      return;
    }
    sessionRef.current.reconnect();
  }, [attachSession]);

  return (
    <div className="terminal-page">
      <a className="skip-link" href="#terminal-main">Skip to terminal</a>
      <button
        type="button"
        className="terminal-reveal"
        aria-label="Show terminal controls"
        hidden={chromeVisible}
        onClick={revealChrome}
        onMouseEnter={revealChrome}
      />
      <header
        className={`terminal-toolbar${chromeVisible ? "" : " terminal-toolbar--hidden"}`}
        onFocusCapture={onChromeFocusCapture}
        onBlurCapture={onChromeBlurCapture}
        onMouseEnter={revealChrome}
      >
        <button type="button" onClick={onBack}>Back to instance</button>
        <div>
          <p className="eyebrow">Instance terminal</p>
          <h1 id="terminal-heading">{instanceName}</h1>
        </div>
        <div className="terminal-actions">
          <p className="terminal-hint">Copy/paste uses the system clipboard.</p>
          <p
            className="terminal-connection"
            role="status"
            aria-live="polite"
            aria-label="Terminal connection state"
          >
            {statusLabel(connection)}
          </p>
          {canReconnect ? (
            <button type="button" onClick={onReconnect}>Reconnect</button>
          ) : null}
          <button
            type="button"
            onClick={() => sessionRef.current?.terminate()}
            disabled={!canTerminate}
          >
            Terminate
          </button>
        </div>
      </header>
      <main
        id="terminal-main"
        className="terminal-main"
        aria-labelledby="terminal-heading"
        ref={surfaceHostRef}
      >
        <Surface onData={onData} onResize={onResize} onReady={onReady} />
      </main>
    </div>
  );
}
