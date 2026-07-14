// SPDX-License-Identifier: AGPL-3.0-only

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { App } from "./App";

describe("App", () => {
  it("renders an accessible application heading", () => {
    render(<App />);

    expect(
      screen.getByRole("heading", { level: 1, name: "OpenBox" }),
    ).toBeInTheDocument();
  });
});
