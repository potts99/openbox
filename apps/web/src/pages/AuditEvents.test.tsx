// SPDX-License-Identifier: AGPL-3.0-only

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { OpenBoxApi } from "../api/client";
import { AuditEventsPage } from "./AuditEvents";

function createApi(overrides: Partial<OpenBoxApi> = {}): OpenBoxApi {
  return {
    getBootstrapStatus: vi.fn(),
    getSession: vi.fn(),
    getCsrfToken: vi.fn(),
    getCapabilities: vi.fn(),
    getConnection: vi.fn(),
    listImages: vi.fn(),
    listSSHKeys: vi.fn(),
    listInstances: vi.fn(),
    getInstance: vi.fn(),
    createInstance: vi.fn(),
    extendInstance: vi.fn(),
    listSnapshots: vi.fn(),
    listArtifacts: vi.fn(),
    uploadArtifact: vi.fn(),
    downloadArtifact: vi.fn(),
    createSnapshot: vi.fn(),
    deleteSnapshot: vi.fn(),
    restoreSnapshot: vi.fn(),
    cloneInstance: vi.fn(),
    listSoftwareCatalog: vi.fn(),
    installSoftware: vi.fn(),
    mutateInstance: vi.fn(),
    listOperations: vi.fn(),
    getOperation: vi.fn(),
    subscribeOperationEvents: vi.fn(),
    listPiProfiles: vi.fn(),
    getPiProfileHistory: vi.fn(),
    rollbackPiProfile: vi.fn(),
    applyPiProfile: vi.fn(),
    listEgressProfiles: vi.fn(),
    createEgressProfile: vi.fn(),
    updateEgressProfile: vi.fn(),
    deleteEgressProfile: vi.fn(),
    attachEgressProfile: vi.fn(),
    listAuditEvents: vi.fn().mockResolvedValue([
      {
        id: "evt-1",
        actor: "openboxd",
        action: "egress.deny",
        outcome: "blocked",
        targetType: "instance",
        targetId: "box-1",
        metadata: { hostname: "evil.example" },
        createdAt: "2026-07-17T09:00:00Z",
      },
      {
        id: "evt-2",
        actor: "openboxd",
        action: "policy.apply",
        outcome: "succeeded",
        targetType: "instance",
        targetId: "box-1",
        metadata: { mode: "restricted" },
        createdAt: "2026-07-17T08:00:00Z",
      },
    ]),
    listTokens: vi.fn().mockResolvedValue([]),
    createToken: vi.fn(),
    revokeToken: vi.fn(),
    setup: vi.fn(),
    login: vi.fn(),
    logout: vi.fn(),
    ...overrides,
  };
}

describe("AuditEventsPage", () => {
  it("renders events as a scannable ledger with metadata", async () => {
    render(<AuditEventsPage api={createApi()} onBack={() => {}} />);
    expect(await screen.findByText("egress.deny")).toBeInTheDocument();
    expect(screen.getByText("evil.example")).toBeInTheDocument();
    expect(screen.getByText("blocked")).toBeInTheDocument();
    expect(document.querySelector(".audit-list")).not.toBeNull();
  });

  it("filters events by outcome", async () => {
    const user = userEvent.setup();
    render(<AuditEventsPage api={createApi()} onBack={() => {}} />);
    expect(await screen.findByText("egress.deny")).toBeInTheDocument();
    expect(screen.getByText("policy.apply")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /Blocked/i }));
    expect(screen.getByText("egress.deny")).toBeInTheDocument();
    expect(screen.queryByText("policy.apply")).not.toBeInTheDocument();
  });
});
