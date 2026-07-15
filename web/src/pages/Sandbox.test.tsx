// SPDX-License-Identifier: AGPL-3.0-only

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SandboxStatus } from "./Sandbox";

describe("SandboxStatus", () => {
  it("shows countdown, egress, and cleanup failure", () => {
    const now = new Date("2026-07-15T12:00:00Z");
    render(
      <SandboxStatus
        expiresAt="2026-07-15T13:30:00Z"
        egressPolicy="default"
        errorCode="runtime_missing"
        errorStage="deleting_runtime"
        now={now}
      />,
    );
    expect(screen.getByText("default")).toBeInTheDocument();
    expect(screen.getByText("1h30m")).toBeInTheDocument();
    expect(screen.getByText("runtime_missing at deleting_runtime")).toBeInTheDocument();
  });
});
