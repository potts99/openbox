// SPDX-License-Identifier: AGPL-3.0-only

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { LaunchPi, launchPiAvailable } from "./LaunchPi";

describe("launchPiAvailable", () => {
  it("allows Pi-enabled Devboxes and Sandboxes only", () => {
    expect(launchPiAvailable("devbox", true)).toBe(true);
    expect(launchPiAvailable("sandbox", true)).toBe(true);
    expect(launchPiAvailable("sandbox", false)).toBe(false);
    expect(launchPiAvailable("vps", false)).toBe(false);
    expect(launchPiAvailable("vps", true)).toBe(false);
  });
});

describe("LaunchPi", () => {
  it("invokes onLaunch", async () => {
    const user = userEvent.setup();
    const onLaunch = vi.fn();
    render(<LaunchPi onLaunch={onLaunch} />);
    await user.click(screen.getByRole("button", { name: "Launch Pi" }));
    expect(onLaunch).toHaveBeenCalledOnce();
  });
});
