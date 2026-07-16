// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useState } from "react";
import type { OpenBoxApi, OperationEvent, OperationStreamStatus, OperationSummary } from "../api/client";

interface OperationEventStreamProps {
  api: OpenBoxApi;
  operationId: string;
  EventSourceImpl?: typeof EventSource;
  onStatusChange(status: OperationStreamStatus): void;
}

function OperationEventStream({
  api,
  operationId,
  EventSourceImpl,
  onStatusChange,
}: OperationEventStreamProps) {
  const [events, setEvents] = useState<OperationEvent[]>([]);
  const [streamError, setStreamError] = useState("");

  useEffect(() => {
    const subscription = api.subscribeOperationEvents(
      operationId,
      {
        onStatus: (status, detail) => {
          onStatusChange(status);
          if (status === "error" && detail) setStreamError(detail);
        },
        onEvent: (event) => {
          setEvents((current) => {
            if (current.some((item) => item.sequence === event.sequence)) return current;
            return [...current, event].sort((left, right) => left.sequence - right.sequence);
          });
        },
        onError: (detail) => {
          if (detail) setStreamError(detail);
        },
      },
      { EventSourceImpl },
    );

    return () => subscription.close();
  }, [EventSourceImpl, api, onStatusChange, operationId]);

  return (
    <>
      {streamError ? <p className="data-message is-error" role="alert">{streamError}</p> : null}
      {events.length === 0 ? (
        <p className="data-message" role="status">Loading events…</p>
      ) : (
        <ol className="operation-event-list">
          {events.map((event) => (
            <li key={event.sequence} className={`operation-event operation-event-${event.status}`}>
              <div className="operation-event-head">
                <span className="operation-event-time">{formatWhen(event.createdAt)}</span>
                <span className="operation-event-stage">{event.stage}</span>
                <span className={`state-pill state-${event.status}`}>{event.status}</span>
                <span className="operation-event-progress">{event.progress}%</span>
              </div>
              {event.message ? <p className="operation-event-message">{event.message}</p> : null}
              {event.errorCode ? (
                <p className="operation-event-error">
                  {event.errorCode}
                  {event.errorClass ? ` (${event.errorClass})` : ""}
                </p>
              ) : null}
            </li>
          ))}
        </ol>
      )}
    </>
  );
}

interface InstanceOperationLogsProps {
  api: OpenBoxApi;
  instanceId: string;
  relatedTargets?: string[];
  refreshKey?: number;
  EventSourceImpl?: typeof EventSource;
}

type LogsData =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; operations: OperationSummary[] };

function isActiveOperation(status: string): boolean {
  return status === "pending" || status === "running";
}

function defaultSelectedOperation(operations: OperationSummary[]): string {
  const active = operations.find((operation) => isActiveOperation(operation.status));
  return active?.id ?? operations[0]?.id ?? "";
}

function formatOperationType(action: string): string {
  switch (action) {
    case "instance.create":
      return "Create instance";
    case "instance.start":
      return "Start";
    case "instance.stop":
      return "Stop";
    case "instance.restart":
      return "Restart";
    case "instance.delete":
      return "Delete";
    case "software.install":
      return "Install software";
    default:
      return action.replaceAll(".", " ");
  }
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
    second: "2-digit",
  });
}

function streamStatusLabel(status: OperationStreamStatus): string {
  switch (status) {
    case "connecting":
      return "connecting";
    case "live":
      return "live";
    case "complete":
      return "complete";
    case "error":
      return "interrupted";
    case "closed":
      return "closed";
    default: {
      const _exhaustive: never = status;
      return _exhaustive;
    }
  }
}

