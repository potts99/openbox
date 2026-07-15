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

    await api.login({ password: "secret" });

    expect(fetcher).toHaveBeenCalledWith("/v1/sessions", expect.objectContaining({
      method: "POST",
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
      expires_at: "2026-07-15T18:00:00Z",
      csrf_token: "rotated-token",
    }), { status: 200, headers: { "content-type": "application/json" } }));
    const api = createHttpApi({ fetcher: loggedInFetch });
    await expect(api.getSession()).resolves.toEqual({
      authenticated: true,
      owner: { displayName: "Owner" },
      csrfToken: "rotated-token",
    });
    expect(api.getCsrfToken()).toBe("rotated-token");
  });

  it("accepts the v1 items envelopes", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ items: [{
        id: "box-1", name: "dev", kind: "devbox", image_id: "ubuntu", requested_isolation: "best_available",
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

    await expect(api.listInstances()).resolves.toEqual([{ id: "box-1", name: "dev", kind: "devbox", status: "running" }]);
    await expect(api.listOperations()).resolves.toEqual([{ id: "op-1", action: "create", target: "box-1", status: "running", updatedAt: "now" }]);
  });

  it("loads instance detail and posts lifecycle actions with an idempotency key", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        id: "box-1", name: "demo", kind: "vps", image_id: "img", requested_isolation: "best_available",
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
