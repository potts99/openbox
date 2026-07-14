# ADR 0001: Repository layout

- Status: Accepted
- Date: 2026-07-14

## Context

OpenBox ships two Go executables and a browser dashboard. Later slices need stable locations without importing executable packages.

## Decision

Go entry points live under `cmd/`, reusable Go packages under `internal/`, the React application under `web/`, and architectural records under `docs/adr/`. Main packages contain dependency wiring only and are never imported.

## Consequences

The repository remains a single Go module and pnpm workspace. Packages may be split further when they gain a clear responsibility, but product logic cannot live in `cmd/`.
