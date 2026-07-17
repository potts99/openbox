// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useRef, useState } from "react";
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
  if (!value) return "Never";
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

function relativeWhen(value?: string): string {
  if (!value) return "Never used";
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

function activeTokens(tokens: TokenSummary[]): TokenSummary[] {
  return tokens.filter((token) => !token.revokedAt);
}

export function SettingsPage({ api, session, onBack }: SettingsPageProps) {
  const [data, setData] = useState<TokenData>({ status: "loading" });
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState("");
  const [listError, setListError] = useState("");
  const [created, setCreated] = useState<CreatedToken | null>(null);
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">("idle");
  const [revokingId, setRevokingId] = useState("");
  const secretRef = useRef<HTMLInputElement>(null);
  const nameRef = useRef<HTMLInputElement>(null);

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

  useEffect(() => {
    if (!created) return;
    secretRef.current?.focus();
    secretRef.current?.select();
  }, [created]);

  async function createToken(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) {
      setCreateError("Give this token a name.");
      nameRef.current?.focus();
      return;
    }
    setCreateError("");
    setListError("");
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
      secretRef.current?.select();
    }
  }

  async function revokeToken(id: string, tokenName: string) {
    if (!window.confirm(`Revoke “${tokenName}”? CLI clients using it will stop working.`)) {
      return;
    }
    setRevokingId(id);
    setListError("");
    try {
      await api.revokeToken(id);
      if (created?.id === id) {
        setCreated(null);
        setCopyState("idle");
      }
      await loadTokens();
    } catch (reason) {
      setListError(reason instanceof Error ? reason.message : "Could not revoke token");
    } finally {
      setRevokingId("");
    }
  }

  const tokens = data.status === "ready" ? data.tokens : [];

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
            </div>
          </div>

          <section className="settings-account" aria-label="Account">
            <div>
              <span className="settings-kicker">Signed in</span>
              <p className="settings-account-name">{session.username}</p>
            </div>
            <span className="settings-role">{session.role === "admin" ? "Admin" : "Member"}</span>
          </section>

          <section className="settings-section" aria-labelledby="api-tokens-heading">
            <div className="ledger-header">
              <h2 id="api-tokens-heading">API tokens</h2>
              {data.status === "ready" ? <span>{tokens.length}</span> : null}
            </div>
            <div className="settings-body">
              <p className="settings-lede">
                For the <code>openbox</code> CLI on your machine. Create a token, copy it once,
                then run <code>export OPENBOX_TOKEN=…</code> before <code>openbox doctor</code>.
              </p>

              {created ? (
                <div className="token-secret-panel" role="status">
                  <div className="token-secret-header">
                    <div>
                      <h3>Token ready — copy it now</h3>
                      <p>
                        <strong>{created.name}</strong> · shown only once
                      </p>
                    </div>
                    <button
                      type="button"
                      className="nav-button"
                      onClick={() => {
                        setCreated(null);
                        setCopyState("idle");
                      }}
                    >
                      Done
                    </button>
                  </div>
                  <label className="token-secret-field">
                    <span className="sr-only">Token secret</span>
                    <input
                      ref={secretRef}
                      className="token-secret"
                      type="text"
                      readOnly
                      value={created.secret}
                      onFocus={(event) => event.currentTarget.select()}
                    />
                  </label>
                  <div className="token-secret-actions">
                    <button className="primary-action" type="button" onClick={() => { void copySecret(); }}>
                      {copyState === "copied" ? "Copied" : "Copy token"}
                    </button>
                    {copyState === "failed" ? (
                      <span className="data-message is-error">Copy failed — select the field and copy manually.</span>
                    ) : (
                      <span className="token-secret-hint">Paste into your shell as <code>OPENBOX_TOKEN</code>.</span>
                    )}
                  </div>
                </div>
              ) : (
                <form
                  className="settings-token-form"
                  onSubmit={(event) => { void createToken(event); }}
                >
                  <label className="settings-token-name">
                    <span>Name</span>
                    <input
                      ref={nameRef}
                      name="name"
                      type="text"
                      value={name}
                      onChange={(event) => setName(event.target.value)}
                      placeholder="e.g. laptop"
                      maxLength={100}
                      autoComplete="off"
                      spellCheck={false}
                      required
                    />
                  </label>
                  <button className="primary-action" type="submit" disabled={creating}>
                    {creating ? "Creating…" : "Create token"}
                  </button>
                </form>
              )}

              {createError ? <p className="form-error" role="alert">{createError}</p> : null}
              {listError ? <p className="form-error" role="alert">{listError}</p> : null}

              {data.status === "loading" ? <p className="data-message" role="status">Loading…</p> : null}
              {data.status === "error" ? <p className="data-message is-error" role="alert">{data.message}</p> : null}

              {data.status === "ready" ? (
                tokens.length === 0 ? (
                  <div className="settings-empty">
                    <p>No tokens yet. Create one to use the CLI.</p>
                  </div>
                ) : (
                  <ul className="token-list">
                    {tokens.map((token) => (
                      <li key={token.id} className="token-row">
                        <div className="token-row-main">
                          <strong>{token.name}</strong>
                          <span title={formatWhen(token.createdAt)}>
                            Created {formatWhen(token.createdAt)}
                          </span>
                          <span title={formatWhen(token.lastUsedAt)}>
                            {relativeWhen(token.lastUsedAt)}
                          </span>
                        </div>
                        <button
                          className="link-button token-revoke"
                          type="button"
                          disabled={revokingId === token.id}
                          onClick={() => { void revokeToken(token.id, token.name); }}
                        >
                          {revokingId === token.id ? "Revoking…" : "Revoke"}
                        </button>
                      </li>
                    ))}
                  </ul>
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
