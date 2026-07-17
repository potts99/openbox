// SPDX-License-Identifier: AGPL-3.0-only

import { cp, mkdir, readdir, readFile, rm } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const source = resolve(webRoot, "dist");
const target = resolve(webRoot, "../../internal/assets/static");

const entries = await readdir(resolve(source, "assets"));
if (entries.length === 0 || entries.some((name) => !/-[A-Za-z0-9_-]{8,}\.[^.]+$/.test(name))) {
  throw new Error("Vite output must use content-hashed asset names");
}
const index = await readFile(resolve(source, "index.html"), "utf8");
if (!index.includes("<noscript>")) {
  throw new Error("dashboard index must include a no-JavaScript failure message");
}

await rm(target, { force: true, recursive: true });
await mkdir(target, { recursive: true });
await cp(source, target, { recursive: true });
