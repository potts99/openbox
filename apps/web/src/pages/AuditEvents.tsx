// SPDX-License-Identifier: AGPL-3.0-only

import { useCallback, useEffect, useMemo, useState } from "react";
import type { AuditEvent, OpenBoxApi } from "../api/client";

interface AuditEventsPageProps {
  api: OpenBoxApi;
  onBack(): void;
}

type PageData =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; events: AuditEvent[] };

type OutcomeFilter = "all" | "blocked" | "failed" | "succeeded";

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

function relativeWhen(value: string): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const deltaMs = Date.now() - date.getTime();
  if (deltaMs < 60_000) return "Just now";
  const minutes = Math.floor(deltaMs / 60_000);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  return formatWhen(value);
}

function outcomeTone(outcome: string): "succeeded" | "failed" | "blocked" | "neutral" {
  const value = outcome.trim().toLowerCase();
  if (value === "succeeded" || value === "allowed" || value === "ok") return "succeeded";
  if (
    value === "blocked"
    || value === "denied"
    || value === "authentication_denied"
    || value === "handshake_limited"
    || value === "rate_limited"
    || value === "connection_limited"
  ) {
    return "blocked";
  }
  if (value === "failed" || value.endsWith("_failed") || value.includes("error")) return "failed";
  return "neutral";
}

function matchesFilter(event: AuditEvent, filter: OutcomeFilter): boolean {
  if (filter === "all") return true;
  const tone = outcomeTone(event.outcome);
  if (filter === "blocked") return tone === "blocked";
  if (filter === "failed") return tone === "failed";
  return tone === "succeeded";
}

function formatTarget(event: AuditEvent): string {
  if (!event.targetType && !event.targetId) return "—";
  if (!event.targetType) return event.targetId;
  if (!event.targetId) return event.targetType;
  return `${event.targetType}/${event.targetId}`;
}

function metadataEntries(event: AuditEvent): Array<[string, string]> {
  return Object.entries(event.metadata).filter(([, value]) => value.trim() !== "");
}

export function AuditEventsPage({ api, onBack }: AuditEventsPageProps) {
  const [data, setData] = useState<PageData>({ status: "loading" });
  const [filter, setFilter] = useState<OutcomeFilter>("all");
  const [refreshing, setRefreshing] = useState(false);

  const loadEvents = useCallback(async () => {
    const events = await api.listAuditEvents(100);
    setData({ status: "ready", events });
  }, [api]);

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

  async function refresh() {
    setRefreshing(true);
    try {
      await loadEvents();
    } catch (reason: unknown) {
      setData({
        status: "error",
        message: reason instanceof Error ? reason.message : "Audit events unavailable",
      });
    } finally {
      setRefreshing(false);
    }
  }

  const filtered = useMemo(() => {
    if (data.status !== "ready") return [];
    return data.events.filter((event) => matchesFilter(event, filter));
  }, [data, filter]);

  const counts = useMemo(() => {
    if (data.status !== "ready") {
      return { all: 0, blocked: 0, failed: 0, succeeded: 0 };
    }
    return data.events.reduce(
      (acc, event) => {
        acc.all += 1;
        const tone = outcomeTone(event.outcome);
        if (tone === "blocked") acc.blocked += 1;
        else if (tone === "failed") acc.failed += 1;
        else if (tone === "succeeded") acc.succeeded += 1;
        return acc;
      },
      { all: 0, blocked: 0, failed: 0, succeeded: 0 },
    );
  }, [data]);

  return (
    <div className="console-layout">
      <a className="skip-link" href="#audit-main">Skip to audit events</a>
      <header className="console-header">
        <a className="wordmark" href="/" aria-label="OpenBox home"><span>OB</span> OpenBox</a>
        <nav aria-label="Primary navigation">
          <button className="nav-button" type="button" onClick={onBack}>Instances</button>
          <button className="nav-button" type="button" aria-current="page">Audit</button>
        </nav>
      </header>
      <div className="console-workspace">
        <main id="audit-main" tabIndex={-1}>
          <div className="page-heading">
            <div>
              <button className="link-button instance-back" type="button" onClick={onBack}>
                ← Instances
              </button>
              <h1>Audit</h1>
            </div>
            {data.status === "ready" ? (
              <button
                className="nav-button"
                type="button"
                onClick={() => { void refresh(); }}
                disabled={refreshing}
              >
                {refreshing ? "Refreshing…" : "Refresh"}
              </button>
            ) : null}
          </div>

          <p className="policy-lede">
            Policy and security decisions for your account — without payloads, DNS answers, or secrets.
          </p>

          {data.status === "loading" ? <p className="data-message" role="status">Loading…</p> : null}
          {data.status === "error" ? <p className="data-message is-error" role="alert">{data.message}</p> : null}

          {data.status === "ready" ? (
            <section className="instance-ledger audit-ledger" aria-labelledby="audit-events-heading">
              <div className="ledger-header">
                <h2 id="audit-events-heading">Events</h2>
                <span>{counts.all}</span>
              </div>

              {counts.all > 0 ? (
                <div className="audit-filters" role="group" aria-label="Filter by outcome">
                  {(
                    [
                      ["all", "All", counts.all],
                      ["blocked", "Blocked", counts.blocked],
                      ["failed", "Failed", counts.failed],
                      ["succeeded", "Succeeded", counts.succeeded],
                    ] as const
                  ).map(([value, label, count]) => (
                    <button
                      key={value}
                      type="button"
                      className={filter === value ? "audit-filter is-active" : "audit-filter"}
                      aria-pressed={filter === value}
                      onClick={() => setFilter(value)}
                    >
                      {label}
                      <span>{count}</span>
                    </button>
                  ))}
                </div>
              ) : null}

              {counts.all === 0 ? (
                <div className="empty-inventory">
                  <h3>No audit events yet</h3>
                  <p>Denied connections, policy applies, and SSH sessions will show up here.</p>
                </div>
              ) : filtered.length === 0 ? (
                <div className="empty-inventory">
                  <h3>Nothing matches</h3>
                  <p>No events for this outcome. Try another filter.</p>
                </div>
              ) : (
                <ul className="audit-list">
                  {filtered.map((event) => {
                    const tone = outcomeTone(event.outcome);
                    const meta = metadataEntries(event);
                    return (
                      <li key={event.id} className="audit-row">
                        <div className="audit-row-top">
                          <time
                            className="audit-when"
                            dateTime={event.createdAt}
                            title={formatWhen(event.createdAt)}
                          >
                            {relativeWhen(event.createdAt)}
                          </time>
                          <span className={`status-pill status-pill-${tone}`}>
                            {event.outcome || "—"}
                          </span>
                        </div>
                        <div className="audit-row-main">
                          <code className="audit-action">{event.action || "—"}</code>
                          <span className="audit-target">{formatTarget(event)}</span>
                          {event.actor ? (
                            <>
                              <span className="audit-sep" aria-hidden="true">·</span>
                              <span className="audit-actor">{event.actor}</span>
                            </>
                          ) : null}
                        </div>
                        {meta.length > 0 ? (
                          <dl className="audit-meta">
                            {meta.map(([key, value]) => (
                              <div key={key}>
                                <dt>{key}</dt>
                                <dd>{value}</dd>
                              </div>
                            ))}
                          </dl>
                        ) : null}
                      </li>
                    );
                  })}
                </ul>
              )}
            </section>
          ) : null}
        </main>
      </div>
      <footer><span>openbox</span><span>v0.01</span></footer>
    </div>
  );
}
