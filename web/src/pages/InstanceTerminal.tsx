// SPDX-License-Identifier: AGPL-3.0-only

import { useCallback, useEffect, useRef, useState } from "react";
import type { ReactElement } from "react";
import type { ConnectionState } from "../terminal/session";
import { TerminalSession } from "../terminal/session";
import { encodePasteText } from "../terminal/copyPaste";
import type { TerminalSurfaceHandle, TerminalSurfaceProps } from "../terminal/TerminalSurface";
import { XtermTerminalSurface } from "../terminal/TerminalSurface";

export interface InstanceTerminalProps {
  instanceId: string;
  instanceName: string;
  csrfToken: string;
  onBack(): void;
  WebSocketImpl?: typeof WebSocket;
  /** Override the PTY surface (tests inject TestTerminalSurface). */
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

export function InstanceTerminal({
  instanceId,
  instanceName,
  csrfToken,
  onBack,
  WebSocketImpl,
  Surface = XtermTerminalSurface,
}: InstanceTerminalProps) {
  const [connection, setConnection] = useState<ConnectionState>({ status: "connecting" });
  const [persistent, setPersistent] = useState(false);
  const [sessionName, setSessionName] = useState("");
  const sessionRef = useRef<TerminalSession | null>(null);
  const surfaceRef = useRef<TerminalSurfaceHandle | null>(null);
  const colsRef = useRef(80);
  const rowsRef = useRef(24);
  const persistentRef = useRef(false);
  const sessionNameRef = useRef("");
  const openedSessionNameRef = useRef<string | undefined>(undefined);

  const attachSession = useCallback(() => {
    sessionRef.current?.dispose();
    const named = persistentRef.current ? sessionNameRef.current.trim() : "";
    openedSessionNameRef.current = named || undefined;
    const session = new TerminalSession({
      instanceId,
      csrfToken,
      cols: colsRef.current,
      rows: rowsRef.current,
      sessionName: named || undefined,
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

  const onData = useCallback((data: string) => {
    sessionRef.current?.sendInput(encodePasteText(data));
  }, []);

  const onResize = useCallback((cols: number, rows: number) => {
    colsRef.current = cols;
    rowsRef.current = rows;
    sessionRef.current?.resize(cols, rows);
  }, []);

  const onReady = useCallback((handle: TerminalSurfaceHandle) => {
    surfaceRef.current = handle;
  }, []);

  const canReconnect = connection.status === "disconnected" || connection.status === "error";
  const canTerminate = connection.status === "connected";

  const onReconnect = useCallback(() => {
    const desired = persistentRef.current ? sessionNameRef.current.trim() : "";
    const desiredOrUndef = desired || undefined;
    if (!sessionRef.current || desiredOrUndef !== openedSessionNameRef.current) {
      attachSession();
      return;
    }
    sessionRef.current.reconnect();
  }, [attachSession]);

  const onPersistentChange = useCallback((checked: boolean) => {
    persistentRef.current = checked;
    setPersistent(checked);
  }, []);

  const onSessionNameChange = useCallback((value: string) => {
    sessionNameRef.current = value;
    setSessionName(value);
  }, []);

  return (
    <div className="terminal-page">
      <a className="skip-link" href="#terminal-main">Skip to terminal</a>
      <header className="terminal-toolbar">
        <button type="button" onClick={onBack}>Back to instances</button>
        <div>
          <p className="eyebrow">Instance terminal</p>
          <h1 id="terminal-heading">{instanceName}</h1>
        </div>
        <div className="terminal-actions">
          <fieldset className="terminal-session-opts">
            <legend className="visually-hidden">Persistent session</legend>
            <label className="terminal-persistent">
              <input
                type="checkbox"
                checked={persistent}
                onChange={(event) => onPersistentChange(event.target.checked)}
              />
              Persistent session
            </label>
            <label className="terminal-session-name">
              <span className="visually-hidden">Session name</span>
              <input
                type="text"
                value={sessionName}
                onChange={(event) => onSessionNameChange(event.target.value)}
                disabled={!persistent}
                placeholder="session name"
                autoComplete="off"
                spellCheck={false}
                aria-label="Session name"
              />
            </label>
          </fieldset>
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
      <main id="terminal-main" className="terminal-main" aria-labelledby="terminal-heading">
        <Surface onData={onData} onResize={onResize} onReady={onReady} />
        <p className="terminal-hint">
          Copy uses the terminal selection and the system clipboard. Paste (Ctrl/Cmd+V) sends
          clipboard text to the instance PTY.
        </p>
      </main>
    </div>
  );
}
