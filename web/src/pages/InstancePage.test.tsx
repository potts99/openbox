// SPDX-License-Identifier: AGPL-3.0-only

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { InstanceDetail, OpenBoxApi } from "../api/client";
import { InstancePage } from "./InstancePage";

const detail: InstanceDetail = {
  id: "box-1",
  name: "demo",
  kind: "vps",
  imageId: "22626b4b3561824d7ed0c109f818161bd5e479b839197ecf0f2602a12b8f8a05",
  requestedIsolation: "strong",
  actualIsolation: "virtual_machine",
  desiredState: "running",
  observedState: "running",
  vcpus: 2,
  memoryBytes: 4 * 1024 ** 3,
  diskBytes: 20 * 1024 ** 3,
  protected: false,
  createdAt: "2026-07-15T18:37:23.344793254Z",
  updatedAt: "2026-07-15T18:45:20.000000000Z",
  networkPolicy: {
    egressMode: "standard",
    acls: ["openbox-default-deny", "openbox-egress-standard"],
    resolutionState: "idle",
    deniedFlows: 0,
  },
  software: [],
};

function createApi(overrides: Partial<OpenBoxApi> = {}): OpenBoxApi {
  return {
    getBootstrapStatus: vi.fn(),
    getSession: vi.fn(),
    getCsrfToken: vi.fn().mockReturnValue("csrf"),
    getCapabilities: vi.fn(),
    getConnection: vi.fn().mockResolvedValue({ ssh: { host: "app.example.com", port: 2222 } }),
    listImages: vi.fn().mockResolvedValue([]),
    listSSHKeys: vi.fn().mockResolvedValue([]),
    listInstances: vi.fn(),
    getInstance: vi.fn().mockResolvedValue(detail),
    createInstance: vi.fn(),
    extendInstance: vi.fn(),
    listSoftwareCatalog: vi.fn().mockResolvedValue([
      { id: "pi", name: "Pi coding agent", description: "Installs Pi CLI and tmux" },
    ]),
    installSoftware: vi.fn().mockResolvedValue({
      packageId: "pi", status: "installed", version: "0.80.7", updatedAt: "now",
    }),
    mutateInstance: vi.fn().mockResolvedValue({
      id: "op-1", action: "instance.stop", status: "pending", targetType: "instance",
      target: "box-1", stage: "queued", progress: 0, attempts: 0, createdAt: "now", updatedAt: "now",
    }),
    listOperations: vi.fn().mockResolvedValue([
      {
        id: "op-create",
        action: "instance.create",
        status: "succeeded",
        targetType: "instance",
        target: "box-1",
        stage: "complete",
        progress: 100,
        attempts: 1,
        createdAt: "2026-07-15T18:37:23.344793254Z",
        updatedAt: "2026-07-15T18:38:00.000000000Z",
      },
      {
        id: "op-other",
        action: "instance.start",
        status: "succeeded",
        targetType: "instance",
        target: "box-2",
        stage: "complete",
        progress: 100,
        attempts: 1,
        createdAt: "2026-07-15T18:39:00.000000000Z",
        updatedAt: "2026-07-15T18:39:30.000000000Z",
      },
    ]),
    getOperation: vi.fn(),
    subscribeOperationEvents: vi.fn().mockReturnValue({ close: vi.fn() }),
    listPiProfiles: vi.fn().mockResolvedValue([]),
    getPiProfileHistory: vi.fn().mockResolvedValue([]),
    rollbackPiProfile: vi.fn(),
    applyPiProfile: vi.fn(),
    listEgressProfiles: vi.fn().mockResolvedValue([]),
    createEgressProfile: vi.fn(),
    updateEgressProfile: vi.fn(),
    deleteEgressProfile: vi.fn(),
    attachEgressProfile: vi.fn(),
    setup: vi.fn(),
    login: vi.fn(),
    logout: vi.fn(),
    ...overrides,
  };
}

