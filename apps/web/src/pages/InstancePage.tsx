// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useState } from "react";
import type {
  ArtifactSummary,
  ConnectionInfo,
  EgressProfile,
  InstanceAction,
  InstanceDetail,
  OpenBoxApi,
  SnapshotSummary,
  SoftwarePackage,
} from "../api/client";
import { Artifacts } from "../components/Artifacts";
import { InstanceMetrics } from "../components/InstanceMetrics";
import { InstanceOperationLogs } from "../components/InstanceOperationLogs";
import { SSHConnect } from "../components/SSHConnect";
import { Checkpoints } from "./Checkpoints";
import { SandboxStatus } from "./Sandbox";

interface InstancePageProps {
  api: OpenBoxApi;
  instanceId: string;
  csrfToken?: string;
  onBack(): void;
  onOpenTerminal(instance: { id: string; name: string; kind: string }): void;
}

type PageData =
  | { status: "loading" }
  | { status: "error"; message: string }
  | {
    status: "ready";
    instance: InstanceDetail;
    catalog: SoftwarePackage[];
    connection: ConnectionInfo;
    egressProfiles: EgressProfile[];
  };

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
  const [extendPending, setExtendPending] = useState(false);
  const [extendError, setExtendError] = useState("");
  const [operationsRefreshKey, setOperationsRefreshKey] = useState(0);
  const [snapshots, setSnapshots] = useState<SnapshotSummary[]>([]);
  const [artifacts, setArtifacts] = useState<ArtifactSummary[]>([]);
  const [artifactPending, setArtifactPending] = useState(false);
  const [artifactError, setArtifactError] = useState("");
  const [checkpointPending, setCheckpointPending] = useState(false);
  const [checkpointError, setCheckpointError] = useState("");
  const [checkpointWarnings, setCheckpointWarnings] = useState<string[]>([]);
  const [storageNote, setStorageNote] = useState("");
  const [attachProfileId, setAttachProfileId] = useState("");
  const [attachPending, setAttachPending] = useState(false);
  const [attachError, setAttachError] = useState("");

  useEffect(() => {
    let active = true;
    void Promise.all([
      api.getInstance(instanceId),
      api.listSoftwareCatalog(),
      api.getConnection().catch(() => ({ ssh: null }) as ConnectionInfo),
      api.listSnapshots(instanceId).catch(() => [] as SnapshotSummary[]),
      api.listArtifacts(instanceId).catch(() => [] as ArtifactSummary[]),
      api.listEgressProfiles().catch(() => [] as EgressProfile[]),
    ]).then(([instance, catalog, connection, listed, artifactItems, egressProfiles]) => {
        if (active) {
          setData({ status: "ready", instance, catalog, connection, egressProfiles });
          setSnapshots(listed);
          setArtifacts(artifactItems);
          setAttachProfileId(instance.egressProfileId || egressProfiles[0]?.id || "");
        }
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

  async function reloadReady() {
    const [instance, catalog, connection, listed, artifactItems, egressProfiles] = await Promise.all([
      api.getInstance(instanceId),
      api.listSoftwareCatalog(),
      api.getConnection().catch(() => ({ ssh: null }) as ConnectionInfo),
      api.listSnapshots(instanceId).catch(() => [] as SnapshotSummary[]),
      api.listArtifacts(instanceId).catch(() => [] as ArtifactSummary[]),
      api.listEgressProfiles().catch(() => [] as EgressProfile[]),
    ]);
    setData({ status: "ready", instance, catalog, connection, egressProfiles });
    setSnapshots(listed);
    setArtifacts(artifactItems);
    setAttachProfileId(instance.egressProfileId || egressProfiles[0]?.id || "");
  }

  async function uploadArtifact(path: string, file: File) {
    setArtifactError("");
    setArtifactPending(true);
    try {
      await api.uploadArtifact(instanceId, path, file);
      setArtifacts(await api.listArtifacts(instanceId));
    } catch (error: unknown) {
      setArtifactError(error instanceof Error ? error.message : "Could not upload artifact");
    } finally {
      setArtifactPending(false);
    }
  }

  async function downloadArtifact(artifact: ArtifactSummary) {
    setArtifactError("");
    setArtifactPending(true);
    try {
      const blob = await api.downloadArtifact(instanceId, artifact.path);
      const link = document.createElement("a");
      link.href = URL.createObjectURL(blob);
      link.download = artifact.path.split("/").at(-1) || "artifact";
      link.click();
      URL.revokeObjectURL(link.href);
    } catch (error: unknown) {
      setArtifactError(error instanceof Error ? error.message : "Could not download artifact");
    } finally {
      setArtifactPending(false);
    }
  }

  async function attachEgressProfile() {
    if (!attachProfileId) return;
    setAttachError("");
    setAttachPending(true);
    try {
      const instance = await api.attachEgressProfile(instanceId, attachProfileId);
      setData((current) => {
        if (current.status !== "ready") return current;
        return { ...current, instance };
      });
    } catch (error: unknown) {
      setAttachError(error instanceof Error ? error.message : "Could not attach egress profile");
    } finally {
      setAttachPending(false);
    }
  }

  async function ownerKey(): Promise<string> {
    const keys = await api.listSSHKeys();
    const key = keys[0]?.publicKey?.trim();
    if (!key) {
      throw new Error("Add an SSH public key before clone or restore");
    }
    return key;
  }

  async function extendTTL(durationSeconds: number) {
    setExtendError("");
    setExtendPending(true);
    try {
      const instance = await api.extendInstance(instanceId, durationSeconds);
      setData((current) => {
        if (current.status !== "ready") return current;
        return { ...current, instance };
      });
    } catch (error: unknown) {
      setExtendError(error instanceof Error ? error.message : "Could not extend TTL");
    } finally {
      setExtendPending(false);
    }
  }

  async function runAction(action: InstanceAction) {
    setActionError("");
    setActionPending(action);
    try {
      await api.mutateInstance(instanceId, action);
      await reloadReady();
      setOperationsRefreshKey((value) => value + 1);
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
      await reloadReady();
      setOperationsRefreshKey((value) => value + 1);
    } catch (error: unknown) {
      setInstallError(error instanceof Error ? error.message : "Install failed");
    } finally {
      setInstallPending(null);
    }
  }

  async function createCheckpoint(name: string) {
    setCheckpointError("");
    setCheckpointWarnings([]);
    setStorageNote("");
    setCheckpointPending(true);
    try {
      await api.createSnapshot(instanceId, name);
      await reloadReady();
      setOperationsRefreshKey((value) => value + 1);
    } catch (error: unknown) {
      setCheckpointError(error instanceof Error ? error.message : "Checkpoint failed");
    } finally {
      setCheckpointPending(false);
    }
  }

  async function restoreCheckpoint(snapshotId: string, name: string) {
    setCheckpointError("");
    setCheckpointWarnings([]);
    setStorageNote("");
    setCheckpointPending(true);
    try {
      const key = await ownerKey();
      const result = await api.restoreSnapshot(snapshotId, name, key);
      setCheckpointWarnings(result.warnings);
      setStorageNote(
        result.storageEfficiency === "confirmed"
          ? "Storage efficiency: confirmed (copy-on-write)."
          : `Storage efficiency: ${result.storageEfficiency}. OpenBox does not claim copy-on-write.`,
      );
      await reloadReady();
      setOperationsRefreshKey((value) => value + 1);
    } catch (error: unknown) {
      setCheckpointError(error instanceof Error ? error.message : "Restore failed");
    } finally {
      setCheckpointPending(false);
    }
  }

  async function deleteCheckpoint(snapshotId: string) {
    setCheckpointError("");
    setCheckpointPending(true);
    try {
      await api.deleteSnapshot(snapshotId);
      await reloadReady();
      setOperationsRefreshKey((value) => value + 1);
    } catch (error: unknown) {
      setCheckpointError(error instanceof Error ? error.message : "Delete failed");
    } finally {
      setCheckpointPending(false);
    }
  }

  async function cloneLive(name: string) {
    setCheckpointError("");
    setCheckpointWarnings([]);
    setStorageNote("");
    setCheckpointPending(true);
    try {
      const key = await ownerKey();
      const result = await api.cloneInstance(instanceId, name, key);
      setCheckpointWarnings(result.warnings);
      setStorageNote(
        result.storageEfficiency === "confirmed"
          ? "Storage efficiency: confirmed (copy-on-write)."
          : `Storage efficiency: ${result.storageEfficiency}. OpenBox does not claim copy-on-write.`,
      );
      await reloadReady();
      setOperationsRefreshKey((value) => value + 1);
    } catch (error: unknown) {
      setCheckpointError(error instanceof Error ? error.message : "Clone failed");
    } finally {
      setCheckpointPending(false);
    }
  }

  const instance = data.status === "ready" ? data.instance : null;
  const catalog = data.status === "ready" ? data.catalog : [];
  const connection = data.status === "ready" ? data.connection : null;
  const observed = instance?.observedState ?? "";
  const canStart = observed === "stopped" || observed === "error";
  const canStop = observed === "running";
  const canRestart = observed === "running";
  const canOpenTerminal = observed === "running" && instance?.kind !== "sandbox";
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
              {instance?.kind !== "sandbox" ? (
                <button
                  className="primary-action"
                  type="button"
                  disabled={!canOpenTerminal || !instance}
                  onClick={() => {
                    if (instance) onOpenTerminal({ id: instance.id, name: instance.name, kind: instance.kind });
                  }}
                >
                  Terminal
                </button>
              ) : null}
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

          {instance && connection ? (
            <SSHConnect instanceName={instance.name} connection={connection} />
          ) : null}

          {instance ? (
            <InstanceMetrics
              instanceId={instance.id}
              csrfToken={csrfToken || api.getCsrfToken()}
              vcpus={instance.vcpus}
              memoryBytes={instance.memoryBytes}
              diskBytes={instance.diskBytes}
            />
          ) : null}

          {instance ? (
            <InstanceOperationLogs
              api={api}
              instanceId={instance.id}
              relatedTargets={snapshots.map((snapshot) => snapshot.id)}
              refreshKey={operationsRefreshKey}
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
                  <dd>
                    {instance.actualIsolation || instance.requestedIsolation}
                    {instance.kind === "sandbox" && instance.actualIsolation === "container"
                      ? " (container; omitted requests select this when KVM is unavailable)"
                      : ""}
                  </dd>
                </div>
                <div>
                  <dt>Egress</dt>
                  <dd>{instance.networkPolicy?.egressMode || "unknown"}</dd>
                </div>
                <div>
                  <dt>Egress profile</dt>
                  <dd>{instance.egressProfileId || "—"}</dd>
                </div>
                <div>
                  <dt>Network ACLs</dt>
                  <dd>{instance.networkPolicy?.acls?.join(", ") || "—"}</dd>
                </div>
                <div>
                  <dt>Resolution</dt>
                  <dd>{instance.networkPolicy?.resolutionState || "idle"}</dd>
                </div>
                <div>
                  <dt>Pending hostnames</dt>
                  <dd>{instance.networkPolicy?.resolutionPending?.join(", ") || "—"}</dd>
                </div>
                <div>
                  <dt>Resolved hostnames</dt>
                  <dd>{instance.networkPolicy?.resolutionResolved?.join(", ") || "—"}</dd>
                </div>
                <div>
                  <dt>Failed hostnames</dt>
                  <dd>{instance.networkPolicy?.resolutionFailed?.join(", ") || "—"}</dd>
                </div>
                <div>
                  <dt>Denied flows</dt>
                  <dd>{instance.networkPolicy?.deniedFlows ?? 0}</dd>
                </div>
                {data.status === "ready" && data.egressProfiles.length > 0 ? (
                  <div className="detail-span">
                    <dt>Attach profile</dt>
                    <dd>
                      <div className="create-choice-row">
                        <select
                          aria-label="Attach egress profile"
                          value={attachProfileId}
                          onChange={(event) => setAttachProfileId(event.target.value)}
                        >
                          {data.egressProfiles.map((profile) => (
                            <option key={profile.id} value={profile.id}>
                              {profile.name} ({profile.mode})
                            </option>
                          ))}
                        </select>
                        <button
                          type="button"
                          className="btn"
                          disabled={attachPending || !attachProfileId || attachProfileId === instance.egressProfileId}
                          onClick={() => { void attachEgressProfile(); }}
                        >
                          {attachPending ? "Attaching…" : "Attach"}
                        </button>
                      </div>
                      {attachError ? <p className="data-message is-error" role="alert">{attachError}</p> : null}
                    </dd>
                  </div>
                ) : null}
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
                {instance.cloneSourceInstanceId ? (
                  <div>
                    <dt>Clone source</dt>
                    <dd><code title={instance.cloneSourceInstanceId}>{shortenId(instance.cloneSourceInstanceId)}</code></dd>
                  </div>
                ) : null}
                {instance.cloneSourceSnapshotId ? (
                  <div>
                    <dt>Source checkpoint</dt>
                    <dd><code title={instance.cloneSourceSnapshotId}>{shortenId(instance.cloneSourceSnapshotId)}</code></dd>
                  </div>
                ) : null}
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
              egressPolicy={instance.networkPolicy?.egressMode || "restricted"}
              isolationNote={
                instance.actualIsolation === "container"
                  ? "Running as a container. Explicit strong never silently downgrades; omitted requests select container when KVM is unavailable."
                  : undefined
              }
              extendPending={extendPending}
              extendError={extendError}
              onExtend={(seconds) => { void extendTTL(seconds); }}
            />
          ) : null}

          {instance ? (
            <Checkpoints
              snapshots={snapshots}
              pending={checkpointPending}
              error={checkpointError}
              warnings={checkpointWarnings}
              storageNote={storageNote}
              cloneSourceInstanceId={instance.cloneSourceInstanceId}
              cloneSourceSnapshotId={instance.cloneSourceSnapshotId}
              onCreate={(name) => { void createCheckpoint(name); }}
              onRestore={(snapshotId, name) => { void restoreCheckpoint(snapshotId, name); }}
              onDelete={(snapshotId) => { void deleteCheckpoint(snapshotId); }}
              onClone={(name) => { void cloneLive(name); }}
            />
          ) : null}
          {instance ? (
            <Artifacts
              artifacts={artifacts}
              pending={artifactPending}
              error={artifactError}
              onUpload={(path, file) => { void uploadArtifact(path, file); }}
              onDownload={(artifact) => { void downloadArtifact(artifact); }}
            />
          ) : null}
        </main>
      </div>
      <footer><span>openbox</span><span>v0.01</span></footer>
    </div>
  );
}
