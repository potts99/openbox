// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useState } from "react";

export interface SandboxStatusProps {
  expiresAt?: string;
  errorCode?: string;
  errorStage?: string;
  egressPolicy?: string;
  isolationNote?: string;
  now?: Date;
  extendPending?: boolean;
  extendError?: string;
  onExtend?(durationSeconds: number): void;
}

function formatCountdown(expiresAt: string, now: Date): string {
  const expires = new Date(expiresAt);
  if (Number.isNaN(expires.getTime())) return expiresAt;
  const remainingMs = expires.getTime() - now.getTime();
  if (remainingMs <= 0) return "expired";
  const totalSeconds = Math.floor(remainingMs / 1000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0 && minutes > 0) return `${hours}h${minutes}m`;
  if (hours > 0) return `${hours}h`;
  if (minutes > 0 && seconds > 0) return `${minutes}m${seconds}s`;
  if (minutes > 0) return `${minutes}m`;
  return `${seconds}s`;
}

export function SandboxStatus({
  expiresAt,
  errorCode,
  errorStage,
  egressPolicy = "restricted",
  isolationNote,
  now,
  extendPending = false,
  extendError,
  onExtend,
}: SandboxStatusProps) {
  const [liveClock, setLiveClock] = useState(() => new Date());
  const clock = now ?? liveClock;

  useEffect(() => {
    if (now) return;
    const timer = window.setInterval(() => setLiveClock(new Date()), 1000);
    return () => window.clearInterval(timer);
  }, [now]);

  return (
    <section className="instance-detail sandbox-status" aria-labelledby="sandbox-status-heading">
      <div className="ledger-header">
        <h2 id="sandbox-status-heading">Sandbox</h2>
        {onExtend ? (
          <button
            type="button"
            className="btn"
            disabled={extendPending}
            onClick={() => onExtend(3600)}
          >
            {extendPending ? "Extending…" : "Extend 1h"}
          </button>
        ) : null}
      </div>
      <dl className="detail-grid">
        <div>
          <dt>Egress</dt>
          <dd>{egressPolicy}</dd>
        </div>
        {expiresAt ? (
          <>
            <div>
              <dt>Expires</dt>
              <dd>{new Date(expiresAt).toLocaleString()}</dd>
            </div>
            <div>
              <dt>Countdown</dt>
              <dd aria-live="polite">{formatCountdown(expiresAt, clock)}</dd>
            </div>
          </>
        ) : null}
        {isolationNote ? (
          <div className="detail-span">
            <dt>Isolation</dt>
            <dd>{isolationNote}</dd>
          </div>
        ) : null}
        {errorCode ? (
          <div className="detail-span">
            <dt>Cleanup failure</dt>
            <dd className="is-error">
              {errorCode}
              {errorStage ? ` at ${errorStage}` : ""}
            </dd>
          </div>
        ) : null}
      </dl>
      {extendError ? <p className="data-message is-error" role="alert">{extendError}</p> : null}
      <p className="data-message">
        Browser terminal is disabled for sandboxes. Use <code>openbox sandbox exec</code> or the API.
      </p>
    </section>
  );
}
