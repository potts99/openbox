// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useRef, useState } from "react";
import type { Capabilities, InstanceSummary, OpenBoxApi, OperationSummary, Session } from "../api/client";
import { CapabilityBanner } from "../components/CapabilityBanner";
import { OperationDrawer } from "../components/OperationDrawer";
import { InstancePage } from "./InstancePage";
import { InstanceTerminal } from "./InstanceTerminal";
import { PiProfilePage } from "./PiProfile";

interface ConsolePageProps {
  api: OpenBoxApi;
  session: Extract<Session, { authenticated: true }>;
  onLoggedOut(): void;
}

type ConsoleData =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; capabilities: Capabilities; instances: InstanceSummary[]; operations: OperationSummary[] };

type View =
  | { kind: "list" }
  | { kind: "detail"; instanceId: string }
  | { kind: "terminal"; instanceId: string; instanceName: string; launchPi?: boolean }
  | { kind: "pi-profile" };

export function ConsolePage({ api, session, onLoggedOut }: ConsolePageProps) {
  const [data, setData] = useState<ConsoleData>({ status: "loading" });
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [logoutPending, setLogoutPending] = useState(false);
  const [logoutError, setLogoutError] = useState("");
  const [view, setView] = useState<View>({ kind: "list" });
  const operationsButton = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    let active = true;
    void Promise.all([api.getCapabilities(), api.listInstances(), api.listOperations()])
      .then(([capabilities, instances, operations]) => {
        if (active) setData({ status: "ready", capabilities, instances, operations });
      })
      .catch((error: unknown) => {
        if (active) setData({ status: "error", message: error instanceof Error ? error.message : "Console data unavailable" });
      });
    return () => { active = false; };
  }, [api]);

  async function logout() {
    setLogoutError("");
    setLogoutPending(true);
    try {
      await api.logout();
      onLoggedOut();
    } catch {
      setLogoutError("Could not sign out. Try again.");
    } finally {
      setLogoutPending(false);
    }
  }

  function closeDrawer() {
    setDrawerOpen(false);
    queueMicrotask(() => operationsButton.current?.focus());
  }

  if (view.kind === "terminal") {
    return (
      <InstanceTerminal
        instanceId={view.instanceId}
        instanceName={view.instanceName}
        csrfToken={session.csrfToken || api.getCsrfToken()}
        launchPi={view.launchPi}
        onBack={() => setView({ kind: "detail", instanceId: view.instanceId })}
      />
    );
  }

  if (view.kind === "detail") {
    return (
      <InstancePage
        api={api}
        instanceId={view.instanceId}
        csrfToken={session.csrfToken || api.getCsrfToken()}
        onBack={() => setView({ kind: "list" })}
        onOpenTerminal={(instance) => setView({
          kind: "terminal",
          instanceId: instance.id,
          instanceName: instance.name,
          launchPi: instance.launchPi,
        })}
      />
    );
  }

  if (view.kind === "pi-profile") {
    return <PiProfilePage api={api} onBack={() => setView({ kind: "list" })} />;
  }

  const operations = data.status === "ready" ? data.operations : [];
  return (
    <div className="console-layout">
      <a className="skip-link" href="#main-content">Skip to instances</a>
      <header className="console-header">
        <a className="wordmark" href="/" aria-label="OpenBox home"><span>OB</span> OpenBox</a>
        <nav aria-label="Primary navigation">
          <a href="#instances" aria-current="page">Instances</a>
          <button className="nav-button" type="button" onClick={() => setView({ kind: "pi-profile" })}>
            Pi profile
          </button>
          <button
            className="nav-button"
            type="button"
            ref={operationsButton}
            aria-expanded={drawerOpen}
            aria-controls="operations-drawer"
            onClick={() => setDrawerOpen((value) => !value)}
          >
            {drawerOpen ? "Hide operations" : "Show operations"}
            {operations.length > 0 ? <span className="count-badge">{operations.length}</span> : null}
          </button>
        </nav>
        <div className="owner-control">
          <span><small>OWNER</small>{session.owner.displayName}</span>
          <button type="button" onClick={() => { void logout(); }} disabled={logoutPending}>
            {logoutPending ? "Signing out…" : "Sign out"}
          </button>
        </div>
      </header>
      {logoutError ? <p className="logout-error" role="alert" aria-live="assertive">{logoutError}</p> : null}
      <div className="console-workspace">
        <main id="main-content" tabIndex={-1}>
          <div className="page-heading">
            <h1 id="instances">Instances</h1>
            <button className="primary-action" type="button" disabled title="Instance creation arrives in a later slice">
              New
            </button>
          </div>

          {data.status === "loading" ? <p className="data-message" role="status">Loading…</p> : null}
          {data.status === "error" ? <p className="data-message is-error" role="alert">{data.message}</p> : null}
          {data.status === "ready" ? (
            <>
              <CapabilityBanner capabilities={data.capabilities} />
              <section className="instance-ledger" aria-labelledby="inventory-heading">
                <div className="ledger-header">
                  <h2 id="inventory-heading">Inventory</h2>
                  <span>{data.instances.length}</span>
                </div>
                {data.instances.length === 0 ? (
                  <div className="empty-inventory">
                    <h3>No instances</h3>
                    <p>Create instances from the CLI once the runtime is available.</p>
                  </div>
                ) : (
                  <table>
                    <caption className="sr-only">OpenBox instances</caption>
                    <thead><tr><th>Name</th><th>Kind</th><th>Status</th></tr></thead>
                    <tbody>{data.instances.map((instance) => (
                      <tr key={instance.id}>
                        <th scope="row">
                          <button
                            type="button"
                            className="link-button instance-link"
                            onClick={() => setView({ kind: "detail", instanceId: instance.id })}
                          >
                            {instance.name}
                          </button>
                        </th>
                        <td>{instance.kind}</td>
                        <td>{instance.status}</td>
                      </tr>
                    ))}</tbody>
                  </table>
                )}
              </section>
            </>
          ) : null}
        </main>
        <div id="operations-drawer">
          <OperationDrawer open={drawerOpen} operations={operations} onClose={closeDrawer} />
        </div>
      </div>
      <footer><span>openbox</span><span>v1</span></footer>
    </div>
  );
}
