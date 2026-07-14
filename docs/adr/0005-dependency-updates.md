# ADR 0005: Dependency update policy

- Status: Accepted
- Date: 2026-07-14

## Context

OpenBox spans Go, npm, and GitHub Actions dependencies. Unreviewed automation or long-lived versions both create supply-chain risk.

## Decision

Dependabot proposes weekly, grouped minor and patch updates for Go modules, pnpm, and GitHub Actions. Major updates remain separate. Every update must pass `make check`; lockfiles and action references are reviewed like source code.

## Consequences

Updates arrive predictably without being merged automatically. Security updates may be raised and reviewed outside the weekly cadence.
