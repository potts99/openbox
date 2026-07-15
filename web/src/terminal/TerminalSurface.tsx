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
