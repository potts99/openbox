// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useState } from "react";
import type { InstanceSummary, OpenBoxApi, PiProfileSummary, PiProfileVersion } from "../api/client";

interface PiProfilePageProps {
  api: OpenBoxApi;
  onBack(): void;
}

type PageData =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; profiles: PiProfileSummary[]; instances: InstanceSummary[] };

function previewText(settingsJson: string): string {
  try {
    return JSON.stringify(JSON.parse(settingsJson), null, 2);
  } catch {
    return settingsJson;
  }
}

export function PiProfilePage({ api, onBack }: PiProfilePageProps) {
  const [data, setData] = useState<PageData>({ status: "loading" });
  const [selectedId, setSelectedId] = useState("");
  const [history, setHistory] = useState<PiProfileVersion[]>([]);
  const [selectedInstances, setSelectedInstances] = useState<string[]>([]);
  const [busy, setBusy] = useState("");
  const [actionError, setActionError] = useState("");

  useEffect(() => {
    let active = true;
    void Promise.all([api.listPiProfiles(), api.listInstances()])
      .then(([profiles, instances]) => {
        if (!active) return;
        setData({ status: "ready", profiles, instances });
        setSelectedId((current) => current || profiles[0]?.id || "");
      })
      .catch((error: unknown) => {
        if (active) {
          setData({
            status: "error",
            message: error instanceof Error ? error.message : "Pi profiles unavailable",
          });
        }
      });
    return () => { active = false; };
  }, [api]);

  useEffect(() => {
    if (!selectedId) return;
    let active = true;
    void api.getPiProfileHistory(selectedId)
      .then((versions) => { if (active) setHistory(versions); })
      .catch(() => { if (active) setHistory([]); });
    return () => { active = false; };
  }, [api, selectedId]);

  const visibleHistory = selectedId ? history : [];
  const profile = data.status === "ready"
    ? data.profiles.find((item) => item.id === selectedId) ?? null
    : null;

  async function onRollback(version: number) {
    if (!profile) return;
    setActionError("");
    setBusy("rollback");
    try {
      const updated = await api.rollbackPiProfile(profile.id, version);
      setData((current) => {
        if (current.status !== "ready") return current;
        return {
          ...current,
          profiles: current.profiles.map((item) => (item.id === updated.id ? updated : item)),
        };
      });
      setHistory(await api.getPiProfileHistory(profile.id));
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Rollback failed");
    } finally {
      setBusy("");
    }
  }

  async function onApply() {
    if (!profile || selectedInstances.length === 0) return;
    setActionError("");
    setBusy("apply");
    try {
      await api.applyPiProfile(profile.id, selectedInstances);
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Apply failed");
    } finally {
      setBusy("");
    }
  }

  function toggleInstance(id: string) {
    setSelectedInstances((current) => (
      current.includes(id) ? current.filter((item) => item !== id) : [...current, id]
    ));
  }

  return (
    <div className="console-layout">
      <a className="skip-link" href="#pi-profile-main">Skip to Pi profile</a>
      <header className="console-header">
        <a className="wordmark" href="/" aria-label="OpenBox home"><span>OB</span> OpenBox</a>
        <nav aria-label="Primary navigation">
          <button className="nav-button" type="button" onClick={onBack}>Instances</button>
        </nav>
      </header>

      <div className="console-workspace">
        <main id="pi-profile-main" tabIndex={-1}>
          <div className="page-heading">
            <div>
              <button className="link-button instance-back" type="button" onClick={onBack}>
                ← Instances
              </button>
              <h1>Pi profile</h1>
            </div>
          </div>

          {actionError ? <p className="data-message is-error" role="alert">{actionError}</p> : null}
          {data.status === "loading" ? <p className="data-message" role="status">Loading…</p> : null}
          {data.status === "error" ? <p className="data-message is-error" role="alert">{data.message}</p> : null}

          {data.status === "ready" ? (
            <div className="pi-profile-layout">
              <section aria-labelledby="pi-profile-list-heading">
                <h2 id="pi-profile-list-heading">Profiles</h2>
                {data.profiles.length === 0 ? (
                  <p className="data-message">No Pi profiles yet.</p>
                ) : (
                  <ul className="pi-profile-list">
                    {data.profiles.map((item) => (
                      <li key={item.id}>
                        <button
                          type="button"
                          className={item.id === selectedId ? "is-selected" : undefined}
                          onClick={() => setSelectedId(item.id)}
                        >
                          {item.name} <span>v{item.version}</span>
                        </button>
                      </li>
                    ))}
                  </ul>
                )}
              </section>

              {profile ? (
                <>
                  <section aria-labelledby="pi-profile-preview-heading">
                    <h2 id="pi-profile-preview-heading">Preview</h2>
                    <p>Version {profile.version}</p>
                    <pre className="pi-profile-preview">{previewText(profile.settingsJson)}</pre>
                  </section>

                  <section aria-labelledby="pi-profile-history-heading">
                    <h2 id="pi-profile-history-heading">Version history</h2>
                    <ul className="pi-profile-history">
                      {visibleHistory.map((item) => (
                        <li key={item.version}>
                          <span>v{item.version}</span>
                          <button
                            type="button"
                            className="btn"
                            disabled={busy !== "" || item.version === profile.version}
                            onClick={() => { void onRollback(item.version); }}
                          >
                            {busy === "rollback" ? "Rolling back…" : "Rollback"}
                          </button>
                        </li>
                      ))}
                    </ul>
                  </section>

                  <section aria-labelledby="pi-profile-apply-heading">
                    <h2 id="pi-profile-apply-heading">Apply to instances</h2>
                    <ul className="pi-profile-instances">
                      {data.instances.map((instance) => (
                        <li key={instance.id}>
                          <label>
                            <input
                              type="checkbox"
                              checked={selectedInstances.includes(instance.id)}
                              onChange={() => toggleInstance(instance.id)}
                            />
                            {instance.name} <small>{instance.kind}</small>
                          </label>
                        </li>
                      ))}
                    </ul>
                    <button
                      type="button"
                      className="primary-action"
                      disabled={busy !== "" || selectedInstances.length === 0}
                      onClick={() => { void onApply(); }}
                    >
                      {busy === "apply" ? "Applying…" : "Apply profile"}
                    </button>
                  </section>
                </>
              ) : null}
            </div>
          ) : null}
        </main>
      </div>
      <footer><span>openbox</span><span>v1</span></footer>
    </div>
  );
}
