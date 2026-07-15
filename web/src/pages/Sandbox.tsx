// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useState } from "react";

export interface SandboxStatusProps {
  expiresAt?: string;
  errorCode?: string;
  errorStage?: string;
  egressPolicy?: string;
  now?: Date;
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
  egressPolicy = "default",
  now,
}: SandboxStatusProps) {
  const [clock, setClock] = useState(() => now ?? new Date());

  useEffect(() => {
    if (now) {
      setClock(now);
      return;
    }
    const timer = window.setInterval(() => setClock(new Date()), 1000);
    return () => window.clearInterval(timer);
  }, [now]);

  return (
    <section className="instance-detail sandbox-status" aria-labelledby="sandbox-status-heading">
      <div className="ledger-header">
        <h2 id="sandbox-status-heading">Sandbox</h2>
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
    </section>
  );
}
