---
title: "Slice 00 — Repository foundation"
status: planned
milestone: "M1 Core instance engine"
depends_on: []
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 00 — Repository foundation

## Goal

Create a boring, reproducible project skeleton that every later slice can build and test without introducing product behavior.

## Dependencies

- None. This is the first executable slice.

## Non-goals

- No Incus calls.
- No database schema.
- No UI beyond a buildable placeholder.

## Proposed files

- `go.mod`
- `go.sum`
- `cmd/openboxd/main.go`
- `cmd/openbox/main.go`
- `internal/version/version.go`
- `web/package.json`
- `web/vite.config.ts`
- `web/src/main.tsx`
- `pnpm-workspace.yaml`
- `Makefile`
- `.github/workflows/ci.yml`
- `LICENSE`
- `README.md`
- `docs/adr/`

## Test-first implementation tasks

1. [ ] Write a smoke test that executes both Go entry points with `--version` and expects the same semantic version.
2. [ ] Initialize the Go module with `cmd/openboxd` and `cmd/openbox`; keep both main packages as dependency-wiring only.
3. [ ] Initialize a pnpm workspace with React, TypeScript, Vite, unit-test tooling, linting, and a minimal accessible application shell.
4. [ ] Add Make targets for format, lint, unit tests, frontend tests, build, and the aggregate `check` target.
5. [ ] Add CI jobs for Go, frontend, license headers, and generated-file drift; cache dependencies but never generated outputs.
6. [ ] Add AGPLv3 licensing, security-reporting instructions, contribution basics, and an explicit pre-v0.1 compatibility statement.
7. [ ] Record ADRs for repository layout, Go/TypeScript boundary, OpenAPI ownership, SQLite choice, and dependency-update policy.

## Verification

- [ ] `go test ./...`
- [ ] `pnpm --dir web test`
- [ ] `make check`
- [ ] Build both Go binaries and the frontend in a clean CI checkout.

## Acceptance gate

- [ ] A new contributor can run one documented command and reproduce CI locally.
- [ ] No product package imports a main package.
- [ ] The frontend build can later be embedded without changing its output contract.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
