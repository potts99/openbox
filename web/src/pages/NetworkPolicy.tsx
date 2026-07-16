// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useState } from "react";
import type { EgressProfile, OpenBoxApi } from "../api/client";

interface NetworkPolicyPageProps {
  api: OpenBoxApi;
  onBack(): void;
}

type PageData =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; profiles: EgressProfile[] };

export function NetworkPolicyPage({ api, onBack }: NetworkPolicyPageProps) {
  const [data, setData] = useState<PageData>({ status: "loading" });
  const [selectedId, setSelectedId] = useState("");
  const [destinationsText, setDestinationsText] = useState("");
  const [mode, setMode] = useState("restricted");
  const [newName, setNewName] = useState("");
  const [busy, setBusy] = useState("");
  const [actionError, setActionError] = useState("");
  const [applyNotes, setApplyNotes] = useState("");

  useEffect(() => {
    let active = true;
    void api.listEgressProfiles()
      .then((profiles) => {
        if (!active) return;
        setData({ status: "ready", profiles });
        setSelectedId((current) => current || profiles[0]?.id || "");
      })
      .catch((error: unknown) => {
        if (active) {
          setData({
            status: "error",
            message: error instanceof Error ? error.message : "Egress profiles unavailable",
          });
        }
      });
    return () => { active = false; };
  }, [api]);

  const profile = data.status === "ready"
    ? data.profiles.find((item) => item.id === selectedId) ?? null
    : null;

  useEffect(() => {
    if (!profile) return;
    setDestinationsText(profile.allowedDestinations.join("\n"));
    setMode(profile.mode);
  }, [profile]);

  async function refreshProfiles(preferredId = selectedId) {
    const profiles = await api.listEgressProfiles();
    setData({ status: "ready", profiles });
    setSelectedId(preferredId || profiles[0]?.id || "");
  }

  async function onSave() {
    if (!profile) return;
    setActionError("");
    setApplyNotes("");
    setBusy("save");
    try {
      const destinations = destinationsText
        .split("\n")
        .map((line) => line.trim())
        .filter(Boolean);
      const result = await api.updateEgressProfile(profile.id, {
        mode,
        allowedDestinations: destinations,
      });
      if (result.applyErrors.length > 0) {
        setApplyNotes(result.applyErrors.map((item) => `${item.instanceId}: ${item.message}`).join("; "));
      }
      await refreshProfiles(result.profile.id);
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Save failed");
    } finally {
      setBusy("");
    }
  }

  async function onCreate() {
    setActionError("");
    setBusy("create");
    try {
      const created = await api.createEgressProfile({
        name: newName.trim(),
        mode: "restricted",
        allowedDestinations: [],
      });
      setNewName("");
      await refreshProfiles(created.id);
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Create failed");
    } finally {
      setBusy("");
    }
  }

  async function onDelete() {
    if (!profile || profile.system) return;
    setActionError("");
    setBusy("delete");
    try {
      await api.deleteEgressProfile(profile.id);
      await refreshProfiles("");
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Delete failed");
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="console-layout">
      <header className="console-header">
        <a className="wordmark" href="/" aria-label="OpenBox home"><span>OB</span> OpenBox</a>
        <nav aria-label="Primary navigation">
          <button className="nav-button" type="button" onClick={onBack}>Instances</button>
          <button className="nav-button" type="button" aria-current="page">Network policy</button>
        </nav>
      </header>
      <main id="main-content" className="console-main">
        <div className="page-heading">
          <h1>Network policy</h1>
          <p>System egress profiles control outbound access. Restricted profiles use host-resolved allowlists.</p>
        </div>
        {data.status === "loading" ? <p>Loading profiles…</p> : null}
        {data.status === "error" ? <p className="is-error" role="alert">{data.message}</p> : null}
        {data.status === "ready" ? (
          <div className="detail-layout">
            <section className="panel">
              <h2>Profiles</h2>
              <ul className="plain-list">
                {data.profiles.map((item) => (
                  <li key={item.id}>
                    <button
                      type="button"
                      className={item.id === selectedId ? "is-active" : undefined}
                      onClick={() => setSelectedId(item.id)}
                    >
                      {item.name} · {item.mode}{item.system ? " · system" : ""}
                    </button>
                  </li>
                ))}
              </ul>
              <div className="form-row">
                <label>
                  New profile name
                  <input value={newName} onChange={(event) => setNewName(event.target.value)} />
                </label>
                <button type="button" onClick={() => { void onCreate(); }} disabled={!newName.trim() || busy !== ""}>
                  {busy === "create" ? "Creating…" : "Create restricted"}
                </button>
              </div>
            </section>
            {profile ? (
              <section className="panel">
                <h2>{profile.name}</h2>
                <dl className="detail-grid">
                  <div>
                    <dt>ID</dt>
                    <dd><code>{profile.id}</code></dd>
                  </div>
                  <div>
                    <dt>System</dt>
                    <dd>{profile.system ? "yes" : "no"}</dd>
                  </div>
                  <div>
                    <dt>Attached</dt>
                    <dd>{profile.attachedInstanceCount ?? 0}</dd>
                  </div>
                </dl>
                <label>
                  Mode
                  <select value={mode} onChange={(event) => setMode(event.target.value)}>
                    <option value="restricted">restricted</option>
                    <option value="standard">standard</option>
                  </select>
                </label>
                <label>
                  Allowed destinations (one IP, CIDR, or hostname per line)
                  <textarea
                    rows={8}
                    value={destinationsText}
                    onChange={(event) => setDestinationsText(event.target.value)}
                  />
                </label>
                <div className="button-row">
                  <button type="button" onClick={() => { void onSave(); }} disabled={busy !== ""}>
                    {busy === "save" ? "Saving…" : "Save and re-apply"}
                  </button>
                  {!profile.system ? (
                    <button type="button" onClick={() => { void onDelete(); }} disabled={busy !== ""}>
                      {busy === "delete" ? "Deleting…" : "Delete"}
                    </button>
                  ) : null}
                </div>
              </section>
            ) : null}
          </div>
        ) : null}
        {actionError ? <p className="is-error" role="alert">{actionError}</p> : null}
        {applyNotes ? <p role="status">Apply warnings: {applyNotes}</p> : null}
      </main>
    </div>
  );
}
