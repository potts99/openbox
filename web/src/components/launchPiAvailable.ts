// SPDX-License-Identifier: AGPL-3.0-only

/** Whether the dashboard may show Launch Pi for this instance kind. */
export function launchPiAvailable(kind: string, includesPi = kind === "devbox" || kind === "sandbox"): boolean {
  if (!includesPi) return false;
  return kind === "devbox" || kind === "sandbox";
}
