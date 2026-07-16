// SPDX-License-Identifier: AGPL-3.0-only

import type { Capabilities } from "../api/client";

export function CapabilityBanner({ capabilities }: { capabilities: Capabilities }) {
  const virtualMachinesAvailable = capabilities.virtualMachines && capabilities.vmAvailability === "supported";
  return (
    <section
      className={`capability-strip ${virtualMachinesAvailable ? "is-ready" : "is-limited"}`}
      role="status"
      aria-label="Runtime capability status"
    >
      <strong>{virtualMachinesAvailable ? "Runtime ready" : "Limited (no KVM)"}</strong>
      <p>{virtualMachinesAvailable
        ? "KVM VMs available. Default isolation is strong."
        : `${capabilities.vmReason ?? "KVM not detected."} Default isolation is container. Strong requests will fail.`}</p>
      <code>{capabilities.architecture}</code>
    </section>
  );
}
