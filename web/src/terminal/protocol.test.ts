// SPDX-License-Identifier: AGPL-3.0-only

import { describe, expect, it } from "vitest";
import { decodeFrame, encodeFrame } from "./protocol";

describe("terminal protocol", () => {
  it("encodes and decodes open, resize, signal, detach, and reconnect frames", () => {
    expect(JSON.parse(encodeFrame({ type: "open", instanceId: "inst-1", cols: 80, rows: 24 }))).toEqual({
      type: "open",
      instance_id: "inst-1",
      cols: 80,
      rows: 24,
    });
    expect(decodeFrame(encodeFrame({ type: "resize", cols: 120, rows: 40 }))).toEqual({
      type: "resize",
      cols: 120,
      rows: 40,
    });
    expect(decodeFrame(encodeFrame({ type: "signal", signal: "TERM" }))).toEqual({
      type: "signal",
      signal: "TERM",
    });
    expect(decodeFrame(encodeFrame({ type: "detach" }))).toEqual({ type: "detach" });
    expect(decodeFrame(encodeFrame({ type: "reconnect", sessionId: "sess-9" }))).toEqual({
      type: "reconnect",
      sessionId: "sess-9",
    });
  });

  it("base64-encodes input and output payloads", () => {
    const input = encodeFrame({ type: "input", data: new TextEncoder().encode("hi") });
    expect(JSON.parse(input)).toEqual({ type: "input", data: "aGk=" });
    const decoded = decodeFrame(input);
    expect(decoded).toEqual({ type: "input", data: expect.any(Uint8Array) });
    if (decoded.type === "input") {
      expect(new TextDecoder().decode(decoded.data)).toBe("hi");
    }

    const output = encodeFrame({ type: "output", data: new Uint8Array([1, 2, 3]) });
    const roundTrip = decodeFrame(output);
    expect(roundTrip.type).toBe("output");
    if (roundTrip.type === "output") {
      expect(Array.from(roundTrip.data)).toEqual([1, 2, 3]);
    }
  });

  it("decodes exit and error frames from the server", () => {
    expect(decodeFrame(JSON.stringify({ type: "exit", code: 0 }))).toEqual({ type: "exit", code: 0 });
    expect(decodeFrame(JSON.stringify({ type: "error", code: "idle_timeout", message: "idle" }))).toEqual({
      type: "error",
      code: "idle_timeout",
      message: "idle",
    });
  });
});
