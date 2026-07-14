---
title: "Slice 05 — Durable operations and reconciliation"
status: planned
milestone: "M1 Core instance engine"
depends_on: ["01-domain-and-storage", "03-container-lifecycle"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 05 — Durable operations and reconciliation

## Goal

Make lifecycle work recoverable across daemon crashes, transient Incus outages, and partial external changes.

## Dependencies

- [01-domain-and-storage](01-domain-and-storage.md)
- [03-container-lifecycle](03-container-lifecycle.md)

## Non-goals

- No distributed queue.
- No multi-node leader election.
- No automatic recreation of missing persistent instances.

## Proposed files

- `internal/operations/`
- `internal/reconcile/`
- `internal/app/recovery/`
- `internal/clock/`
- `cmd/openboxd/`

## Test-first implementation tasks

1. [ ] Write crash-boundary tests for every lifecycle stage before implementing the worker.
2. [ ] Implement a bounded local worker queue whose source of truth is durable pending/running operations in SQLite.
3. [ ] Persist stage transitions before and after external actions and attach structured progress events.
4. [ ] Classify transient, correctable, and integrity-threatening failures with bounded exponential backoff.
5. [ ] On startup, recover abandoned operations by inspecting both durable stage and runtime identity.
6. [ ] Implement desired-versus-observed reconciliation for running, stopped, and deleted states.
7. [ ] Mark missing persistent runtime data `runtime_missing`; expose explicit forget, adopt, and restore entry points without silent recreation.
8. [ ] Leave unknown Incus instances untouched and surface them as unmanaged diagnostics.
9. [ ] Enter degraded read-only mode when Incus is unavailable while continuing metadata reads.

## Verification

- [ ] Deterministic fake-clock retry tests.
- [ ] Kill-and-restart tests at each operation stage.
- [ ] Incus outage and recovery integration test.
- [ ] Missing runtime, replacement identity, orphan adoption, and unmanaged-resource tests.

## Acceptance gate

- [ ] Restarting `openboxd` neither duplicates instances nor loses operation status.
- [ ] A missing VPS or Devbox never becomes an empty replacement automatically.
- [ ] Every retry and terminal failure is visible through one operation event model.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
