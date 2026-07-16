// SPDX-License-Identifier: AGPL-3.0-only

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { OpenBoxApi } from "../api/client";
import { CreateInstancePage } from "./CreateInstancePage";

function createApi(overrides: Partial<OpenBoxApi> = {}): OpenBoxApi {
  return {
    getBootstrapStatus: vi.fn(),
    getSession: vi.fn(),
    getCsrfToken: vi.fn().mockReturnValue("csrf"),
    getCapabilities: vi.fn(),
    listImages: vi.fn().mockResolvedValue([
      {
        id: "22626b4b3561824d7ed0c109f818161bd5e479b839197ecf0f2602a12b8f8a05",
        alias: "22626b4b3561824d7ed0c109f818161bd5e479b839197ecf0f2602a12b8f8a05",
        source: "incus:ubuntu",
        architecture: "x86_64",
        compatibility: "virtual-machine",
      },
      {
        id: "acd70a7759ea45c60f7e740b39c818883070c0b0de1f7ffdac6521ffc8f74f1c",
        alias: "acd70a7759ea45c60f7e740b39c818883070c0b0de1f7ffdac6521ffc8f74f1c",
        source: "incus:openbox:sandbox/ubuntu/24.04",
        architecture: "x86_64",
        compatibility: "container",
      },
    ]),
    listSSHKeys: vi.fn().mockResolvedValue([{
      id: "key-1",
      label: "laptop",
      fingerprint: "SHA256:abc",
      publicKey: "ssh-ed25519 AAAA test",
      createdAt: "now",
    }]),
    listInstances: vi.fn(),
    getInstance: vi.fn(),
    createInstance: vi.fn().mockResolvedValue({
      operation: {
        id: "op-1",
        action: "create",
        status: "pending",
        targetType: "instance",
        target: "box-1",
        stage: "queued",
        progress: 0,
        attempts: 0,
        createdAt: "now",
        updatedAt: "now",
      },
      instance: {
        id: "box-1",
        name: "fresh",
        kind: "vps",
        imageId: "ubuntu",
        requestedIsolation: "best_available",
        actualIsolation: "virtual_machine",
        desiredState: "running",
        observedState: "pending",
        vcpus: 2,
        memoryBytes: 8 * 1024 ** 3,
        diskBytes: 20 * 1024 ** 3,
        protected: false,
        createdAt: "now",
        updatedAt: "now",
        networkPolicy: { egressMode: "standard", acls: [], resolutionState: "idle", deniedFlows: 0 },
        software: [],
      },
    }),
    listSoftwareCatalog: vi.fn().mockResolvedValue([{ id: "pi", name: "Pi", description: "Coding agent" }]),
    installSoftware: vi.fn(),
    mutateInstance: vi.fn(),
    listOperations: vi.fn(),
    getOperation: vi.fn(),
    subscribeOperationEvents: vi.fn().mockReturnValue({ close: vi.fn() }),
    listPiProfiles: vi.fn(),
    getPiProfileHistory: vi.fn(),
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

describe("CreateInstancePage", () => {
  it("submits CLI-aligned defaults and selected packages", async () => {
    const user = userEvent.setup();
    const api = createApi();
    const onCreated = vi.fn();
    render(<CreateInstancePage api={api} onBack={vi.fn()} onCreated={onCreated} />);

    await screen.findByRole("heading", { name: "New instance" });
    await user.type(screen.getByLabelText("Name"), "fresh");
    await user.click(screen.getByRole("checkbox", { name: /Pi/i }));
    await user.click(screen.getByRole("button", { name: "Create instance" }));

    await waitFor(() => {
      expect(api.createInstance).toHaveBeenCalledWith({
        name: "fresh",
        kind: "vps",
        image: "ubuntu",
        requestedIsolation: "best_available",
        vcpus: 2,
        memoryBytes: 8 * 1024 ** 3,
        diskBytes: 20 * 1024 ** 3,
        ownerPublicKey: "ssh-ed25519 AAAA test",
        packages: ["pi"],
      });
    });
    expect(onCreated).toHaveBeenCalled();
  });

  it("resets isolation and resources when switching to sandbox", async () => {
    const user = userEvent.setup();
    const api = createApi();
    render(<CreateInstancePage api={api} onBack={vi.fn()} onCreated={vi.fn()} />);

    await screen.findByRole("heading", { name: "New instance" });
    await user.click(screen.getByRole("button", { name: "Sandbox" }));

    expect(screen.getByRole("button", { name: "Sandbox" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByLabelText("Isolation")).toHaveValue("standard");
    expect(screen.getByLabelText("Memory (GiB)")).toHaveValue(2);
    expect(screen.getByLabelText("Disk (GiB)")).toHaveValue(10);
    expect(screen.getByLabelText("Image")).toHaveValue("openbox:sandbox/ubuntu/24.04");
    expect(screen.getByRole("option", { name: "Sandbox · Ubuntu 24.04 (container)" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Ubuntu (VM)" })).toBeInTheDocument();
  });
});
