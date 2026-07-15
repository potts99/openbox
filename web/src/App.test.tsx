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
    getSession: vi.fn().mockResolvedValue({ authenticated: true, owner: { displayName: "Operator" } }),
    getCapabilities: vi.fn().mockResolvedValue({
      architecture: "x86_64",
      containers: true,
      virtualMachines: false,
      vmAvailability: "unavailable",
      vmReason: "/dev/kvm is not available",
    }),
    listInstances: vi.fn().mockResolvedValue([]),
    listOperations: vi.fn().mockResolvedValue([]),
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

    expect(await screen.findByRole("heading", { name: "Claim this OpenBox" })).toBeInTheDocument();
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

    expect(await screen.findByRole("heading", { name: "Claim this OpenBox" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Return to your OpenBox" })).not.toBeInTheDocument();
  });

  it("submits setup without storing secrets in browser storage", async () => {
    const user = userEvent.setup();
    const setup = vi.fn().mockResolvedValue({ authenticated: true, owner: { displayName: "Owner" } });
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
    await user.click(screen.getByRole("button", { name: "Claim OpenBox" }));

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
    await user.click(screen.getByRole("button", { name: "Claim OpenBox" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(message);
    expect(screen.getByRole("heading", { name: "Claim this OpenBox" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Return to your OpenBox" })).not.toBeInTheDocument();
  });

  it("shows login and announces invalid credentials", async () => {
    const user = userEvent.setup();
    const api = createApi({
      getSession: vi.fn().mockResolvedValue({ authenticated: false }),
      login: vi.fn().mockRejectedValue(new Error("Invalid credentials")),
    });

    render(<App api={api} />);
    await user.type(await screen.findByLabelText("Password"), "wrong-password");
    await user.click(screen.getByRole("button", { name: "Unlock console" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Invalid credentials");
  });

  it("renders the operator console and communicates unavailable VM capability", async () => {
    const api = createApi();
    render(<App api={api} />);

    expect(await screen.findByRole("heading", { level: 1, name: "Instances" })).toBeInTheDocument();
    const capability = await screen.findByRole("status", { name: "Runtime capability status" });
    expect(capability).toHaveTextContent("Virtual machines unavailable");
    expect(capability).toHaveTextContent("/dev/kvm is not available");
    expect(screen.getByText("No instances yet")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Show operations" })).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("complementary", { name: "Operations" })).not.toBeInTheDocument();
  });

  it("opens and closes the operations drawer with keyboard-accessible controls", async () => {
    const user = userEvent.setup();
    const api = createApi({
      listOperations: vi.fn().mockResolvedValue([
        { id: "op-1", action: "create", status: "running", target: "workbench", updatedAt: "2026-07-15T09:30:00Z" },
      ]),
    });

    render(<App api={api} />);
    const toggle = await screen.findByRole("button", { name: "Show operations" });
    await user.click(toggle);

    const drawer = screen.getByRole("complementary", { name: "Operations" });
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(within(drawer).getByText("workbench")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("complementary", { name: "Operations" })).not.toBeInTheDocument();
    await waitFor(() => expect(toggle).toHaveFocus());
  });

  it("stays authenticated and announces a safe message when logout fails", async () => {
    const user = userEvent.setup();
    const logout = vi.fn().mockRejectedValue(new Error("session hash leaked from /private/state"));
    const api = createApi({ logout });

    render(<App api={api} />);
    await user.click(await screen.findByRole("button", { name: "Lock" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("OpenBox could not lock this session. Try again.");
    expect(screen.getByRole("heading", { level: 1, name: "Instances" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Return to your OpenBox" })).not.toBeInTheDocument();
    expect(logout).toHaveBeenCalledOnce();
  });

  it("has no automated accessibility violations on auth and console views", async () => {
    const loginApi = createApi({ getSession: vi.fn().mockResolvedValue({ authenticated: false }) });
    const loginView = render(<App api={loginApi} />);
    await screen.findByRole("heading", { name: "Return to your OpenBox" });
    expect((await axe.run(loginView.container)).violations).toEqual([]);
    loginView.unmount();

    const consoleView = render(<App api={createApi()} />);
    await screen.findByRole("heading", { level: 1, name: "Instances" });
    expect((await axe.run(consoleView.container)).violations).toEqual([]);
  });
});