export function InstanceOperationLogs({
  api,
  instanceId,
  relatedTargets = [],
  refreshKey = 0,
  EventSourceImpl,
}: InstanceOperationLogsProps) {
  const [data, setData] = useState<LogsData>({ status: "loading" });
  const [selectedOperationId, setSelectedOperationId] = useState("");
  const [streamStatus, setStreamStatus] = useState<OperationStreamStatus>("closed");

  useEffect(() => {
    let active = true;
    void api.listOperations()
      .then((operations) => {
        if (!active) return;
        const targets = new Set([instanceId, ...relatedTargets]);
        const scoped = operations.filter((operation) => targets.has(operation.target));
        setData({ status: "ready", operations: scoped });
        setSelectedOperationId((current) => {
          if (current && scoped.some((operation) => operation.id === current)) return current;
          return defaultSelectedOperation(scoped);
        });
      })
      .catch((error: unknown) => {
        if (!active) return;
        setData({
          status: "error",
          message: error instanceof Error ? error.message : "Operations unavailable",
        });
      });
    return () => { active = false; };
  }, [api, instanceId, refreshKey, relatedTargets]);

  const operations = data.status === "ready" ? data.operations : [];
  const selectedOperation = operations.find((operation) => operation.id === selectedOperationId) ?? null;
  const hasActiveOperations = operations.some((operation) => isActiveOperation(operation.status));

  useEffect(() => {
    if (!hasActiveOperations || data.status !== "ready") return undefined;
    const timer = window.setInterval(() => {
      void api.listOperations()
        .then((items) => {
          const targets = new Set([instanceId, ...relatedTargets]);
          const scoped = items.filter((operation) => targets.has(operation.target));
          setData({ status: "ready", operations: scoped });
        })
        .catch(() => undefined);
    }, 5000);
    return () => window.clearInterval(timer);
  }, [api, data.status, hasActiveOperations, instanceId, relatedTargets]);

  return (
    <section className="instance-detail instance-operation-logs" aria-labelledby="instance-logs-heading">
      <div className="ledger-header">
        <h2 id="instance-logs-heading">Build &amp; deploy logs</h2>
        {selectedOperation ? (
          <span className={`operation-logs-status operation-logs-status-${streamStatus}`}>
            {streamStatusLabel(streamStatus)}
          </span>
        ) : null}
      </div>

      {data.status === "loading" ? <p className="data-message" role="status">Loading operations…</p> : null}
      {data.status === "error" ? <p className="data-message is-error" role="alert">{data.message}</p> : null}
      {data.status === "ready" && operations.length === 0 ? (
        <p className="data-message">No build or deploy activity yet.</p>
      ) : null}

      {data.status === "ready" && operations.length > 0 ? (
        <div className="operation-logs-layout">
          <ul className="operation-history" aria-label="Operation history">
            {operations.map((operation) => {
              const selected = operation.id === selectedOperationId;
              return (
                <li key={operation.id}>
                  <button
                    type="button"
                    className={`operation-history-item${selected ? " is-selected" : ""}`}
                    aria-current={selected ? "true" : undefined}
                    onClick={() => setSelectedOperationId(operation.id)}
                  >
                    <span className="operation-history-title">{formatOperationType(operation.action)}</span>
                    <span className={`state-pill state-${operation.status}`}>{operation.status}</span>
                    <span className="operation-history-meta">
                      {operation.progress}% · {formatWhen(operation.updatedAt)}
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>

          <div className="operation-log-viewer" aria-live="polite">
            {selectedOperation ? (
              <div className="operation-log-summary">
                <span>{formatOperationType(selectedOperation.action)}</span>
                <span className={`state-pill state-${selectedOperation.status}`}>{selectedOperation.status}</span>
                <span>{selectedOperation.stage || "—"}</span>
                <span>{selectedOperation.progress}%</span>
              </div>
            ) : null}
            {selectedOperationId ? (
              <OperationEventStream
                key={selectedOperationId}
                api={api}
                operationId={selectedOperationId}
                EventSourceImpl={EventSourceImpl}
                onStatusChange={setStreamStatus}
              />
            ) : (
              <p className="data-message">No events recorded for this operation.</p>
            )}
          </div>
        </div>
      ) : null}
    </section>
  );
}
