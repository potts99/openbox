// SPDX-License-Identifier: AGPL-3.0-only

import { useState } from "react";
import type { ArtifactSummary } from "../api/client";

interface ArtifactsProps {
  artifacts: ArtifactSummary[];
  pending?: boolean;
  error?: string;
  onUpload(path: string, file: File): void;
  onDownload(artifact: ArtifactSummary): void;
}

export function Artifacts({ artifacts, pending = false, error, onUpload, onDownload }: ArtifactsProps) {
  const [path, setPath] = useState("");
  const [file, setFile] = useState<File | null>(null);

  return (
    <section className="instance-detail" aria-labelledby="artifacts-heading">
      <div className="ledger-header">
        <h2 id="artifacts-heading">Artifacts</h2>
        <p className="lede">Upload results and download them without SSH.</p>
      </div>
      <div className="action-row">
        <input aria-label="Artifact path" value={path} placeholder="results/summary.json" onChange={(event) => setPath(event.target.value)} />
        <input aria-label="Artifact file" type="file" onChange={(event) => setFile(event.target.files?.[0] ?? null)} />
        <button
          type="button"
          className="button"
          disabled={pending || !path.trim() || file === null}
          onClick={() => {
            if (file) onUpload(path.trim(), file);
          }}
        >
          {pending ? "Uploading…" : "Upload"}
        </button>
      </div>
      {artifacts.length === 0 ? <p className="data-message">No artifacts yet.</p> : (
        <ul className="plain-list">
          {artifacts.map((artifact) => (
            <li key={artifact.id}>
              <code>{artifact.path}</code>
              <span> {artifact.sizeBytes} bytes</span>
              <div className="action-row">
                <button type="button" className="button" disabled={pending} onClick={() => onDownload(artifact)}>Download</button>
              </div>
            </li>
          ))}
        </ul>
      )}
      {error ? <p className="data-message is-error" role="alert">{error}</p> : null}
    </section>
  );
}
