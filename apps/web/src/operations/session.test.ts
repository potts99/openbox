// SPDX-License-Identifier: AGPL-3.0-only

import { describe, expect, it } from "vitest";
import { OperationEventsSession, operationEventsUrl } from "./session";

type Listener = (event: MessageEvent<string>) => void;

class MockEventSource {
  static instances: MockEventSource[] = [];
  url: string;
  closed = false;
  onerror: (() => void) | null = null;
  private listeners = new Map<string, Listener[]>();

  constructor(url: string) {
    this.url = url;
    MockEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: Listener): void {
    const current = this.listeners.get(type) ?? [];
    current.push(listener);
    this.listeners.set(type, current);
  }

  close(): void {
    this.closed = true;
  }

  emit(type: string, data: unknown, id = ""): void {
    const event = { data: JSON.stringify(data), lastEventId: id } as MessageEvent<string>;
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }

  fail(): void {
    this.onerror?.();
  }
}

describe("OperationEventsSession", () => {
  it("builds the versioned operation events URL", () => {
    expect(operationEventsUrl("op-1")).toBe("/v1/operations/op-1/events");
  });

  it("replays retained events and closes on terminal status", () => {
    MockEventSource.instances = [];
    const statuses: string[] = [];
    const events: Array<{ stage: string; status: string }> = [];
    const session = new OperationEventsSession({
      operationId: "op-1",
      EventSourceImpl: MockEventSource as unknown as typeof EventSource,
      onStatus: (status) => statuses.push(status),
      onEvent: (event) => events.push({ stage: event.stage, status: event.status }),
    });

    session.connect();
    const source = MockEventSource.instances[0];
    expect(source.url).toBe("/v1/operations/op-1/events");

    source.emit("operation", {
      sequence: 1,
      operation_id: "op-1",
      stage: "runtime",
      status: "running",
      progress: 20,
      created_at: "2026-07-15T12:00:00Z",
    }, "1");
    source.emit("operation", {
      sequence: 2,
      operation_id: "op-1",
      stage: "complete",
      status: "succeeded",
      progress: 100,
      created_at: "2026-07-15T12:01:00Z",
    }, "2");

    expect(events).toEqual([
      { stage: "runtime", status: "running" },
      { stage: "complete", status: "succeeded" },
    ]);
    expect(statuses).toContain("live");
    expect(statuses).toContain("complete");
    expect(statuses).not.toContain("closed");
    expect(statuses.at(-1)).toBe("complete");
    expect(source.closed).toBe(true);
  });

  it("reports stream errors from server error events", () => {
    MockEventSource.instances = [];
    const errors: string[] = [];
    const session = new OperationEventsSession({
      operationId: "op-2",
      EventSourceImpl: MockEventSource as unknown as typeof EventSource,
      onError: (detail) => errors.push(detail),
      onStatus: (status) => {
        if (status === "error") errors.push("status-error");
      },
    });

    session.connect();
    MockEventSource.instances[0].emit("error", {
      error: { message: "The event stream could not be continued." },
    });

    expect(errors).toContain("The event stream could not be continued.");
    expect(errors).toContain("status-error");
    session.close();
  });

  it("closes the active stream on cleanup", () => {
    MockEventSource.instances = [];
    const session = new OperationEventsSession({
      operationId: "op-3",
      EventSourceImpl: MockEventSource as unknown as typeof EventSource,
    });
    session.connect();
    const source = MockEventSource.instances[0];
    session.close();
    expect(source.closed).toBe(true);
  });
});
