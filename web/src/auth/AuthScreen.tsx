// SPDX-License-Identifier: AGPL-3.0-only

import { useState } from "react";
import type { FormEvent } from "react";
import type { OpenBoxApi, Session } from "../api/client";

interface AuthScreenProps {
  mode: "setup" | "login";
  api: OpenBoxApi;
  onAuthenticated(session: Session): void;
}

export function AuthScreen({ mode, api, onAuthenticated }: AuthScreenProps) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    const form = new FormData(event.currentTarget);
    const password = String(form.get("password") ?? "");
    if (mode === "setup" && password !== String(form.get("confirmation") ?? "")) {
      setError("Passwords do not match");
      return;
    }
    setPending(true);
    try {
      const session = mode === "setup"
        ? await api.setup({ secret: String(form.get("secret") ?? ""), password })
        : await api.login({ password });
      onAuthenticated(session);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Authentication failed");
    } finally {
      setPending(false);
    }
  }

  const isSetup = mode === "setup";
  return (
    <div className="auth-layout">
      <a className="skip-link" href="#main-content">Skip to authentication</a>
      <aside className="auth-mast" aria-label="OpenBox identity">
        <a className="wordmark" href="/" aria-label="OpenBox home"><span>OB</span> OpenBox</a>
        <div>
          <p className="eyebrow">Self-hosted compute / local authority</p>
          <p className="auth-statement">The control surface belongs on the machine it controls.</p>
        </div>
        <p className="build-mark">OPENBOX // OWNER CONSOLE</p>
      </aside>
      <main className="auth-main" id="main-content">
        <div className="auth-form-wrap">
          <p className="step-label">{isSetup ? "First-run procedure 01" : "Restricted access"}</p>
          <h1>{isSetup ? "Claim this OpenBox" : "Return to your OpenBox"}</h1>
          <p className="lede">
            {isSetup
              ? "Use the one-time secret printed by openboxd. It expires and cannot be used again."
              : "Authenticate as the local owner. Your session stays in a protected browser cookie."}
          </p>
          <form onSubmit={(event) => { void submit(event); }} aria-describedby={error ? "auth-error" : undefined}>
            {isSetup ? (
              <label>
                <span>One-time setup secret</span>
                <input name="secret" type="password" autoComplete="one-time-code" required autoFocus />
              </label>
            ) : null}
            <label>
              <span>{isSetup ? "New password" : "Password"}</span>
              <input name="password" type="password" autoComplete={isSetup ? "new-password" : "current-password"} minLength={12} required autoFocus={!isSetup} />
            </label>
            {isSetup ? (
              <label>
                <span>Confirm password</span>
                <input name="confirmation" type="password" autoComplete="new-password" minLength={12} required />
              </label>
            ) : null}
            <p className="form-error" id="auth-error" role={error ? "alert" : undefined} aria-live="assertive">{error}</p>
            <button className="primary-action" type="submit" disabled={pending}>
              {pending ? "Working…" : isSetup ? "Claim OpenBox" : "Unlock console"}
            </button>
          </form>
          <p className="security-note"><span aria-hidden="true">●</span> Credentials remain on this OpenBox.</p>
        </div>
      </main>
    </div>
  );
}