describe("InstancePage", () => {
  it("loads instance detail and opens the terminal", async () => {
    const user = userEvent.setup();
    const onOpenTerminal = vi.fn();
    const api = createApi();
    render(<InstancePage api={api} instanceId="box-1" onBack={() => undefined} onOpenTerminal={onOpenTerminal} />);

    expect(await screen.findByRole("heading", { level: 1, name: "demo" })).toBeInTheDocument();
    expect(screen.getByText("virtual_machine")).toBeInTheDocument();
    expect(screen.getByText("4 GiB")).toBeInTheDocument();
    expect(screen.getByText("standard")).toBeInTheDocument();
    const detailSection = document.getElementById("instance-detail-heading")?.closest("section");
    expect(detailSection?.querySelector(".state-pill")?.textContent).toBe("running");
    const detailHeader = detailSection?.querySelector(".ledger-header");
    expect(detailHeader?.querySelector(".detail-header-meta")).toHaveTextContent("Created");
    expect(detailHeader?.querySelector(".detail-header-meta")).toHaveTextContent("Updated");
    expect(detailSection?.querySelector(".detail-grid")?.textContent).not.toContain("Created");
    expect(detailSection?.querySelector(".detail-grid")?.textContent).not.toContain("Updated");

    expect(screen.getByText("ssh demo@app.example.com -p 2222")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Terminal" }));
    expect(onOpenTerminal).toHaveBeenCalledWith({ id: "box-1", name: "demo", kind: "vps" });
  });

  it("hides terminal for sandboxes and still shows SSH connect", async () => {
    const onOpenTerminal = vi.fn();
    const api = createApi({
      getInstance: vi.fn().mockResolvedValue({ ...detail, kind: "sandbox", name: "lab" }),
    });
    render(<InstancePage api={api} instanceId="box-1" onBack={() => undefined} onOpenTerminal={onOpenTerminal} />);

    expect(await screen.findByRole("heading", { level: 1, name: "lab" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Terminal" })).toBeNull();
    expect(screen.getByText("ssh lab@app.example.com -p 2222")).toBeInTheDocument();
  });

  it("submits stop and refreshes detail", async () => {
    const user = userEvent.setup();
    const mutateInstance = vi.fn().mockResolvedValue({
      id: "op-1", action: "instance.stop", status: "pending", targetType: "instance",
      target: "box-1", stage: "queued", progress: 0, attempts: 0, createdAt: "now", updatedAt: "now",
    });
    const getInstance = vi.fn()
      .mockResolvedValueOnce(detail)
      .mockResolvedValueOnce({ ...detail, observedState: "stopping", desiredState: "stopped" });
    const api = createApi({ mutateInstance, getInstance });
    render(<InstancePage api={api} instanceId="box-1" onBack={() => undefined} onOpenTerminal={() => undefined} />);

    await screen.findByRole("heading", { level: 1, name: "demo" });
    await user.click(screen.getByRole("button", { name: "Stop" }));

    await waitFor(() => expect(mutateInstance).toHaveBeenCalledWith("box-1", "stop"));
    expect(await screen.findByText("stopping")).toBeInTheDocument();
  });

  it("returns to the inventory from the back control", async () => {
    const user = userEvent.setup();
    const onBack = vi.fn();
    render(<InstancePage api={createApi()} instanceId="box-1" onBack={onBack} onOpenTerminal={() => undefined} />);
    await screen.findByRole("heading", { level: 1, name: "demo" });
    await user.click(screen.getByRole("button", { name: "← Instances" }));
    expect(onBack).toHaveBeenCalled();
  });

  it("shows build and deploy logs for instance-scoped operations", async () => {
    const subscribeOperationEvents = vi.fn().mockReturnValue({ close: vi.fn() });
    const api = createApi({ subscribeOperationEvents });
    render(<InstancePage api={api} instanceId="box-1" onBack={() => undefined} onOpenTerminal={() => undefined} />);

    expect(await screen.findByRole("heading", { name: "Build & deploy logs" })).toBeInTheDocument();
    const logsSection = screen.getByRole("heading", { name: "Build & deploy logs" }).closest("section");
    expect(logsSection).not.toBeNull();
    expect(await within(logsSection!).findByRole("button", { name: /Create instance/i })).toHaveAttribute("aria-current", "true");
    expect(within(logsSection!).queryByRole("button", { name: /Start/i })).toBeNull();
    expect(subscribeOperationEvents).toHaveBeenCalledWith(
      "op-create",
      expect.objectContaining({ onEvent: expect.any(Function) }),
      expect.any(Object),
    );
  });

  it("does not show Launch Pi and shows Software Install for VPS", async () => {
    const user = userEvent.setup();
    const installSoftware = vi.fn().mockResolvedValue({
      packageId: "pi", status: "installed", version: "0.80.7", updatedAt: "now",
    });
    const getInstance = vi.fn()
      .mockResolvedValueOnce(detail)
      .mockResolvedValueOnce({
        ...detail,
        software: [{ packageId: "pi", status: "installed", version: "0.80.7", updatedAt: "now" }],
      });
    const api = createApi({ installSoftware, getInstance });
    render(<InstancePage api={api} instanceId="box-1" onBack={() => undefined} onOpenTerminal={() => undefined} />);
    await screen.findByRole("heading", { name: "demo" });
    expect(screen.queryByRole("button", { name: "Launch Pi" })).toBeNull();
    expect(screen.getByRole("heading", { name: "Software" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Install" }));
    await waitFor(() => expect(installSoftware).toHaveBeenCalledWith("box-1", "pi"));
    expect(await screen.findByRole("button", { name: "Installed" })).toBeDisabled();
  });
});
