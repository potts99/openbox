---
title: "Slice 13 — Sandbox exec API and expiry"
status: planned
milestone: "M4 Sandbox and containment"
depends_on: ["05-durable-operations-and-reconciliation", "06-versioned-api-and-cli"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 13 — Sandbox exec API and expiry

## Goal

Turn the instance engine into a reliable disposable execution environment for agents and automation.

## Dependencies

- [05-durable-operations-and-reconciliation](05-durable-operations-and-reconciliation.md)
- [06-versioned-api-and-cli](06-versioned-api-and-cli.md)

## Non-goals

- No multi-tenant scheduler.
- No billing meter.
- No claim that container sandboxes are hardware isolated.

## Proposed files

- `internal/sandbox/`
- `internal/execstream/`
- `internal/httpapi/sandbox_handlers.go`
- `cmd/openbox/sandbox.go`
- `web/src/pages/Sandbox.tsx`

## Test-first implementation tasks

1. [ ] Write policy tests for required default expiry, maximum configurable TTL, explicit extension, and VPS/Devbox no-expiry behavior.
2. [ ] Create Sandbox instances using kind-specific image, lifecycle, resource, and isolation defaults.
3. [ ] Implement argv-based exec with working directory, environment allowlist, stdin, stdout/stderr framing, exit status, timeout, and cancellation.
4. [ ] Stream execution over the API without buffering unbounded output in memory or SQLite.
5. [ ] Use a durable expiry scheduler driven by stored UTC timestamps and a fakeable clock.
6. [ ] At expiry set desired state deleted, retry cleanup, and keep observed state truthful until Incus confirms removal.
7. [ ] Support atomic TTL extension and reject extension after irreversible deletion begins.
8. [ ] Expose countdown, isolation, egress policy, operation progress, and cleanup failure in CLI/dashboard.
9. [ ] Add per-instance execution concurrency and output-rate limits.

## Verification

- [ ] Exec framing, cancellation, timeout, and backpressure tests.
- [ ] Fake-clock expiry and extension race tests.
- [ ] Daemon restart during execution and deletion tests.
- [ ] Container and VM sandbox end-to-end tests.

## Acceptance gate

- [ ] An API client can create, execute, stream, cancel, extend, and destroy without SSH.
- [ ] Expired runtime resources cannot remain hidden behind a deleted UI state.
- [ ] `strong` requests fail clearly when KVM is unavailable.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
