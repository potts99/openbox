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
        : await api.login({
          username: String(form.get("username") ?? "").trim(),
          password,
        });
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
      <aside className="auth-mast" aria-hidden="true">
        <a className="wordmark" href="/"><span>OB</span> OpenBox</a>
      </aside>
      <main className="auth-main" id="main-content">
        <div className="auth-form-wrap">
          <a className="wordmark" href="/" aria-label="OpenBox home"><span>OB</span> OpenBox</a>
          <h1>{isSetup ? "Set up" : "Sign in"}</h1>
          <p className="lede">
            {isSetup
              ? "Use the one-time secret from the openboxd log, then choose a password."
              : "Sign in with your OpenBox username and password."}
          </p>
          <form onSubmit={(event) => { void submit(event); }} aria-describedby={error ? "auth-error" : undefined}>
            {isSetup ? (
              <label>
                <span>One-time setup secret</span>
                <input name="secret" type="password" autoComplete="one-time-code" required autoFocus />
              </label>
            ) : (
              <label>
                <span>Username</span>
                <input
                  name="username"
                  type="text"
                  autoComplete="username"
                  spellCheck={false}
                  autoCapitalize="none"
                  required
                  autoFocus
                />
              </label>
            )}
            <label>
              <span>{isSetup ? "New password" : "Password"}</span>
              <input
                name="password"
                type="password"
                autoComplete={isSetup ? "new-password" : "current-password"}
                minLength={12}
                required
              />
            </label>
            {isSetup ? (
              <label>
                <span>Confirm password</span>
                <input name="confirmation" type="password" autoComplete="new-password" minLength={12} required />
              </label>
            ) : null}
            <p className="form-error" id="auth-error" role={error ? "alert" : undefined} aria-live="assertive">{error}</p>
            <button className="primary-action" type="submit" disabled={pending}>
              {pending ? "Working…" : isSetup ? "Create owner" : "Sign in"}
            </button>
          </form>
          <p className="security-note">Credentials stay on this host.</p>
        </div>
      </main>
    </div>
  );
}
