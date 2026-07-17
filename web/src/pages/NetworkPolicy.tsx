// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useMemo, useState } from "react";
import type { EgressProfile, OpenBoxApi } from "../api/client";

interface NetworkPolicyPageProps {
  api: OpenBoxApi;
  onBack(): void;
}

type PageData =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; profiles: EgressProfile[] };

function destinationsEqual(text: string, destinations: string[]): boolean {
  const parsed = text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
  if (parsed.length !== destinations.length) return false;
  return parsed.every((entry, index) => entry === destinations[index]);
}

export function NetworkPolicyPage({ api, onBack }: NetworkPolicyPageProps) {
  const [data, setData] = useState<PageData>({ status: "loading" });
  const [selectedId, setSelectedId] = useState("");
  const [destinationsText, setDestinationsText] = useState("");
  const [mode, setMode] = useState("restricted");
  const [newName, setNewName] = useState("");
  const [busy, setBusy] = useState("");
  const [actionError, setActionError] = useState("");
  const [applyNotes, setApplyNotes] = useState("");
  const [savedNote, setSavedNote] = useState("");

  function seedEditor(next: EgressProfile) {
    setDestinationsText(next.allowedDestinations.join("\n"));
    setMode(next.mode);
    setApplyNotes("");
    setSavedNote("");
    setActionError("");
  }

  function selectProfile(id: string, profiles: EgressProfile[]) {
    setSelectedId(id);
    const next = profiles.find((item) => item.id === id);
    if (next) seedEditor(next);
  }

  useEffect(() => {
    let active = true;
    void api.listEgressProfiles()
      .then((profiles) => {
        if (!active) return;
        setData({ status: "ready", profiles });
        const initialId = profiles[0]?.id || "";
        setSelectedId(initialId);
        if (profiles[0]) seedEditor(profiles[0]);
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

  const dirty = useMemo(() => {
    if (!profile) return false;
    return mode !== profile.mode || !destinationsEqual(destinationsText, profile.allowedDestinations);
  }, [profile, mode, destinationsText]);

  async function refreshProfiles(preferredId = selectedId) {
    const profiles = await api.listEgressProfiles();
    setData({ status: "ready", profiles });
    const nextId = preferredId || profiles[0]?.id || "";
    setSelectedId(nextId);
  }

  async function onSave() {
    if (!profile) return;
    setActionError("");
    setApplyNotes("");
    setSavedNote("");
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
      setDestinationsText(result.profile.allowedDestinations.join("\n"));
      setMode(result.profile.mode);
      if (result.applyErrors.length > 0) {
        setApplyNotes(result.applyErrors.map((item) => `${item.instanceId}: ${item.message}`).join("; "));
      } else {
        setSavedNote("Saved and re-applied to attached instances.");
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
    setSavedNote("");
    setBusy("create");
    try {
      const created = await api.createEgressProfile({
        name: newName.trim(),
        mode: "restricted",
        allowedDestinations: [],
      });
      setNewName("");
      const profiles = await api.listEgressProfiles();
      setData({ status: "ready", profiles });
      selectProfile(created.id, profiles);
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Create failed");
    } finally {
      setBusy("");
    }
  }

  async function onDelete() {
    if (!profile || profile.system) return;
    setActionError("");
    setSavedNote("");
    setBusy("delete");
    try {
      await api.deleteEgressProfile(profile.id);
      const profiles = await api.listEgressProfiles();
      setData({ status: "ready", profiles });
      const nextId = profiles[0]?.id || "";
      if (nextId) {
        selectProfile(nextId, profiles);
      } else {
        setSelectedId("");
        setDestinationsText("");
        setMode("restricted");
      }
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Delete failed");
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="console-layout">
      <a className="skip-link" href="#network-policy-main">Skip to network policy</a>
      <header className="console-header">
        <a className="wordmark" href="/" aria-label="OpenBox home"><span>OB</span> OpenBox</a>
        <nav aria-label="Primary navigation">
          <button className="nav-button" type="button" onClick={onBack}>Instances</button>
          <button className="nav-button" type="button" aria-current="page">Network policy</button>
        </nav>
      </header>

      <div className="console-workspace">
        <main id="network-policy-main" tabIndex={-1}>
          <div className="page-heading instance-heading">
            <div>
              <button className="link-button instance-back" type="button" onClick={onBack}>
                ← Instances
              </button>
              <h1>Network policy</h1>
            </div>
          </div>

          <p className="policy-lede">
            Egress profiles set outbound access for instances. Restricted profiles only reach
            host-resolved allowlisted destinations.
          </p>

          {data.status === "loading" ? <p className="data-message" role="status">Loading profiles…</p> : null}
          {data.status === "error" ? <p className="data-message is-error" role="alert">{data.message}</p> : null}

          {data.status === "ready" ? (
            <>
              <section className="instance-ledger" aria-labelledby="profiles-heading">
                <div className="ledger-header">
                  <h2 id="profiles-heading">Profiles</h2>
                  <span>{data.profiles.length}</span>
                </div>
                {data.profiles.length === 0 ? (
                  <div className="empty-inventory">
                    <h3>No profiles</h3>
                    <p>Create a restricted profile to start approving destinations.</p>
                  </div>
                ) : (
                  <div className="table-wrap">
                    <table>
                      <caption className="sr-only">Egress profiles</caption>
                      <thead>
                        <tr>
                          <th>Name</th>
                          <th>Mode</th>
                          <th>Attached</th>
                          <th>Type</th>
                        </tr>
                      </thead>
                      <tbody>
                        {data.profiles.map((item) => {
                          const selected = item.id === selectedId;
                          return (
                            <tr key={item.id} className={selected ? "is-selected" : undefined}>
                              <th scope="row">
                                <button
                                  type="button"
                                  className="link-button instance-link"
                                  aria-current={selected ? "true" : undefined}
                                  onClick={() => selectProfile(item.id, data.profiles)}
                                >
                                  {item.name}
                                </button>
                              </th>
                              <td>
                                <span className={`state-pill state-mode-${item.mode}`}>{item.mode}</span>
                              </td>
                              <td>{item.attachedInstanceCount ?? 0}</td>
                              <td>{item.system ? "system" : "custom"}</td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
                <form
                  className="policy-create-row"
                  onSubmit={(event) => {
                    event.preventDefault();
                    void onCreate();
                  }}
                >
                  <label>
                    <span className="sr-only">New profile name</span>
                    <input
                      value={newName}
                      onChange={(event) => setNewName(event.target.value)}
                      placeholder="New restricted profile"
                      autoComplete="off"
                    />
                  </label>
                  <button
                    className="btn"
                    type="submit"
                    disabled={!newName.trim() || busy !== ""}
                  >
                    {busy === "create" ? "Creating…" : "Create"}
                  </button>
                </form>
              </section>

              {profile ? (
                <section className="instance-detail policy-editor" aria-labelledby="profile-editor-heading">
                  <div className="ledger-header">
                    <h2 id="profile-editor-heading">{profile.name}</h2>
                    <div className="detail-header-meta">
                      <span><b>Attached</b> {profile.attachedInstanceCount ?? 0}</span>
                      <span className={`state-pill state-mode-${profile.mode}`}>
                        {profile.system ? "system · " : ""}{profile.mode}
                      </span>
                    </div>
                  </div>

                  <dl className="detail-grid">
                    <div className="detail-span">
                      <dt>ID</dt>
                      <dd><code title={profile.id}>{profile.id}</code></dd>
                    </div>
                  </dl>

                  <form
                    className="create-instance-form policy-form"
                    onSubmit={(event) => {
                      event.preventDefault();
                      void onSave();
                    }}
                  >
                    <fieldset>
                      <legend>Mode</legend>
                      <div className="create-choice-row" role="group" aria-label="Egress mode">
                        <button
                          type="button"
                          className={mode === "restricted" ? "choice-button is-selected" : "choice-button"}
                          aria-pressed={mode === "restricted"}
                          onClick={() => setMode("restricted")}
                        >
                          Restricted
                        </button>
                        <button
                          type="button"
                          className={mode === "standard" ? "choice-button is-selected" : "choice-button"}
                          aria-pressed={mode === "standard"}
                          onClick={() => setMode("standard")}
                        >
                          Standard
                        </button>
                      </div>
                      <p className="policy-hint">
                        {mode === "restricted"
                          ? "Outbound traffic only to the allowlist below. Hostnames are resolved on the host."
                          : "Full internet egress. The allowlist is stored but not enforced in this mode."}
                      </p>
                    </fieldset>

                    <label>
                      <span>Allowed destinations</span>
                      <textarea
                        rows={10}
                        value={destinationsText}
                        onChange={(event) => setDestinationsText(event.target.value)}
                        placeholder={"api.example.com\n203.0.113.10\n1.1.1.1/32"}
                        spellCheck={false}
                      />
                    </label>
                    <p className="policy-hint">
                      One public IP, public CIDR, or exact hostname per line. Restricted profiles reject
                      private, bridge, and 0.0.0.0/0 destinations. Empty restricted profiles still block
                      arbitrary internet and peer traffic.
                    </p>

                    <div className="create-actions">
                      <button
                        className="primary-action"
                        type="submit"
                        disabled={!dirty || busy !== ""}
                      >
                        {busy === "save" ? "Saving…" : "Save and re-apply"}
                      </button>
                      {!profile.system ? (
                        <button
                          className="btn"
                          type="button"
                          onClick={() => { void onDelete(); }}
                          disabled={busy !== "" || (profile.attachedInstanceCount ?? 0) > 0}
                          title={(profile.attachedInstanceCount ?? 0) > 0
                            ? "Detach all instances before deleting"
                            : undefined}
                        >
                          {busy === "delete" ? "Deleting…" : "Delete"}
                        </button>
                      ) : null}
                    </div>
                  </form>
                </section>
              ) : null}
            </>
          ) : null}

          {actionError ? <p className="data-message is-error" role="alert">{actionError}</p> : null}
          {applyNotes ? (
            <p className="data-message is-error" role="status">
              Profile saved, but some instances failed re-apply: {applyNotes}
            </p>
          ) : null}
          {savedNote ? <p className="data-message" role="status">{savedNote}</p> : null}
        </main>
      </div>
      <footer><span>openbox</span><span>v0.01</span></footer>
    </div>
  );
}
