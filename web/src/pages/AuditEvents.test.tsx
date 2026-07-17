// SPDX-License-Identifier: AGPL-3.0-only

import { render, screen } from "@testing-library/react";
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
        action: "egress.deny",
        outcome: "blocked",
        targetType: "instance",
        targetId: "box-1",
        metadata: { hostname: "evil.example" },
        createdAt: "2026-07-17T09:00:00Z",
      },
    ]),
    setup: vi.fn(),
    login: vi.fn(),
    logout: vi.fn(),
    ...overrides,
  };
}

describe("AuditEventsPage", () => {
  it("wraps the events table for horizontal scroll on narrow viewports", async () => {
    render(<AuditEventsPage api={createApi()} onBack={() => {}} />);
    expect(await screen.findByRole("columnheader", { name: "When" })).toBeInTheDocument();
    expect(document.querySelector(".table-wrap")).not.toBeNull();
  });
});
