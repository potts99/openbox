// SPDX-License-Identifier: AGPL-3.0-only

import { describe, expect, it, vi } from "vitest";
import { createHttpApi } from "./client";

describe("createHttpApi", () => {
  it("sends cookie credentials, CSRF, and the API version for mutations", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ authenticated: true }), {
      status: 200,
      headers: { "content-type": "application/json", "x-openbox-csrf-token": "next-token" },
    }));
    const api = createHttpApi({ fetcher, csrfToken: "csrf-token" });

    await api.login({ username: "owner-local", password: "secret" });

    expect(fetcher).toHaveBeenCalledWith("/v1/sessions", expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ password: "secret", username: "owner-local" }),
      credentials: "same-origin",
      headers: expect.objectContaining({
        "Content-Type": "application/json",
        "X-CSRF-Token": "csrf-token",
        "X-OpenBox-API-Version": "v1",
      }),
    }));
  });

  it("maps API errors to safe display messages", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: { code: "unauthenticated", message: "password hash: $argon2id$secret" },
    }), { status: 401, headers: { "content-type": "application/json" } }));

    await expect(createHttpApi({ fetcher }).login({ password: "bad" })).rejects.toThrow("Invalid credentials");
  });

  it("maps bootstrap contract errors without exposing server detail", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: { code: "bootstrap_unavailable", message: "stored bootstrap hash was consumed at /private/path" },
    }), { status: 409, headers: { "content-type": "application/json" } }));

    await expect(createHttpApi({ fetcher }).setup({ secret: "expired", password: "password-value" }))
      .rejects.toThrow("Setup is unavailable. Restart openboxd to issue a new one-time secret.");
  });

  it("treats a 401 session probe as logged out and normalizes backend session fields", async () => {
    const loggedOutFetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: { code: "unauthorized" },
    }), { status: 401, headers: { "content-type": "application/json" } }));
    await expect(createHttpApi({ fetcher: loggedOutFetch }).getSession()).resolves.toEqual({ authenticated: false });

    const loggedInFetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      owner_id: "owner-local",
      user_id: "usr_owner-local",
      username: "owner-local",
      role: "admin",
      expires_at: "2026-07-15T18:00:00Z",
      csrf_token: "rotated-token",
    }), { status: 200, headers: { "content-type": "application/json" } }));
    const api = createHttpApi({ fetcher: loggedInFetch });
    await expect(api.getSession()).resolves.toEqual({
      authenticated: true,
      owner: { displayName: "Owner" },
      userId: "usr_owner-local",
      username: "owner-local",
      role: "admin",
      csrfToken: "rotated-token",
    });
    expect(api.getCsrfToken()).toBe("rotated-token");
  });

  it("accepts the v1 items envelopes", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ items: [{
        id: "box-1", name: "dev", kind: "vps", image_id: "ubuntu", requested_isolation: "strong",
        actual_isolation: "container", desired_state: "running", observed_state: "running", resources: {}, protected: false,
        created_at: "now", updated_at: "now",
      }] }), {
        status: 200, headers: { "content-type": "application/json" },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ items: [{
        id: "op-1", type: "create", target_type: "instance", target_id: "box-1", status: "running", stage: "runtime_create",
        progress: 40, attempts: 1, created_at: "now", updated_at: "now",
      }] }), {
        status: 200, headers: { "content-type": "application/json" },
      }));
    const api = createHttpApi({ fetcher });

    await expect(api.listInstances()).resolves.toEqual([{ id: "box-1", name: "dev", kind: "vps", status: "running" }]);
    await expect(api.listOperations()).resolves.toEqual([{
      id: "op-1",
      action: "create",
      targetType: "instance",
      target: "box-1",
      status: "running",
      stage: "runtime_create",
      progress: 40,
      attempts: 1,
      createdAt: "now",
      updatedAt: "now",
    }]);
  });

  it("uses checkpoint routes, maps readiness, and sends idempotency keys", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ items: [{
        id: "snap-1", instance_id: "box-1", name: "ready", ready: false, created_at: "now",
      }] }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        instance: { id: "box-2", name: "worker", kind: "vps", requested_isolation: "container", actual_isolation: "container",
          desired_state: "running", observed_state: "creating", resources: {}, protected: false, created_at: "now", updated_at: "now" },
        operation: { id: "op-1", status: "pending" }, warnings: [], storage_efficiency: "confirmed",
      }), { status: 202, headers: { "content-type": "application/json" } }));
    const api = createHttpApi({ fetcher, csrfToken: "csrf-token" });

    await expect(api.listSnapshots("box-1")).resolves.toEqual([{
      id: "snap-1", instanceId: "box-1", name: "ready", ready: false, createdAt: "now",
    }]);
    await api.restoreSnapshot("snap-1", "worker", "ssh-ed25519 owner");

    expect(fetcher).toHaveBeenNthCalledWith(1, "/v1/instances/box-1/snapshots", expect.objectContaining({
      headers: expect.objectContaining({ "X-OpenBox-API-Version": "v1" }),
    }));
    expect(fetcher).toHaveBeenNthCalledWith(2, "/v1/snapshots/snap-1/restore", expect.objectContaining({
      method: "POST",
      headers: expect.objectContaining({ "Idempotency-Key": expect.any(String) }),
    }));
  });

  it("loads operation detail and subscribes to retained events", async () => {
    MockEventSource.instances = [];
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      id: "op-2",
      type: "instance.start",
      target_type: "instance",
      target_id: "box-1",
      status: "running",
      stage: "runtime",
      progress: 10,
      attempts: 1,
      created_at: "now",
      updated_at: "now",
    }), { status: 200, headers: { "content-type": "application/json" } }));
    const api = createHttpApi({ fetcher });

    await expect(api.getOperation("op-2")).resolves.toMatchObject({
      id: "op-2",
      action: "instance.start",
      target: "box-1",
      stage: "runtime",
      progress: 10,
    });

    const events: number[] = [];
    const subscription = api.subscribeOperationEvents("op-2", {
      onEvent: (event) => events.push(event.sequence),
    }, { EventSourceImpl: MockEventSource as unknown as typeof EventSource });

    expect(MockEventSource.instances[0]?.url).toBe("/v1/operations/op-2/events");
    MockEventSource.instances[0]?.emit("operation", {
      sequence: 1,
      operation_id: "op-2",
      stage: "runtime",
      status: "running",
      progress: 10,
      created_at: "now",
    });
    subscription.close();
    expect(events).toEqual([1]);
    expect(MockEventSource.instances[0]?.closed).toBe(true);
  });

  it("loads connection ssh endpoint and null when unset", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        ssh: { host: "app.example.com", port: 2222 },
      }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        ssh: null,
      }), { status: 200, headers: { "content-type": "application/json" } }));
    const api = createHttpApi({ fetcher, csrfToken: "csrf" });

    await expect(api.getConnection()).resolves.toEqual({
      ssh: { host: "app.example.com", port: 2222 },
    });
    await expect(api.getConnection()).resolves.toEqual({ ssh: null });
    expect(fetcher).toHaveBeenCalledWith("/v1/connection", expect.any(Object));
  });

  it("lists images and ssh keys and creates instances with idempotency", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        items: [{
          id: "img-1", alias: "ubuntu", source: "incus:ubuntu", digest: "sha256:abc",
          architecture: "x86_64", compatibility: "virtual-machine", created_at: "now", updated_at: "now",
        }],
      }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        items: [{
          id: "key-1", label: "laptop", fingerprint: "SHA256:abc",
          public_key: "ssh-ed25519 AAAA", created_at: "now",
        }],
      }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        operation: {
          id: "op-create", type: "create", target_type: "instance", target_id: "box-9",
          status: "pending", stage: "queued", progress: 0, attempts: 0, created_at: "now", updated_at: "now",
        },
        instance: {
          id: "box-9", name: "fresh", kind: "vps", image_id: "ubuntu",
          requested_isolation: "strong", actual_isolation: "virtual_machine",
          desired_state: "running", observed_state: "pending",
          resources: { vcpus: 2, memory_bytes: 8589934592, disk_bytes: 21474836480 },
          protected: false, created_at: "now", updated_at: "now",
        },
      }), { status: 202, headers: { "content-type": "application/json" } }));
    const api = createHttpApi({ fetcher, csrfToken: "csrf" });

    await expect(api.listImages()).resolves.toEqual([{
      id: "img-1", alias: "ubuntu", source: "incus:ubuntu", architecture: "x86_64", compatibility: "virtual-machine",
    }]);
    await expect(api.listSSHKeys()).resolves.toEqual([{
      id: "key-1", label: "laptop", fingerprint: "SHA256:abc", publicKey: "ssh-ed25519 AAAA", createdAt: "now",
    }]);
    await expect(api.createInstance({
      name: "fresh",
      kind: "vps",
      image: "ubuntu",
      requestedIsolation: "strong",
      vcpus: 2,
      memoryBytes: 8589934592,
      diskBytes: 21474836480,
      ownerPublicKey: "ssh-ed25519 AAAA",
      packages: ["pi"],
    })).resolves.toMatchObject({
      operation: { id: "op-create", action: "create", target: "box-9", status: "pending" },
      instance: { id: "box-9", name: "fresh", kind: "vps" },
    });
    expect(fetcher).toHaveBeenLastCalledWith("/v1/instances", expect.objectContaining({
      method: "POST",
      headers: expect.objectContaining({
        "Idempotency-Key": expect.any(String),
        "X-CSRF-Token": "csrf",
      }),
      body: JSON.stringify({
        name: "fresh",
        kind: "vps",
        image: "ubuntu",
        requested_isolation: "strong",
        resources: {
          vcpus: 2,
          memory_bytes: 8589934592,
          disk_bytes: 21474836480,
        },
        owner_public_key: "ssh-ed25519 AAAA",
        packages: ["pi"],
      }),
    }));
  });

  it("loads instance detail and posts lifecycle actions with an idempotency key", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        id: "box-1", name: "demo", kind: "vps", image_id: "img", requested_isolation: "strong",
        actual_isolation: "virtual_machine", desired_state: "running", observed_state: "running",
        resources: { vcpus: 2, memory_bytes: 4294967296, disk_bytes: 21474836480 },
        protected: false, created_at: "now", updated_at: "now",
      }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        id: "op-9", type: "instance.stop", target_type: "instance", target_id: "box-1",
        status: "pending", stage: "queued", progress: 0, attempts: 0, created_at: "now", updated_at: "now",
      }), { status: 202, headers: { "content-type": "application/json" } }));
    const api = createHttpApi({ fetcher, csrfToken: "csrf" });

    await expect(api.getInstance("box-1")).resolves.toMatchObject({
      id: "box-1", name: "demo", actualIsolation: "virtual_machine", vcpus: 2, memoryBytes: 4294967296,
    });
    await expect(api.mutateInstance("box-1", "stop")).resolves.toMatchObject({
      id: "op-9", action: "instance.stop", target: "box-1", status: "pending",
    });
    expect(fetcher).toHaveBeenLastCalledWith("/v1/instances/box-1/actions/stop", expect.objectContaining({
      method: "POST",
      headers: expect.objectContaining({
        "Idempotency-Key": expect.any(String),
        "X-CSRF-Token": "csrf",
      }),
    }));
  });
});

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

  emit(type: string, data: unknown): void {
    const event = { data: JSON.stringify(data) } as MessageEvent<string>;
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }
}
