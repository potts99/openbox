// SPDX-License-Identifier: AGPL-3.0-only

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Checkpoints } from "./Checkpoints";

describe("Checkpoints", () => {
  it("shows warnings and restore action", async () => {
    const user = userEvent.setup();
    const onRestore = vi.fn();
    vi.spyOn(window, "prompt").mockReturnValue("worker-a");
    render(
      <Checkpoints
        snapshots={[{ id: "snap-1", instanceId: "inst-1", name: "ready", ready: true, createdAt: "2026-07-16T12:00:00Z" }]}
        warnings={["source has installed software that may retain secrets; cloned guest files may include them"]}
        storageNote="Storage efficiency: not_supported. OpenBox does not claim copy-on-write."
        onCreate={() => {}}
        onRestore={onRestore}
        onDelete={() => {}}
        onClone={() => {}}
      />,
    );
    expect(screen.getByText(/does not claim copy-on-write/i)).toBeInTheDocument();
    expect(screen.getByText(/may retain secrets/i)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /restore as new/i }));
    expect(onRestore).toHaveBeenCalledWith("snap-1", "worker-a");
  });
});
