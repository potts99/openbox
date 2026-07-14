# Contributing to OpenBox

OpenBox is pre-v0.1 and its interfaces may change. Before starting substantial work, open an issue describing the problem and proposed scope.

## Local checks

Install the prerequisites listed in the README, then run:

```sh
pnpm install --frozen-lockfile
make check
```

Keep changes focused, add tests for behavior, use the existing Go and TypeScript boundaries, and update an ADR when changing a recorded architectural decision.

All contributions are provided under the repository's AGPL-3.0-only license.
