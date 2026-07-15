// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useRef, useState } from "react";
import type { Capabilities, InstanceSummary, OpenBoxApi, OperationSummary, Session } from "../api/client";
import { CapabilityBanner } from "../components/CapabilityBanner";
import { OperationDrawer } from "../components/OperationDrawer";
import { InstanceTerminal } from "./InstanceTerminal";

interface ConsolePageProps {
  api: OpenBoxApi;
  session: Extract<Session, { authenticated: true }>;
  onLoggedOut(): void;
}

type ConsoleData =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; capabilities: Capabilities; instances: InstanceSummary[]; operations: OperationSummary[] };

export function ConsolePage({ api, session, onLoggedOut }: ConsolePageProps) {
  const [data, setData] = useState<ConsoleData>({ status: "loading" });
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [logoutPending, setLogoutPending] = useState(false);
  const [logoutError, setLogoutError] = useState("");
  const [terminalInstance, setTerminalInstance] = useState<InstanceSummary | null>(null);
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
      setLogoutError("OpenBox could not lock this session. Try again.");
    } finally {
      setLogoutPending(false);
    }
  }

  function closeDrawer() {
    setDrawerOpen(false);
    queueMicrotask(() => operationsButton.current?.focus());
  }

  if (terminalInstance) {
    return (
      <InstanceTerminal
        instanceId={terminalInstance.id}
        instanceName={terminalInstance.name}
        csrfToken={session.csrfToken || api.getCsrfToken()}
        onBack={() => setTerminalInstance(null)}
      />
    );
  }

  const operations = data.status === "ready" ? data.operations : [];
  return (
    <div className="console-layout">
      <a className="skip-link" href="#main-content">Skip to instances</a>
      <header className="console-header">
        <a className="wordmark" href="/" aria-label="OpenBox home"><span>OB</span> OpenBox</a>
        <nav aria-label="Primary navigation">
          <a href="#instances" aria-current="page">Instances</a>
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
            {logoutPending ? "Locking…" : "Lock"}
          </button>
        </div>
      </header>
      {logoutError ? <p className="logout-error" role="alert" aria-live="assertive">{logoutError}</p> : null}
      <div className="console-workspace">
        <main id="main-content" tabIndex={-1}>
          <div className="page-heading">
            <div>
              <p className="eyebrow">Compute inventory / local host</p>
              <h1 id="instances">Instances</h1>
            </div>
            <button className="primary-action" type="button" disabled title="Instance creation arrives in a later slice">
              + New instance
            </button>
          </div>

          {data.status === "loading" ? <p className="data-message" role="status">Reading runtime state…</p> : null}
          {data.status === "error" ? <p className="data-message is-error" role="alert">{data.message}</p> : null}
          {data.status === "ready" ? (
            <>
              <CapabilityBanner capabilities={data.capabilities} />
              <section className="instance-ledger" aria-labelledby="inventory-heading">
                <div className="ledger-header">
                  <h2 id="inventory-heading">Host inventory</h2>
                  <span>{data.instances.length.toString().padStart(2, "0")} ACTIVE RECORDS</span>
                </div>
                {data.instances.length === 0 ? (
                  <div className="empty-inventory">
                    <div className="empty-glyph" aria-hidden="true"><span /><span /><span /></div>
                    <div>
                      <h3>No instances yet</h3>
                      <p>This control plane is ready. Instance creation will be enabled in the next delivery slice.</p>
                    </div>
                  </div>
                ) : (
                  <table>
                    <caption className="sr-only">OpenBox instances</caption>
                    <thead><tr><th>Name</th><th>Kind</th><th>Status</th><th>Terminal</th></tr></thead>
                    <tbody>{data.instances.map((instance) => (
                      <tr key={instance.id}>
                        <th scope="row">{instance.name}</th>
                        <td>{instance.kind}</td>
                        <td>{instance.status}</td>
                        <td>
                          <button
                            type="button"
                            className="link-button"
                            aria-label={`Open terminal for ${instance.name}`}
                            onClick={() => setTerminalInstance(instance)}
                          >
                            Open terminal
                          </button>
                        </td>
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
      <footer><span>OPENBOX CONTROL PLANE</span><span>API V1</span></footer>
    </div>
  );
}
