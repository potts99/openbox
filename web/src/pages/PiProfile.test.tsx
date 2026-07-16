// SPDX-License-Identifier: AGPL-3.0-only

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { OpenBoxApi } from "../api/client";
import { PiProfilePage } from "./PiProfile";

function createApi(overrides: Partial<OpenBoxApi> = {}): OpenBoxApi {
  return {
    getBootstrapStatus: vi.fn(),
    getSession: vi.fn(),
    getCsrfToken: vi.fn().mockReturnValue("csrf"),
    getCapabilities: vi.fn(),
    listImages: vi.fn().mockResolvedValue([]),
    listSSHKeys: vi.fn().mockResolvedValue([]),
    listInstances: vi.fn().mockResolvedValue([
      { id: "box-1", name: "dev", kind: "vps", status: "running" },
      { id: "box-2", name: "lab", kind: "sandbox", status: "running" },
    ]),
    getInstance: vi.fn(),
    createInstance: vi.fn(),
    listSoftwareCatalog: vi.fn().mockResolvedValue([]),
    installSoftware: vi.fn(),
    mutateInstance: vi.fn(),
    listOperations: vi.fn().mockResolvedValue([]),
    getOperation: vi.fn(),
    subscribeOperationEvents: vi.fn().mockReturnValue({ close: vi.fn() }),
    listPiProfiles: vi.fn().mockResolvedValue([
      {
        id: "prof-1",
        name: "default",
        version: 2,
        settingsJson: JSON.stringify({ theme: "dark", defaultProvider: "anthropic" }),
        updatedAt: "2026-07-15T12:00:00Z",
      },
    ]),
    getPiProfileHistory: vi.fn().mockResolvedValue([
      { version: 1, settingsJson: JSON.stringify({ theme: "dark" }), createdAt: "2026-07-15T11:00:00Z" },
      { version: 2, settingsJson: JSON.stringify({ theme: "light" }), createdAt: "2026-07-15T12:00:00Z" },
    ]),
    rollbackPiProfile: vi.fn().mockResolvedValue({
      id: "prof-1",
      name: "default",
      version: 3,
      settingsJson: JSON.stringify({ theme: "dark" }),
      updatedAt: "2026-07-15T13:00:00Z",
    }),
    applyPiProfile: vi.fn().mockResolvedValue(undefined),
    setup: vi.fn(),
    login: vi.fn(),
    logout: vi.fn(),
    ...overrides,
  };
}

describe("PiProfilePage", () => {
  it("previews settings, rolls back, and applies to selected instances", async () => {
    const user = userEvent.setup();
    const api = createApi();
    render(<PiProfilePage api={api} onBack={() => undefined} />);

    expect(await screen.findByRole("heading", { name: "Pi profile" })).toBeInTheDocument();
    expect(screen.getByText(/"theme": "dark"/)).toBeInTheDocument();
    expect(await screen.findByText("Version history")).toBeInTheDocument();

    const rollbackButtons = await screen.findAllByRole("button", { name: "Rollback" });
    const rollback = rollbackButtons.find((btn) => !(btn as HTMLButtonElement).disabled);
    expect(rollback).toBeTruthy();
    await user.click(rollback!);
    await waitFor(() => expect(api.rollbackPiProfile).toHaveBeenCalledWith("prof-1", 1));

    await user.click(screen.getByLabelText(/dev/));
    await user.click(screen.getByLabelText(/lab/));
    await user.click(screen.getByRole("button", { name: "Apply profile" }));
    await waitFor(() => expect(api.applyPiProfile).toHaveBeenCalledWith("prof-1", ["box-1", "box-2"]));
  });
});
