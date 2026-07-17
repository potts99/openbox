// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useState } from "react";
import type { AuditEvent, OpenBoxApi } from "../api/client";

interface AuditEventsPageProps {
  api: OpenBoxApi;
  onBack(): void;
}

type PageData =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; events: AuditEvent[] };

function formatWhen(value: string): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function AuditEventsPage({ api, onBack }: AuditEventsPageProps) {
  const [data, setData] = useState<PageData>({ status: "loading" });

  useEffect(() => {
    let active = true;
    void api.listAuditEvents(100)
      .then((events) => {
        if (active) setData({ status: "ready", events });
      })
      .catch((reason: unknown) => {
        if (active) {
          setData({
            status: "error",
            message: reason instanceof Error ? reason.message : "Audit events unavailable",
          });
        }
      });
    return () => { active = false; };
  }, [api]);

  return (
    <div className="console-layout">
      <a className="skip-link" href="#audit-main">Skip to audit events</a>
      <header className="console-header">
        <a className="wordmark" href="/" aria-label="OpenBox home"><span>OB</span> OpenBox</a>
        <nav aria-label="Primary navigation">
          <button className="nav-button" type="button" onClick={onBack}>Instances</button>
        </nav>
      </header>
      <div className="console-workspace">
        <main id="audit-main" tabIndex={-1}>
          <div className="page-heading">
            <div>
              <button className="link-button instance-back" type="button" onClick={onBack}>
                ← Instances
              </button>
              <h1>Audit events</h1>
              <p className="data-message">
                Policy and security decisions without payloads, DNS answers, or secrets.
              </p>
            </div>
          </div>

          {data.status === "loading" ? <p className="data-message" role="status">Loading…</p> : null}
          {data.status === "error" ? <p className="data-message is-error" role="alert">{data.message}</p> : null}

          {data.status === "ready" ? (
            data.events.length === 0 ? (
              <p className="data-message">No audit events yet.</p>
            ) : (
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th scope="col">When</th>
                      <th scope="col">Action</th>
                      <th scope="col">Outcome</th>
                      <th scope="col">Target</th>
                      <th scope="col">Details</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.events.map((event) => (
                      <tr key={event.id}>
                        <td>{formatWhen(event.createdAt)}</td>
                        <td>{event.action}</td>
                        <td>{event.outcome}</td>
                        <td>{event.targetType}/{event.targetId}</td>
                        <td>
                          {Object.entries(event.metadata)
                            .map(([key, value]) => `${key}=${value}`)
                            .join(" · ") || "—"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )
          ) : null}
        </main>
      </div>
      <footer><span>openbox</span><span>v0.01</span></footer>
    </div>
  );
}
