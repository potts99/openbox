// SPDX-License-Identifier: AGPL-3.0-only

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { LandingPage } from "./LandingPage";

describe("LandingPage", () => {
  it("renders marketing copy and sign-in actions", async () => {
    const user = userEvent.setup();
    const onSignIn = vi.fn();
    render(<LandingPage onSignIn={onSignIn} />);

    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(/sandboxes/i);
    expect(screen.getByRole("button", { name: "Open console" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(onSignIn).toHaveBeenCalledOnce();
  });
});
