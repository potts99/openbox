// SPDX-License-Identifier: AGPL-3.0-only

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SSHConnect } from "./SSHConnect";

describe("SSHConnect", () => {
  it("shows copyable ssh command when endpoint configured", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });

    render(
      <SSHConnect
        instanceName="demo"
        connection={{ ssh: { host: "app.example.com", port: 2222 } }}
      />,
    );

    expect(screen.getByText("ssh demo@app.example.com -p 2222")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /copy/i }));
    expect(writeText).toHaveBeenCalledWith("ssh demo@app.example.com -p 2222");
  });

  it("explains when ssh is not configured", () => {
    render(<SSHConnect instanceName="demo" connection={{ ssh: null }} />);
    expect(screen.getByText(/not configured/i)).toBeInTheDocument();
  });
});
