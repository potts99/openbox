// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useState } from "react";
import type { InstanceAction, InstanceDetail, OpenBoxApi, SoftwarePackage } from "../api/client";
import { InstanceMetrics } from "../components/InstanceMetrics";
import { SandboxStatus } from "./Sandbox";

interface InstancePageProps {
  api: OpenBoxApi;
  instanceId: string;
  csrfToken?: string;
  onBack(): void;
  onOpenTerminal(instance: { id: string; name: string }): void;
}

type PageData =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; instance: InstanceDetail; catalog: SoftwarePackage[] };

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

function softwareStatus(instance: InstanceDetail, packageId: string): string {
  const row = instance.software.find((item) => item.packageId === packageId);
  return row?.status ?? "absent";
}

export function InstancePage({ api, instanceId, csrfToken, onBack, onOpenTerminal }: InstancePageProps) {
  const [data, setData] = useState<PageData>({ status: "loading" });
  const [actionPending, setActionPending] = useState<InstanceAction | null>(null);
  const [actionError, setActionError] = useState("");
  const [installPending, setInstallPending] = useState<string | null>(null);
  const [installError, setInstallError] = useState("");

  useEffect(() => {
    let active = true;
    void Promise.all([api.getInstance(instanceId), api.listSoftwareCatalog()])
      .then(([instance, catalog]) => {
        if (active) setData({ status: "ready", instance, catalog });
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
      const [instance, catalog] = await Promise.all([api.getInstance(instanceId), api.listSoftwareCatalog()]);
      setData({ status: "ready", instance, catalog });
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Action failed");
    } finally {
      setActionPending(null);
    }
  }

  async function installPackage(packageId: string) {
    setInstallError("");
    setInstallPending(packageId);
    try {
      await api.installSoftware(instanceId, packageId);
      const [instance, catalog] = await Promise.all([api.getInstance(instanceId), api.listSoftwareCatalog()]);
      setData({ status: "ready", instance, catalog });
    } catch (error: unknown) {
      setInstallError(error instanceof Error ? error.message : "Install failed");
    } finally {
      setInstallPending(null);
    }
  }

  const instance = data.status === "ready" ? data.instance : null;
  const catalog = data.status === "ready" ? data.catalog : [];
  const observed = instance?.observedState ?? "";
  const canStart = observed === "stopped" || observed === "error";
  const canStop = observed === "running";
  const canRestart = observed === "running";
  const canOpenTerminal = observed === "running";
  const canInstall = observed === "running" && instance?.kind === "vps";

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
              <button
                className="primary-action"
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
          {installError ? <p className="data-message is-error" role="alert">{installError}</p> : null}
          {data.status === "loading" ? <p className="data-message" role="status">Loading…</p> : null}
          {data.status === "error" ? <p className="data-message is-error" role="alert">{data.message}</p> : null}

          {instance ? (
            <InstanceMetrics
              instanceId={instance.id}
              csrfToken={csrfToken || api.getCsrfToken()}
              vcpus={instance.vcpus}
              memoryBytes={instance.memoryBytes}
              diskBytes={instance.diskBytes}
            />
          ) : null}

          {instance && instance.kind === "vps" ? (
            <section className="instance-detail" aria-labelledby="instance-software-heading">
              <div className="ledger-header">
                <h2 id="instance-software-heading">Software</h2>
              </div>
              <ul className="software-list">
                {catalog.map((pkg) => {
                  const status = softwareStatus(instance, pkg.id);
                  const installed = status === "installed";
                  const pending = status === "pending" || installPending === pkg.id;
                  return (
                    <li key={pkg.id} className="software-row">
                      <div>
                        <strong>{pkg.name}</strong>
                        <p>{pkg.description}</p>
                        <span className={`state-pill state-${status}`}>{status}</span>
                      </div>
                      <button
                        type="button"
                        className="btn"
                        disabled={!canInstall || pending || installed}
                        onClick={() => { void installPackage(pkg.id); }}
                      >
                        {pending ? "Installing…" : installed ? "Installed" : "Install"}
                      </button>
                    </li>
                  );
                })}
              </ul>
            </section>
          ) : null}

          {instance ? (
            <section className="instance-detail" aria-labelledby="instance-detail-heading">
              <div className="ledger-header">
                <h2 id="instance-detail-heading">Detail</h2>
                <div className="detail-header-meta">
                  <span><b>Created</b> {formatWhen(instance.createdAt)}</span>
                  <span><b>Updated</b> {formatWhen(instance.updatedAt)}</span>
                  <span className={`state-pill state-${observed}`}>{observed || "unknown"}</span>
                </div>
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
                  <dt>Egress</dt>
                  <dd>{instance.networkPolicy?.egressMode || "unknown"}</dd>
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
                <div>
                  <dt>Image</dt>
                  <dd><code title={instance.imageId}>{shortenId(instance.imageId)}</code></dd>
                </div>
                <div className="detail-span">
                  <dt>ID</dt>
                  <dd><code title={instance.id}>{instance.id}</code></dd>
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
