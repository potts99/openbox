// SPDX-License-Identifier: AGPL-3.0-only

import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { InstanceMetrics } from "./InstanceMetrics";

class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  url: string;
  listeners = new Map<string, Set<(event: { data?: string }) => void>>();
  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }
  addEventListener(type: string, fn: (event: { data?: string }) => void) {
    const set = this.listeners.get(type) ?? new Set();
    set.add(fn);
    this.listeners.set(type, set);
  }
  close() {}
  emit(type: string, data?: string) {
    for (const fn of this.listeners.get(type) ?? []) fn({ data });
  }
}

describe("InstanceMetrics", () => {
  it("renders snapshot readouts", async () => {
    FakeWebSocket.instances = [];
    render(
      <InstanceMetrics
        instanceId="box-1"
        csrfToken="csrf"
        vcpus={2}
        memoryBytes={4 * 1024 ** 3}
        diskBytes={20 * 1024 ** 3}
        WebSocketImpl={FakeWebSocket as unknown as typeof WebSocket}
      />,
    );
    const socket = FakeWebSocket.instances.at(-1);
    socket?.emit("open");
    socket?.emit("message", JSON.stringify({
      type: "snapshot",
      interval_seconds: 10,
      limits: { vcpus: 2, memory_bytes: 4 * 1024 ** 3, disk_bytes: 20 * 1024 ** 3 },
      samples: [{
        t: "2026-07-15T12:00:00Z",
        cpu_percent: 12.5,
        memory_bytes: 1 * 1024 ** 3,
        disk_bytes: 5 * 1024 ** 3,
        net_rx_bps: 2048,
        net_tx_bps: 1024,
      }],
    }));

    expect(await screen.findByRole("heading", { name: "Monitoring" })).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByText("12.5%")).toBeInTheDocument();
      expect(screen.getByText("1 GiB / 4 GiB")).toBeInTheDocument();
      expect(screen.getByText("5 GiB / 20 GiB")).toBeInTheDocument();
    });
    expect(screen.getByText("live")).toBeInTheDocument();
  });
});
