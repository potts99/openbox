// SPDX-License-Identifier: AGPL-3.0-only

import type { SnapshotSummary } from "../api/client";

interface CheckpointsProps {
  snapshots: SnapshotSummary[];
  pending?: boolean;
  error?: string;
  warnings?: string[];
  storageNote?: string;
  cloneSourceInstanceId?: string;
  cloneSourceSnapshotId?: string;
  onCreate(name: string): void;
  onRestore(snapshotId: string, name: string): void;
  onDelete(snapshotId: string): void;
  onClone(name: string): void;
}

export function Checkpoints({
  snapshots,
  pending = false,
  error,
  warnings,
  storageNote,
  cloneSourceInstanceId,
  cloneSourceSnapshotId,
  onCreate,
  onRestore,
  onDelete,
  onClone,
}: CheckpointsProps) {
  return (
    <section className="instance-detail" aria-labelledby="checkpoints-heading">
      <div className="ledger-header">
        <h2 id="checkpoints-heading">Checkpoints</h2>
        <p className="lede">Disk-only snapshots. Restore creates a new instance; clone copies the live source.</p>
      </div>

      {(cloneSourceInstanceId || cloneSourceSnapshotId) ? (
        <p className="data-message">
          Derived from
          {cloneSourceInstanceId ? ` instance ${cloneSourceInstanceId}` : ""}
          {cloneSourceSnapshotId ? ` checkpoint ${cloneSourceSnapshotId}` : ""}
          . Guest files may retain secrets; rotate credentials after fan-out.
        </p>
      ) : null}

      <div className="action-row">
        <button
          type="button"
          className="button"
          disabled={pending}
          onClick={() => {
            const name = window.prompt("Checkpoint name");
            if (name?.trim()) onCreate(name.trim());
          }}
        >
          Create checkpoint
        </button>
        <button
          type="button"
          className="button"
          disabled={pending}
          onClick={() => {
            const name = window.prompt("New instance name for live clone");
            if (name?.trim()) onClone(name.trim());
          }}
        >
          Clone live instance
        </button>
      </div>

      {snapshots.length === 0 ? (
        <p className="data-message">No checkpoints yet.</p>
      ) : (
        <ul className="plain-list">
          {snapshots.map((snapshot) => (
            <li key={snapshot.id}>
              <code>{snapshot.name}</code>
              <span> {snapshot.id}</span>
              {!snapshot.ready ? <span> (creating…)</span> : null}
              <div className="action-row">
                <button
                  type="button"
                  className="button"
                  disabled={pending || !snapshot.ready}
                  onClick={() => {
                    const name = window.prompt("New instance name for restore");
                    if (name?.trim()) onRestore(snapshot.id, name.trim());
                  }}
                >
                  Restore as new
                </button>
                <button
                  type="button"
                  className="button"
                  disabled={pending || !snapshot.ready}
                  onClick={() => {
                    if (window.confirm(`Delete checkpoint "${snapshot.name}"? This permanently removes the recovery point.`)) {
                      onDelete(snapshot.id);
                    }
                  }}
                >
                  Delete
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}

      {storageNote ? <p className="data-message">{storageNote}</p> : null}
      {warnings?.map((warning) => (
        <p key={warning} className="data-message is-error" role="status">{warning}</p>
      ))}
      {error ? <p className="data-message is-error" role="alert">{error}</p> : null}
    </section>
  );
}
