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
      <strong>{virtualMachinesAvailable ? "Runtime ready" : "VMs unavailable"}</strong>
      <p>{virtualMachinesAvailable
        ? "Containers and VMs available."
        : `${capabilities.vmReason ?? "KVM not detected."} Containers ${capabilities.containers ? "ok" : "unavailable"}.`}</p>
      <code>{capabilities.architecture}</code>
    </section>
  );
}
