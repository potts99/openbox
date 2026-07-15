// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useRef } from "react";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";

export interface TerminalSurfaceHandle {
  write(data: Uint8Array): void;
  focus(): void;
  dispose(): void;
}

export interface TerminalSurfaceProps {
  onData(data: string): void;
  onResize(cols: number, rows: number): void;
  onReady?(handle: TerminalSurfaceHandle): void;
}

/** Lightweight surface used in unit tests (no canvas / xterm). */
export function TestTerminalSurface({ onData, onResize, onReady }: TerminalSurfaceProps) {
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

/** Production xterm.js surface with fit-on-resize. */
export function XtermTerminalSurface({ onData, onResize, onReady }: TerminalSurfaceProps) {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const term = new Terminal({
      cursorBlink: true,
      fontFamily: 'ui-monospace, "SFMono-Regular", Menlo, monospace',
      theme: {
        background: "#171714",
        foreground: "#f5f1e7",
        cursor: "#e64e20",
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(container);
    fit.fit();
    onResize(term.cols, term.rows);

    const dataDisposable = term.onData((data) => onData(data));
    const resizeDisposable = term.onResize(({ cols, rows }) => onResize(cols, rows));

    const handle: TerminalSurfaceHandle = {
      write(data) {
        term.write(data);
      },
      focus() {
        term.focus();
      },
      dispose() {
        dataDisposable.dispose();
        resizeDisposable.dispose();
        term.dispose();
      },
    };
    onReady?.(handle);

    const observer = new ResizeObserver(() => {
      fit.fit();
    });
    observer.observe(container);

    return () => {
      observer.disconnect();
      handle.dispose();
    };
  }, [onData, onReady, onResize]);

  return (
    <div
      className="terminal-surface"
      role="application"
      aria-label="Instance terminal"
      ref={containerRef}
      tabIndex={0}
    />
  );
}
