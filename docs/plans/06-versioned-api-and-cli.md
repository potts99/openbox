---
title: "Slice 06 — Versioned HTTPS API and native CLI"
status: planned
milestone: "M1 Core instance engine"
depends_on: ["05-durable-operations-and-reconciliation"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 06 — Versioned HTTPS API and native CLI

## Goal

Expose the headless instance engine through one versioned contract used by automation and the native CLI.

## Dependencies

- [05-durable-operations-and-reconciliation](05-durable-operations-and-reconciliation.md)

## Non-goals

- No browser dashboard.
- No SSH command server.
- No generated public SDKs in v0.1.

## Proposed files

- `api/openapi.yaml`
- `internal/httpapi/`
- `internal/httpapi/generated/`
- `cmd/openbox/`
- `internal/client/`
- `docs/api/`

## Test-first implementation tasks

1. [ ] Write API conformance tests from the OpenAPI document before handlers.
2. [ ] Define `/v1` resources for capabilities, instances, operations, images, and health with stable error envelopes.
3. [ ] Require idempotency keys for create and other retry-sensitive mutations.
4. [ ] Stream operation progress through SSE and support cancellation only at declared safe stages.
5. [ ] Generate request/response types but keep domain types independent from generated transport types.
6. [ ] Implement CLI commands for doctor, new, ls, inspect, start, stop, restart, rm, and operation watch.
7. [ ] Provide JSON output with compatibility guarantees and concise human output with actionable errors.
8. [ ] Add client timeouts, cancellation, retry only for safe requests, and server-version negotiation.

## Verification

- [ ] OpenAPI schema validation and golden response tests.
- [ ] Handler tests using fake application services.
- [ ] CLI golden tests for human and JSON output.
- [ ] Network interruption and idempotent retry tests.

## Acceptance gate

- [ ] The CLI performs no direct database or Incus access.
- [ ] All long mutations return an operation and can be watched after reconnect.
- [ ] Unknown fields are tolerated by clients; unknown enum values fail safely with context.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
