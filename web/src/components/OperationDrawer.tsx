// SPDX-License-Identifier: AGPL-3.0-only

import type { OperationSummary } from "../api/client";

interface OperationDrawerProps {
  open: boolean;
  operations: OperationSummary[];
  onClose(): void;
}

export function OperationDrawer({ open, operations, onClose }: OperationDrawerProps) {
  if (!open) return null;
  return (
    <aside className="operation-drawer is-open" aria-label="Operations" onKeyDown={(event) => {
      if (event.key === "Escape") onClose();
    }}>
      <div className="drawer-heading">
        <div>
          <p className="eyebrow">Durable queue</p>
          <h2>Operations</h2>
        </div>
        <button className="icon-button" type="button" onClick={onClose} aria-label="Close operations" autoFocus>×</button>
      </div>
      <div className="operation-body" aria-live="polite">
        {operations.length === 0 ? <p className="drawer-empty">No operations recorded.</p> : null}
        {operations.length > 0 ? (
          <ol className="operation-list">
            {operations.map((operation) => (
              <li key={operation.id}>
                <span className={`status-dot status-${operation.status}`} aria-hidden="true" />
                <div><strong>{operation.target}</strong><span>{operation.action}</span></div>
                <small>{operation.status}</small>
              </li>
            ))}
          </ol>
        ) : null}
      </div>
    </aside>
  );
}
