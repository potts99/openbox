// SPDX-License-Identifier: AGPL-3.0-only

const pendingKey = "openbox.setupChecklist.pending";
const dismissedKey = "openbox.setupChecklist.dismissed";

export function markSetupChecklistPending(username: string): void {
  try {
    window.localStorage.setItem(pendingKey, username);
    window.localStorage.removeItem(dismissedKey);
  } catch {
    /* ignore quota / private mode */
  }
}

export function readSetupChecklistUsername(): string {
  try {
    if (window.localStorage.getItem(dismissedKey) === "1") return "";
    return window.localStorage.getItem(pendingKey) ?? "";
  } catch {
    return "";
  }
}

export function dismissSetupChecklist(): void {
  try {
    window.localStorage.setItem(dismissedKey, "1");
    window.localStorage.removeItem(pendingKey);
  } catch {
    /* ignore */
  }
}

interface SetupChecklistProps {
  username: string;
  onDismiss(): void;
  onOpenSettings(): void;
}

export function SetupChecklist({ username, onDismiss, onOpenSettings }: SetupChecklistProps) {
  return (
    <section className="setup-checklist" aria-labelledby="setup-checklist-heading">
      <div className="setup-checklist-header">
        <h2 id="setup-checklist-heading">You are set up</h2>
        <button type="button" className="nav-button" onClick={onDismiss}>Dismiss</button>
      </div>
      <p>Signed in as <strong>{username}</strong>. A few useful next steps:</p>
      <ol>
        <li>
          <button type="button" className="link-button" onClick={onOpenSettings}>
            Create an API token in Settings
          </button>
          {" "}for the CLI.
        </li>
        <li>Export <code>OPENBOX_TOKEN</code> and run <code>openbox doctor</code>.</li>
        <li>Create your first instance from this console.</li>
      </ol>
    </section>
  );
}
