// SPDX-License-Identifier: AGPL-3.0-only

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { OpenBoxApi } from "../api/client";
import { SettingsPage } from "./Settings";

function createApi(overrides: Partial<OpenBoxApi> = {}): OpenBoxApi {
  return {
    listTokens: vi.fn().mockResolvedValue([]),
    createToken: vi.fn(),
    revokeToken: vi.fn(),
    ...overrides,
  } as OpenBoxApi;
}

const session = {
  authenticated: true as const,
  owner: { displayName: "Operator" },
  userId: "usr_potts",
  username: "potts",
  role: "admin",
  csrfToken: "csrf",
};

describe("SettingsPage", () => {
  it("creates a token, shows the secret once, and lists it", async () => {
    const user = userEvent.setup();
    const createToken = vi.fn().mockResolvedValue({
      id: "tok_1",
      name: "laptop",
      scopes: ["owner"],
      createdAt: "2026-07-17T10:00:00Z",
      secret: "obx_secret-value",
    });
    const listTokens = vi.fn()
      .mockResolvedValueOnce([])
      .mockResolvedValue([{
        id: "tok_1",
        name: "laptop",
        scopes: ["owner"],
        createdAt: "2026-07-17T10:00:00Z",
      }]);
    const api = createApi({ listTokens, createToken });

    render(<SettingsPage api={api} session={session} onBack={() => undefined} />);

    expect(await screen.findByRole("heading", { name: "Settings" })).toBeInTheDocument();
    expect(screen.getByText("potts")).toBeInTheDocument();
    expect(screen.getByText("Admin")).toBeInTheDocument();

    await user.type(screen.getByLabelText("Name"), "laptop");
    await user.click(screen.getByRole("button", { name: "Create token" }));

    await waitFor(() => expect(createToken).toHaveBeenCalledWith({ name: "laptop" }));
    expect(await screen.findByText("Token ready — copy it now")).toBeInTheDocument();
    expect(screen.getByDisplayValue("obx_secret-value")).toBeInTheDocument();
    expect(screen.getAllByText("laptop").length).toBeGreaterThan(0);
  });

  it("revokes a listed token after confirm", async () => {
    const user = userEvent.setup();
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const revokeToken = vi.fn().mockResolvedValue(undefined);
    const listTokens = vi.fn()
      .mockResolvedValueOnce([{
        id: "tok_1",
        name: "ci",
        scopes: ["owner"],
        createdAt: "2026-07-17T10:00:00Z",
      }])
      .mockResolvedValue([]);
    const api = createApi({ listTokens, revokeToken });

    render(<SettingsPage api={api} session={session} onBack={() => undefined} />);

    expect(await screen.findByRole("button", { name: "Revoke" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Revoke" }));

    await waitFor(() => expect(revokeToken).toHaveBeenCalledWith("tok_1"));
    expect(await screen.findByText("No tokens yet. Create one to use the CLI.")).toBeInTheDocument();
  });
});
