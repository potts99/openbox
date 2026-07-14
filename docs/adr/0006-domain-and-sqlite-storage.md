# ADR 0006: Domain invariants and transactional metadata

- Status: Accepted
- Date: 2026-07-14

## Context

OpenBox must preserve lifecycle safety before an Incus runtime is connected. It also needs idempotent operations and a schema that can gain multiple owners later without implementing multi-user behavior in v0.1.

## Decision

Domain types own instance-name, kind, expiry, isolation, lifecycle-transition, and protected-base rules. Public persistence methods validate these rules before writing. SQLite reinforces finite-value and ownership constraints, but database errors do not replace domain validation.

All timestamps are stored as UTC RFC 3339 values. Migrations are forward-only, embedded in the binary, serialized with an immediate write lock, and verified by SHA-256 checksum on every open. Instance creation or mutation and its durable operation share one transaction. Idempotency is owner-scoped and compares a caller-provided request hash.

Deleted instances leave a minimal tombstone containing identity, owner, deletion operation, and time. Active instance and runtime metadata are removed. Owner IDs are present on every owner-scoped table, with composite foreign keys preventing future cross-owner references.

## Consequences

Invalid public lifecycle writes fail before SQL execution, replayed requests can return the original operation, and edited historical migrations are detected. Schema migrations may only move forward; a correction requires a new migration. Runtime reconciliation, HTTP transport, workers, multi-host state, and billing remain outside this slice.
