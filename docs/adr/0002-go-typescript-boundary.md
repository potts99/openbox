# ADR 0002: Go and TypeScript boundary

- Status: Accepted
- Date: 2026-07-14

## Context

The control plane needs host integration and a simple deployment artifact, while the dashboard benefits from the React ecosystem.

## Decision

Go owns the control plane, CLI, host services, and HTTP API. TypeScript owns browser presentation and calls only the versioned API. The frontend builds to `web/dist`; a later slice may embed that directory into `openboxd` without changing the output path.

## Consequences

Business rules remain server-side. The dashboard cannot introduce private behavior unavailable to other API clients.
