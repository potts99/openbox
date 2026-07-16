// SPDX-License-Identifier: AGPL-3.0-only

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { OpenBoxApi, OperationEvent, OperationStreamStatus } from "../api/client";
import { InstanceOperationLogs } from "./InstanceOperationLogs";

const operations = [
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
    id: "op-start",
    action: "instance.start",
    status: "running",
    targetType: "instance",
    target: "box-1",
    stage: "runtime",
    progress: 40,
    attempts: 1,
    createdAt: "2026-07-15T18:39:00.000000000Z",
    updatedAt: "2026-07-15T18:39:10.000000000Z",
  },
];

function createApi(overrides: Partial<OpenBoxApi> = {}): OpenBoxApi {
  const close = vi.fn();
  return {
    getBootstrapStatus: vi.fn(),
    getSession: vi.fn(),
    getCsrfToken: vi.fn().mockReturnValue("csrf"),
    getCapabilities: vi.fn(),
    listImages: vi.fn().mockResolvedValue([]),
    listSSHKeys: vi.fn().mockResolvedValue([]),
    listInstances: vi.fn(),
    getInstance: vi.fn(),
    createInstance: vi.fn(),
    listSoftwareCatalog: vi.fn(),
    installSoftware: vi.fn(),
    mutateInstance: vi.fn(),
    listOperations: vi.fn().mockResolvedValue(operations),
    getOperation: vi.fn(),
    subscribeOperationEvents: vi.fn().mockReturnValue({ close }),
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

describe("InstanceOperationLogs", () => {
  it("filters operations to the current instance and defaults to the active one", async () => {
    const subscribeOperationEvents = vi.fn().mockReturnValue({ close: vi.fn() });
    render(<InstanceOperationLogs api={createApi({ subscribeOperationEvents })} instanceId="box-1" />);

    expect(await screen.findByRole("button", { name: /Start/i })).toHaveAttribute("aria-current", "true");
    expect(screen.getByRole("button", { name: /Create instance/i })).not.toHaveAttribute("aria-current");
    expect(subscribeOperationEvents).toHaveBeenCalledWith(
      "op-start",
      expect.objectContaining({ onEvent: expect.any(Function) }),
      expect.any(Object),
    );
  });

  it("renders replayed events and closes the stream on cleanup", async () => {
    const close = vi.fn();
    let onEvent: ((event: OperationEvent) => void) | undefined;
    const subscribeOperationEvents = vi.fn((_operationId, handlers) => {
      onEvent = handlers.onEvent;
      return { close };
    });
    const { unmount } = render(
      <InstanceOperationLogs api={createApi({ subscribeOperationEvents })} instanceId="box-1" />,
    );

    await screen.findByRole("button", { name: /Start/i });
    onEvent?.({
      sequence: 1,
      operationId: "op-start",
      stage: "runtime",
      status: "running",
      progress: 40,
      createdAt: "2026-07-15T18:39:10.000000000Z",
    });
    onEvent?.({
      sequence: 2,
      operationId: "op-start",
      stage: "complete",
      status: "succeeded",
      progress: 100,
      createdAt: "2026-07-15T18:39:20.000000000Z",
    });

    expect(await screen.findByText("runtime")).toBeInTheDocument();
    expect(screen.getByText("complete")).toBeInTheDocument();
    unmount();
    expect(close).toHaveBeenCalled();
  });

  it("shows stream interruption errors and switches subscriptions when selected", async () => {
    const user = userEvent.setup();
    const close = vi.fn();
    const handlers: Array<{
      onStatus?: (status: OperationStreamStatus, detail?: string) => void;
      onEvent: (event: OperationEvent) => void;
      onError?: (detail?: string) => void;
    }> = [];
    const subscribeOperationEvents = vi.fn((_operationId, nextHandlers) => {
      handlers.push(nextHandlers);
      return { close };
    });
    render(<InstanceOperationLogs api={createApi({ subscribeOperationEvents })} instanceId="box-1" />);

    await screen.findByRole("button", { name: /Start/i });
    handlers[0]?.onStatus?.("error", "Event stream interrupted");
    expect(await screen.findByText("Event stream interrupted")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /Create instance/i }));
    await waitFor(() => expect(subscribeOperationEvents).toHaveBeenCalledTimes(2));
    expect(close).toHaveBeenCalled();
  });
});
