// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { CreatedToken, OpenBoxApi, Session, TokenSummary } from "../api/client";

interface SettingsPageProps {
  api: OpenBoxApi;
  session: Extract<Session, { authenticated: true }>;
  onBack(): void;
}

type TokenData =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; tokens: TokenSummary[] };

function formatWhen(value?: string): string {
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

function activeTokens(tokens: TokenSummary[]): TokenSummary[] {
  return tokens.filter((token) => !token.revokedAt);
}

export function SettingsPage({ api, session, onBack }: SettingsPageProps) {
  const [data, setData] = useState<TokenData>({ status: "loading" });
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState("");
  const [created, setCreated] = useState<CreatedToken | null>(null);
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">("idle");
  const [revokingId, setRevokingId] = useState("");

  async function loadTokens() {
    const tokens = await api.listTokens();
    setData({ status: "ready", tokens: activeTokens(tokens) });
  }

  useEffect(() => {
    let active = true;
    void api.listTokens()
      .then((tokens) => {
        if (active) setData({ status: "ready", tokens: activeTokens(tokens) });
      })
      .catch((reason: unknown) => {
        if (active) {
          setData({
            status: "error",
            message: reason instanceof Error ? reason.message : "API tokens unavailable",
          });
        }
      });
    return () => { active = false; };
  }, [api]);

  async function createToken(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) {
      setCreateError("Give this token a name.");
      return;
    }
    setCreateError("");
    setCreating(true);
    setCopyState("idle");
    try {
      const token = await api.createToken({ name: trimmed });
      setCreated(token);
      setName("");
      await loadTokens();
    } catch (reason) {
      setCreateError(reason instanceof Error ? reason.message : "Could not create token");
    } finally {
      setCreating(false);
    }
  }

  async function copySecret() {
    if (!created?.secret) return;
    try {
      await navigator.clipboard.writeText(created.secret);
      setCopyState("copied");
    } catch {
      setCopyState("failed");
    }
  }

  async function revokeToken(id: string) {
    setRevokingId(id);
    setCreateError("");
    try {
      await api.revokeToken(id);
      if (created?.id === id) setCreated(null);
      await loadTokens();
    } catch (reason) {
      setCreateError(reason instanceof Error ? reason.message : "Could not revoke token");
    } finally {
      setRevokingId("");
    }
  }

  return (
    <div className="console-layout">
      <a className="skip-link" href="#settings-main">Skip to settings</a>
      <header className="console-header">
        <a className="wordmark" href="/" aria-label="OpenBox home"><span>OB</span> OpenBox</a>
        <nav aria-label="Primary navigation">
          <button className="nav-button" type="button" onClick={onBack}>Instances</button>
          <button className="nav-button" type="button" aria-current="page">Settings</button>
        </nav>
      </header>
      <div className="console-workspace">
        <main id="settings-main" tabIndex={-1}>
          <div className="page-heading">
            <div>
              <button className="link-button instance-back" type="button" onClick={onBack}>
                ← Instances
              </button>
              <h1>Settings</h1>
              <p className="data-message">
                Signed in as <strong>{session.username}</strong>
                {session.role === "admin" ? " (admin)" : ""}.
              </p>
            </div>
          </div>

          <section className="settings-section" aria-labelledby="api-tokens-heading">
            <div className="ledger-header">
              <h2 id="api-tokens-heading">API tokens</h2>
            </div>
            <div className="settings-body">
              <p className="data-message">
                Tokens let the <code>openbox</code> CLI talk to this host. Create one, copy it once,
                then export <code>OPENBOX_TOKEN</code>.
              </p>

              <form className="create-instance-form settings-token-form" onSubmit={(event) => { void createToken(event); }}>
                <label>
                  Token name
                  <input
                    name="name"
                    type="text"
                    value={name}
                    onChange={(event) => setName(event.target.value)}
                    placeholder="laptop"
                    maxLength={100}
                    autoComplete="off"
                    spellCheck={false}
                    required
                  />
                </label>
                <div className="create-actions">
                  <button className="primary-action" type="submit" disabled={creating}>
                    {creating ? "Creating…" : "Create token"}
                  </button>
                </div>
              </form>

              {createError ? <p className="form-error" role="alert">{createError}</p> : null}

              {created ? (
                <div className="token-secret-panel" role="status">
                  <h3>Copy this token now</h3>
                  <p>It will not be shown again.</p>
                  <code className="token-secret">{created.secret}</code>
                  <div className="token-secret-actions">
                    <button className="primary-action" type="button" onClick={() => { void copySecret(); }}>
                      {copyState === "copied" ? "Copied" : "Copy token"}
                    </button>
                    {copyState === "failed" ? (
                      <span className="data-message is-error">Copy failed — select the token and copy manually.</span>
                    ) : null}
                  </div>
                  <pre className="token-export-hint">{`export OPENBOX_TOKEN='${created.secret}'\nopenbox doctor`}</pre>
                </div>
              ) : null}

              {data.status === "loading" ? <p className="data-message" role="status">Loading…</p> : null}
              {data.status === "error" ? <p className="data-message is-error" role="alert">{data.message}</p> : null}

              {data.status === "ready" ? (
                data.tokens.length === 0 ? (
                  <p className="data-message">No active tokens yet.</p>
                ) : (
                  <div className="table-wrap">
                    <table>
                      <caption className="sr-only">API tokens</caption>
                      <thead>
                        <tr>
                          <th scope="col">Name</th>
                          <th scope="col">Created</th>
                          <th scope="col">Last used</th>
                          <th scope="col"><span className="sr-only">Actions</span></th>
                        </tr>
                      </thead>
                      <tbody>
                        {data.tokens.map((token) => (
                          <tr key={token.id}>
                            <th scope="row">{token.name}</th>
                            <td>{formatWhen(token.createdAt)}</td>
                            <td>{formatWhen(token.lastUsedAt)}</td>
                            <td>
                              <button
                                className="nav-button"
                                type="button"
                                disabled={revokingId === token.id}
                                onClick={() => { void revokeToken(token.id); }}
                              >
                                {revokingId === token.id ? "Revoking…" : "Revoke"}
                              </button>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )
              ) : null}
            </div>
          </section>
        </main>
      </div>
      <footer><span>openbox</span><span>v0.01</span></footer>
    </div>
  );
}
