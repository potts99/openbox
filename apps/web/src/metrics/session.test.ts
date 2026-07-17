// SPDX-License-Identifier: AGPL-3.0-only

import { describe, expect, it, vi } from "vitest";
import { MetricsSession, metricsWebSocketUrl } from "./session";

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

describe("metrics session", () => {
  it("builds csrf websocket url", () => {
    expect(metricsWebSocketUrl("box-1", "tok", { protocol: "https:", host: "example.test" }))
      .toBe("wss://example.test/v1/instances/box-1/metrics?csrf=tok");
  });

  it("delivers snapshot and sample frames", () => {
    FakeWebSocket.instances = [];
    const onSnapshot = vi.fn();
    const onSample = vi.fn();
    const onStatus = vi.fn();
    const session = new MetricsSession({
      instanceId: "box-1",
      csrfToken: "csrf",
      WebSocketImpl: FakeWebSocket as unknown as typeof WebSocket,
      location: { protocol: "http:", host: "localhost" },
      onSnapshot,
      onSample,
      onStatus,
    });
    session.connect();
    const socket = FakeWebSocket.instances[0];
    expect(socket?.url).toContain("/metrics?csrf=csrf");
    socket?.emit("open");
    expect(onStatus).toHaveBeenCalledWith("live");
    socket?.emit("message", JSON.stringify({
      type: "snapshot",
      interval_seconds: 10,
      limits: { vcpus: 2, memory_bytes: 1024, disk_bytes: 2048 },
      samples: [{ t: "2026-07-15T12:00:00Z", memory_bytes: 100, disk_bytes: 200, cpu_percent: 5 }],
    }));
    expect(onSnapshot).toHaveBeenCalledWith({
      intervalSeconds: 10,
      limits: { vcpus: 2, memoryBytes: 1024, diskBytes: 2048 },
      samples: [{ at: "2026-07-15T12:00:00Z", memoryBytes: 100, diskBytes: 200, cpuPercent: 5 }],
    });
    socket?.emit("message", JSON.stringify({
      type: "sample",
      sample: { t: "2026-07-15T12:00:10Z", memory_bytes: 110, disk_bytes: 210, net_rx_bps: 12 },
    }));
    expect(onSample).toHaveBeenCalledWith({
      at: "2026-07-15T12:00:10Z",
      memoryBytes: 110,
      diskBytes: 210,
      netRxBps: 12,
    });
    session.close();
  });
});
