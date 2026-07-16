// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect } from "react";
import type { OperationSummary } from "../api/client";

interface OperationDrawerProps {
  open: boolean;
  operations: OperationSummary[];
  onClose(): void;
}

type StatusTone = "pending" | "running" | "succeeded" | "failed" | "unknown";

function statusTone(status: string): StatusTone {
  switch (status) {
    case "pending":
      return "pending";
    case "running":
      return "running";
    case "succeeded":
    case "completed":
      return "succeeded";
    case "failed":
      return "failed";
    default:
      return "unknown";
  }
}

function formatAction(action: string): string {
  return action.replaceAll(".", " · ");
}

function formatWhen(value: string): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function shortenTarget(target: string): string {
  if (target.length <= 28) return target;
  return `${target.slice(0, 12)}…${target.slice(-10)}`;
}

function summarize(operations: OperationSummary[]) {
  let active = 0;
  let failed = 0;
  for (const operation of operations) {
    const tone = statusTone(operation.status);
    if (tone === "pending" || tone === "running") active += 1;
    if (tone === "failed") failed += 1;
  }
  return { active, failed, total: operations.length };
}

export function OperationDrawer({ open, operations, onClose }: OperationDrawerProps) {
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open, onClose]);

  if (!open) return null;

  const summary = summarize(operations);

  return (
    <>
      <button
        type="button"
        className="operation-drawer-backdrop"
        aria-label="Dismiss operations"
        tabIndex={-1}
        onClick={onClose}
      />
      <aside
        className="operation-drawer is-open"
        role="complementary"
        aria-label="Operations"
        aria-modal="true"
      >
        <div className="drawer-heading">
          <div>
            <h2>Operations</h2>
            <p className="drawer-summary">
              {summary.total === 0
                ? "Durable queue is idle"
                : [
                    `${summary.total} total`,
                    summary.active > 0 ? `${summary.active} active` : null,
                    summary.failed > 0 ? `${summary.failed} failed` : null,
                  ].filter(Boolean).join(" · ")}
            </p>
          </div>
          <button
            className="icon-button"
            type="button"
            onClick={onClose}
            aria-label="Close operations"
            autoFocus
          >
            ×
          </button>
        </div>
        <div className="operation-body" aria-live="polite">
          {operations.length === 0 ? (
            <div className="drawer-empty">
              <p>No operations recorded</p>
              <span>Lifecycle and software changes will appear here as they run.</span>
            </div>
          ) : (
            <ol className="operation-list">
              {operations.map((operation) => {
                const tone = statusTone(operation.status);
                return (
                  <li key={operation.id}>
                    <span className={`status-dot status-${tone}`} aria-hidden="true" />
                    <div className="operation-copy">
                      <strong>{formatAction(operation.action)}</strong>
                      <span className="operation-target" title={operation.target}>
                        {shortenTarget(operation.target)}
                      </span>
                      <time dateTime={operation.updatedAt}>{formatWhen(operation.updatedAt)}</time>
                    </div>
                    <span className={`status-pill status-pill-${tone}`}>{operation.status}</span>
                  </li>
                );
              })}
            </ol>
          )}
        </div>
      </aside>
    </>
  );
}
