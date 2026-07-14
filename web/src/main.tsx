// SPDX-License-Identifier: AGPL-3.0-only

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";

const root = document.getElementById("root");

if (!root) {
  throw new Error("OpenBox application root is missing");
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
