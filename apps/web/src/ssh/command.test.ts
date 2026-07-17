// SPDX-License-Identifier: AGPL-3.0-only

import { describe, expect, it } from "vitest";
import { formatSSHCommand } from "./command";

describe("formatSSHCommand", () => {
  it("formats gateway ssh command", () => {
    expect(formatSSHCommand("demo", "app.example.com", 2222)).toBe(
      "ssh demo@app.example.com -p 2222",
    );
  });
});
