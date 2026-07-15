// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useState } from "react";
import type { InstanceAction, InstanceDetail, OpenBoxApi } from "../api/client";
import { LaunchPi } from "../components/LaunchPi";
import { launchPiAvailable } from "../components/launchPiAvailable";
import { SandboxStatus } from "./Sandbox";

interface InstancePageProps {
  api: OpenBoxApi;
  instanceId: string;
  onBack(): void;
  onOpenTerminal(instance: { id: string; name: string; launchPi?: boolean }): void;
}

type PageData =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; instance: InstanceDetail };

function formatBytes(bytes: number): string {
  if (bytes <= 0) return "—";
  const gib = bytes / (1024 ** 3);
  if (gib >= 1) return `${gib % 1 === 0 ? gib.toFixed(0) : gib.toFixed(1)} GiB`;
  const mib = bytes / (1024 ** 2);
  return `${mib % 1 === 0 ? mib.toFixed(0) : mib.toFixed(1)} MiB`;
}

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

function shortenId(id: string): string {
  if (id.length <= 16) return id;
  return `${id.slice(0, 8)}…${id.slice(-6)}`;
}

export function InstancePage({ api, instanceId, onBack, onOpenTerminal }: InstancePageProps) {
  const [data, setData] = useState<PageData>({ status: "loading" });
  const [actionPending, setActionPending] = useState<InstanceAction | null>(null);
  const [actionError, setActionError] = useState("");

  useEffect(() => {
    let active = true;
    void api.getInstance(instanceId)
      .then((instance) => {
        if (active) setData({ status: "ready", instance });
      })
      .catch((error: unknown) => {
        if (active) {
          setData({
            status: "error",
            message: error instanceof Error ? error.message : "Instance unavailable",
          });
        }
      });
    return () => { active = false; };
  }, [api, instanceId]);

  async function runAction(action: InstanceAction) {
    setActionError("");
    setActionPending(action);
    try {
      await api.mutateInstance(instanceId, action);
      const instance = await api.getInstance(instanceId);
      setData({ status: "ready", instance });
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Action failed");
    } finally {
      setActionPending(null);
    }
  }

  const instance = data.status === "ready" ? data.instance : null;
  const observed = instance?.observedState ?? "";
  const canStart = observed === "stopped" || observed === "error";
  const canStop = observed === "running";
  const canRestart = observed === "running";
  const canOpenTerminal = observed === "running";
  const showLaunchPi = instance ? launchPiAvailable(instance.kind) : false;

  return (
    <div className="console-layout">
      <a className="skip-link" href="#instance-main">Skip to instance</a>
      <header className="console-header">
        <a className="wordmark" href="/" aria-label="OpenBox home"><span>OB</span> OpenBox</a>
        <nav aria-label="Primary navigation">
          <button className="nav-button" type="button" onClick={onBack}>Instances</button>
        </nav>
      </header>

      <div className="console-workspace">
        <main id="instance-main" tabIndex={-1}>
          <div className="page-heading instance-heading">
            <div>
              <button className="link-button instance-back" type="button" onClick={onBack}>
                ← Instances
              </button>
              <h1>{instance?.name ?? "Instance"}</h1>
            </div>
            <div className="instance-actions">
              {showLaunchPi ? (
                <LaunchPi
                  disabled={!canOpenTerminal || !instance}
                  onLaunch={() => {
                    if (instance) onOpenTerminal({ id: instance.id, name: instance.name, launchPi: true });
                  }}
                />
              ) : null}
              <button
                className={showLaunchPi ? "btn" : "primary-action"}
                type="button"
                disabled={!canOpenTerminal || !instance}
                onClick={() => {
                  if (instance) onOpenTerminal({ id: instance.id, name: instance.name });
                }}
              >
                Terminal
              </button>
              <button type="button" className="btn" disabled={!canStart || actionPending !== null} onClick={() => { void runAction("start"); }}>
                {actionPending === "start" ? "Starting…" : "Start"}
              </button>
              <button type="button" className="btn" disabled={!canStop || actionPending !== null} onClick={() => { void runAction("stop"); }}>
                {actionPending === "stop" ? "Stopping…" : "Stop"}
              </button>
              <button type="button" className="btn" disabled={!canRestart || actionPending !== null} onClick={() => { void runAction("restart"); }}>
                {actionPending === "restart" ? "Restarting…" : "Restart"}
              </button>
            </div>
          </div>

          {actionError ? <p className="data-message is-error" role="alert">{actionError}</p> : null}
          {data.status === "loading" ? <p className="data-message" role="status">Loading…</p> : null}
          {data.status === "error" ? <p className="data-message is-error" role="alert">{data.message}</p> : null}

          {instance ? (
            <section className="instance-detail" aria-labelledby="instance-detail-heading">
              <div className="ledger-header">
                <h2 id="instance-detail-heading">Detail</h2>
                <span className={`state-pill state-${observed}`}>{observed || "unknown"}</span>
              </div>
              <dl className="detail-grid">
                <div>
                  <dt>Kind</dt>
                  <dd>{instance.kind}</dd>
                </div>
                <div>
                  <dt>Isolation</dt>
                  <dd>{instance.actualIsolation || instance.requestedIsolation}</dd>
                </div>
                <div>
                  <dt>Desired</dt>
                  <dd>{instance.desiredState}</dd>
                </div>
                <div>
                  <dt>Protected</dt>
                  <dd>{instance.protected ? "yes" : "no"}</dd>
                </div>
                <div>
                  <dt>vCPUs</dt>
                  <dd>{instance.vcpus || "—"}</dd>
                </div>
                <div>
                  <dt>Memory</dt>
                  <dd>{formatBytes(instance.memoryBytes)}</dd>
                </div>
                <div>
                  <dt>Disk</dt>
                  <dd>{formatBytes(instance.diskBytes)}</dd>
                </div>
                <div className="detail-span">
                  <dt>Image</dt>
                  <dd><code title={instance.imageId}>{shortenId(instance.imageId)}</code></dd>
                </div>
                <div className="detail-span">
                  <dt>ID</dt>
                  <dd><code title={instance.id}>{instance.id}</code></dd>
                </div>
                <div>
                  <dt>Created</dt>
                  <dd>{formatWhen(instance.createdAt)}</dd>
                </div>
                <div>
                  <dt>Updated</dt>
                  <dd>{formatWhen(instance.updatedAt)}</dd>
                </div>
                {instance.errorCode ? (
                  <div className="detail-span">
                    <dt>Error</dt>
                    <dd className="is-error">
                      {instance.errorCode}
                      {instance.errorStage ? ` at ${instance.errorStage}` : ""}
                    </dd>
                  </div>
                ) : null}
              </dl>
            </section>
          ) : null}

          {instance?.kind === "sandbox" ? (
            <SandboxStatus
              expiresAt={instance.expiresAt}
              errorCode={instance.errorCode}
              errorStage={instance.errorStage}
              egressPolicy="default"
            />
          ) : null}
        </main>
      </div>
      <footer><span>openbox</span><span>v1</span></footer>
    </div>
  );
}
