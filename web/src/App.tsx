// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useState } from "react";
import { createHttpApi } from "./api/client";
import type { BootstrapStatus, OpenBoxApi, Session } from "./api/client";
import { AuthScreen } from "./auth/AuthScreen";
import { ConsolePage } from "./pages/ConsolePage";
import { LandingPage } from "./pages/LandingPage";

const defaultApi = createHttpApi();

type EntryState =
  | { status: "loading" }
  | { status: "ready"; bootstrap: BootstrapStatus; session: Session; showAuth?: boolean }
  | { status: "error"; message: string };

function messageFrom(error: unknown): string {
  return error instanceof Error ? error.message : "OpenBox could not be reached";
}

export function App({ api = defaultApi }: { api?: OpenBoxApi }) {
  const [entry, setEntry] = useState<EntryState>({ status: "loading" });

  useEffect(() => {
    let active = true;
    void Promise.all([api.getBootstrapStatus(), api.getSession()])
      .then(([bootstrap, session]) => {
        if (active) setEntry({ status: "ready", bootstrap, session });
      })
      .catch((error: unknown) => {
        if (active) setEntry({ status: "error", message: messageFrom(error) });
      });
    return () => { active = false; };
  }, [api]);

  if (entry.status === "loading") {
    return <LoadingScreen />;
  }

  if (entry.status === "error") {
    return (
      <main className="system-message" id="main-content">
        <h1>OpenBox</h1>
        <p role="alert">{entry.message}</p>
        <button className="btn" type="button" onClick={() => window.location.reload()}>Retry</button>
      </main>
    );
  }

  if (entry.bootstrap.required || !entry.session.authenticated) {
    if (entry.bootstrap.required || entry.showAuth) {
      return (
        <AuthScreen
          mode={entry.bootstrap.required ? "setup" : "login"}
          api={api}
          onAuthenticated={(session) => setEntry({
            status: "ready",
            bootstrap: { required: false },
            session,
          })}
        />
      );
    }
    return (
      <LandingPage
        onSignIn={() => setEntry({
          status: "ready",
          bootstrap: entry.bootstrap,
          session: entry.session,
          showAuth: true,
        })}
      />
    );
  }

  return (
    <ConsolePage
      api={api}
      session={entry.session}
      onLoggedOut={() => setEntry({
        status: "ready",
        bootstrap: { required: false },
        session: { authenticated: false },
      })}
    />
  );
}

function LoadingScreen() {
  return (
    <main className="system-message" id="main-content" aria-busy="true">
      <h1>OpenBox</h1>
      <p role="status">Loading…</p>
    </main>
  );
}
