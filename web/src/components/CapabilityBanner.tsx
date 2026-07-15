// SPDX-License-Identifier: AGPL-3.0-only

import type { Capabilities } from "../api/client";

export function CapabilityBanner({ capabilities }: { capabilities: Capabilities }) {
  const virtualMachinesAvailable = capabilities.virtualMachines && capabilities.vmAvailability === "available";
  return (
    <section
      className={`capability-strip ${virtualMachinesAvailable ? "is-ready" : "is-limited"}`}
      role="status"
      aria-label="Runtime capability status"
    >
      <div className="capability-signal" aria-hidden="true">{virtualMachinesAvailable ? "●" : "!"}</div>
      <div>
        <strong>{virtualMachinesAvailable ? "Container and virtual-machine runtime ready" : "Virtual machines unavailable"}</strong>
        <p>{virtualMachinesAvailable
          ? `${capabilities.architecture} host reports both isolation modes ready.`
          : `${capabilities.vmReason ?? "KVM support was not detected."} Containers remain ${capabilities.containers ? "available" : "unavailable"}.`}</p>
      </div>
      <code>{capabilities.architecture}</code>
    </section>
  );
}
