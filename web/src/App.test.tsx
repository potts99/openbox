// SPDX-License-Identifier: AGPL-3.0-only

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { createHttpApi } from "./api/client";
import type { OpenBoxApi, Session } from "./api/client";

function createApi(overrides: Partial<OpenBoxApi> = {}): OpenBoxApi {
  return {
    getBootstrapStatus: vi.fn().mockResolvedValue({ required: false }),
    getSession: vi.fn().mockResolvedValue({ authenticated: true, owner: { displayName: "Operator" }, csrfToken: "test-csrf" }),
    getCsrfToken: vi.fn().mockReturnValue("test-csrf"),
    getCapabilities: vi.fn().mockResolvedValue({
      architecture: "x86_64",
      containers: true,
      virtualMachines: false,
      vmAvailability: "unavailable",
      vmReason: "/dev/kvm is not available",
    }),
    getConnection: vi.fn().mockResolvedValue({ ssh: { host: "app.example.com", port: 2222 } }),
    listImages: vi.fn().mockResolvedValue([{
      id: "img-1",
      alias: "ubuntu",
      source: "incus:ubuntu",
      architecture: "x86_64",
      compatibility: "virtual-machine",
    }]),
    listSSHKeys: vi.fn().mockResolvedValue([{
      id: "key-1",
      label: "laptop",
      fingerprint: "SHA256:abc",
      publicKey: "ssh-ed25519 AAAA test",
      createdAt: "now",
    }]),
    listInstances: vi.fn().mockResolvedValue([]),
    getInstance: vi.fn(),
    createInstance: vi.fn(),
    listSoftwareCatalog: vi.fn().mockResolvedValue([{ id: "pi", name: "Pi", description: "Coding agent" }]),
    installSoftware: vi.fn(),
    mutateInstance: vi.fn(),
    listOperations: vi.fn().mockResolvedValue([]),
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

describe("App", () => {
  it("loads independent entry data in parallel and shows setup when required", async () => {
    let resolveBootstrap: (value: { required: boolean }) => void = () => undefined;
    let resolveSession: (value: Session) => void = () => undefined;
    const api = createApi({
      getBootstrapStatus: vi.fn(
        () => new Promise<{ required: boolean }>((resolve) => { resolveBootstrap = resolve; }),
      ),
      getSession: vi.fn(
        () => new Promise<Session>((resolve) => { resolveSession = resolve; }),
      ),
    });

    render(<App api={api} />);

    expect(api.getBootstrapStatus).toHaveBeenCalledOnce();
    expect(api.getSession).toHaveBeenCalledOnce();
    resolveBootstrap({ required: true });
    resolveSession({ authenticated: false });

    expect(await screen.findByRole("heading", { name: "Set up" })).toBeInTheDocument();
    expect(screen.getByLabelText("One-time setup secret")).toHaveAttribute("autocomplete", "one-time-code");
  });

  it("shows setup for a bootstrap status without an expiry field", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ required: true }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: "unauthenticated" } }), {
        status: 401,
        headers: { "content-type": "application/json" },
      }));

    render(<App api={createHttpApi({ fetcher })} />);

    expect(await screen.findByRole("heading", { name: "Set up" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Sign in" })).not.toBeInTheDocument();
  });

  it("submits setup without storing secrets in browser storage", async () => {
    const user = userEvent.setup();
    const setup = vi.fn().mockResolvedValue({ authenticated: true, owner: { displayName: "Owner" }, csrfToken: "setup-csrf" });
    const api = createApi({
      getBootstrapStatus: vi.fn().mockResolvedValue({ required: true }),
      getSession: vi.fn().mockResolvedValue({ authenticated: false }),
      setup,
    });
    const setItem = vi.spyOn(Storage.prototype, "setItem");

    render(<App api={api} />);
    await user.type(await screen.findByLabelText("One-time setup secret"), "setup-secret");
    await user.type(screen.getByLabelText("New password"), "correct horse battery staple");
    await user.type(screen.getByLabelText("Confirm password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Create owner" }));

    await waitFor(() => expect(setup).toHaveBeenCalledWith({
      secret: "setup-secret",
      password: "correct horse battery staple",
    }));
    expect(setItem).not.toHaveBeenCalled();
    setItem.mockRestore();
  });

  it("keeps setup open and announces how to recover from an unavailable secret", async () => {
    const user = userEvent.setup();
    const message = "Setup is unavailable. Restart openboxd to issue a new one-time secret.";
    const api = createApi({
      getBootstrapStatus: vi.fn().mockResolvedValue({ required: true }),
      getSession: vi.fn().mockResolvedValue({ authenticated: false }),
      setup: vi.fn().mockRejectedValue(new Error(message)),
    });

    render(<App api={api} />);
    await user.type(await screen.findByLabelText("One-time setup secret"), "expired-setup-secret");
    await user.type(screen.getByLabelText("New password"), "correct horse battery staple");
    await user.type(screen.getByLabelText("Confirm password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Create owner" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(message);
    expect(screen.getByRole("heading", { name: "Set up" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Sign in" })).not.toBeInTheDocument();
  });

  it("shows login and announces invalid credentials", async () => {
    const user = userEvent.setup();
    const api = createApi({
      getSession: vi.fn().mockResolvedValue({ authenticated: false }),
      login: vi.fn().mockRejectedValue(new Error("Invalid credentials")),
    });

    render(<App api={api} />);
    await user.type(await screen.findByLabelText("Password"), "wrong-password");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Invalid credentials");
  });

  it("renders the operator console and communicates unavailable VM capability", async () => {
    const api = createApi();
    render(<App api={api} />);

    expect(await screen.findByRole("heading", { level: 1, name: "Instances" })).toBeInTheDocument();
    const capability = await screen.findByRole("status", { name: "Runtime capability status" });
    expect(capability).toHaveTextContent("Limited (no KVM)");
    expect(capability).toHaveTextContent("/dev/kvm is not available");
    expect(capability).toHaveTextContent("Default isolation is container");
    expect(screen.getByText("No instances")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create an instance" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Show operations" })).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("complementary", { name: "Operations" })).not.toBeInTheDocument();
  });

  it("creates an instance from the console and opens its detail page", async () => {
    const user = userEvent.setup();
    const createInstance = vi.fn().mockResolvedValue({
      operation: {
        id: "op-1",
        action: "create",
        status: "pending",
        targetType: "instance",
        target: "box-9",
        stage: "queued",
        progress: 0,
        attempts: 0,
        createdAt: "now",
        updatedAt: "now",
      },
      instance: {
        id: "box-9",
        name: "fresh",
        kind: "vps",
        imageId: "ubuntu",
        requestedIsolation: "strong",
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
    });
    const api = createApi({
      createInstance,
      listInstances: vi.fn()
        .mockResolvedValueOnce([])
        .mockResolvedValue([{ id: "box-9", name: "fresh", kind: "vps", status: "pending" }]),
      getInstance: vi.fn().mockResolvedValue({
        id: "box-9",
        name: "fresh",
        kind: "vps",
        imageId: "ubuntu",
        requestedIsolation: "strong",
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
      }),
    });

    render(<App api={api} />);
    await user.click(await screen.findByRole("button", { name: "New" }));
    expect(await screen.findByRole("heading", { name: "New instance" })).toBeInTheDocument();
    await user.type(screen.getByLabelText("Name"), "fresh");
    await user.click(screen.getByRole("button", { name: "Create instance" }));

    expect(await screen.findByRole("heading", { name: "Instance created" })).toBeInTheDocument();
    expect(screen.getByText("ssh fresh@app.example.com -p 2222")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Open instance" }));

    expect(await screen.findByRole("heading", { level: 1, name: "fresh" })).toBeInTheDocument();
    expect(createInstance).toHaveBeenCalled();
  });

  it("renders runtime ready when VMs report supported availability", async () => {
    const api = createApi({
      getCapabilities: vi.fn().mockResolvedValue({
        architecture: "x86_64",
        containers: true,
        virtualMachines: true,
        vmAvailability: "supported",
      }),
    });
    render(<App api={api} />);

    const capability = await screen.findByRole("status", { name: "Runtime capability status" });
    expect(capability).toHaveTextContent("Runtime ready");
    expect(capability).toHaveTextContent("KVM VMs available. Default isolation is strong.");
  });

  it("offers a uniquely named open-terminal action per instance", async () => {
    const api = createApi({
      listInstances: vi.fn().mockResolvedValue([
        { id: "box-1", name: "workbench", kind: "vps", status: "running" },
        { id: "box-2", name: "staging", kind: "vps", status: "stopped" },
      ]),
      getInstance: vi.fn().mockImplementation(async (id: string) => ({
        id,
        name: id === "box-1" ? "workbench" : "staging",
        kind: "vps",
        imageId: "img",
        requestedIsolation: "container",
        actualIsolation: "container",
        desiredState: "running",
        observedState: id === "box-1" ? "running" : "stopped",
        vcpus: 2,
        memoryBytes: 8 * 1024 ** 3,
        diskBytes: 20 * 1024 ** 3,
        protected: false,
        createdAt: "now",
        updatedAt: "now",
        networkPolicy: {
          egressMode: "standard",
          acls: [],
          resolutionState: "idle",
          deniedFlows: 0,
        },
        software: [],
      })),
    });
    const user = userEvent.setup();
    render(<App api={api} />);

    expect(await screen.findByRole("button", { name: "workbench" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "staging" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "workbench" }));
    expect(await screen.findByRole("heading", { level: 1, name: "workbench" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Terminal" })).toBeInTheDocument();
  });

  it("opens and closes the operations drawer with keyboard-accessible controls", async () => {
    const user = userEvent.setup();
    const api = createApi({
      listOperations: vi.fn().mockResolvedValue([
        { id: "op-1", action: "instance.create", status: "running", target: "workbench", updatedAt: "2026-07-15T09:30:00Z" },
      ]),
    });

    render(<App api={api} />);
    const toggle = await screen.findByRole("button", { name: "Show operations" });
    await user.click(toggle);

    const drawer = screen.getByRole("complementary", { name: "Operations" });
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(within(drawer).getByText("workbench")).toBeInTheDocument();
    expect(within(drawer).getByText("instance · create")).toBeInTheDocument();
    expect(within(drawer).getByText("running")).toBeInTheDocument();
    expect(within(drawer).getByText(/1 total/)).toBeInTheDocument();
    expect(within(drawer).getByText(/1 active/)).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("complementary", { name: "Operations" })).not.toBeInTheDocument();
    await waitFor(() => expect(toggle).toHaveFocus());
  });

  it("refreshes operations while the drawer stays open", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const listOperations = vi.fn()
      .mockResolvedValueOnce([
        { id: "op-1", action: "instance.start", status: "running", target: "workbench", updatedAt: "2026-07-15T09:30:00Z" },
      ])
      .mockResolvedValue([
        { id: "op-1", action: "instance.start", status: "succeeded", target: "workbench", updatedAt: "2026-07-15T09:31:00Z" },
      ]);
    const api = createApi({ listOperations });

    render(<App api={api} />);
    await user.click(await screen.findByRole("button", { name: "Show operations" }));

    const drawer = screen.getByRole("complementary", { name: "Operations" });
    expect(within(drawer).getByText("running")).toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(3000);
    await waitFor(() => expect(within(drawer).getByText("succeeded")).toBeInTheDocument());
    expect(listOperations.mock.calls.length).toBeGreaterThan(1);

    vi.useRealTimers();
  });

  it("stays authenticated and announces a safe message when logout fails", async () => {
    const user = userEvent.setup();
    const logout = vi.fn().mockRejectedValue(new Error("session hash leaked from /private/state"));
    const api = createApi({ logout });

    render(<App api={api} />);
    await user.click(await screen.findByRole("button", { name: "Sign out" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Could not sign out. Try again.");
    expect(screen.getByRole("heading", { level: 1, name: "Instances" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Sign in" })).not.toBeInTheDocument();
    expect(logout).toHaveBeenCalledOnce();
  });

  it("has no automated accessibility violations on auth and console views", async () => {
    const loginApi = createApi({ getSession: vi.fn().mockResolvedValue({ authenticated: false }) });
    const loginView = render(<App api={loginApi} />);
    await screen.findByRole("heading", { name: "Sign in" });
    expect((await axe.run(loginView.container)).violations).toEqual([]);
    loginView.unmount();

    const consoleView = render(<App api={createApi()} />);
    await screen.findByRole("heading", { level: 1, name: "Instances" });
    expect((await axe.run(consoleView.container)).violations).toEqual([]);
  });
});
