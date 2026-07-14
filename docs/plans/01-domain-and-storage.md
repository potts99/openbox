---
title: "Slice 01 — Domain model and SQLite storage"
status: planned
milestone: "M1 Core instance engine"
depends_on: ["00-foundation"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 01 — Domain model and SQLite storage

## Goal

Define OpenBox’s stable resource vocabulary, lifecycle invariants, and transactional SQLite repositories before connecting any runtime.

## Dependencies

- [00-foundation](00-foundation.md)

## Non-goals

- No Incus implementation.
- No HTTP endpoints.
- No background workers.

## Proposed files

- `internal/domain/`
- `internal/persistence/sqlite/`
- `internal/persistence/migrations/`
- `internal/testutil/`
- `docs/adr/`

## Test-first implementation tasks

1. [ ] Write table-driven tests for instance-name validation, kind defaults, isolation requests, desired/observed transitions, expiry rules, and protected-base deletion.
2. [ ] Define typed IDs and domain records for Owner, SSHKey, Instance, Image, Snapshot, Route, PiProfile, CredentialProfile metadata, GatewayGrant, EgressProfile, Operation, and AuditEvent.
3. [ ] Define machine-readable error codes and keep user-facing messages outside persistence code.
4. [ ] Create forward-only embedded migrations with owner IDs on every owner-scoped record, even though v0.1 has one owner.
5. [ ] Open SQLite with WAL mode, foreign keys, busy timeout, UTC timestamps, and a single migration lock.
6. [ ] Implement repositories with context cancellation and explicit transactions; do not expose SQL rows outside the package.
7. [ ] Implement operation creation and target mutation in one transaction, including unique idempotency keys.
8. [ ] Implement deletion tombstones that preserve audit and idempotency metadata without retaining guest storage metadata as active.
9. [ ] Add migration-from-empty, reopen, rollback-on-error, uniqueness, concurrency, and corruption-detection tests.

## Verification

- [ ] `go test ./internal/domain/...`
- [ ] `go test ./internal/persistence/sqlite/...`
- [ ] Run migration tests against temporary on-disk databases, not only in-memory SQLite.

## Acceptance gate

- [ ] Invalid state transitions cannot be persisted through public repository methods.
- [ ] Replaying an idempotent create returns the original operation.
- [ ] The schema contains no multi-host or billing tables.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
