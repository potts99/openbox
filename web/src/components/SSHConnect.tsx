// SPDX-License-Identifier: AGPL-3.0-only

import { useState } from "react";
import type { ConnectionInfo } from "../api/client";
import { formatSSHCommand } from "../ssh/command";

interface SSHConnectProps {
  instanceName: string;
  connection: ConnectionInfo;
}

export function SSHConnect({ instanceName, connection }: SSHConnectProps) {
  const [copied, setCopied] = useState(false);

  if (connection.ssh == null) {
    return (
      <section className="instance-detail ssh-connect" aria-labelledby="ssh-connect-heading">
        <div className="ledger-header">
          <h2 id="ssh-connect-heading">Connect</h2>
        </div>
        <p className="ssh-connect-note">SSH endpoint is not configured on this server.</p>
      </section>
    );
  }

  const command = formatSSHCommand(instanceName, connection.ssh.host, connection.ssh.port);

  async function copyCommand() {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
    }
  }

  return (
    <section className="instance-detail ssh-connect" aria-labelledby="ssh-connect-heading">
      <div className="ledger-header">
        <h2 id="ssh-connect-heading">Connect</h2>
      </div>
      <div className="ssh-connect-row">
        <code className="ssh-connect-command">{command}</code>
        <button type="button" className="btn" onClick={() => { void copyCommand(); }}>
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <p className="ssh-connect-note">Use a registered owner SSH key.</p>
    </section>
  );
}
