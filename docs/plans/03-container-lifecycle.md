---
title: "Slice 03 — Container instance lifecycle"
status: planned
milestone: "M1 Core instance engine"
depends_on: ["01-domain-and-storage", "02-runtime-contract-and-incus-preflight"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 03 — Container instance lifecycle

## Goal

Deliver the first useful vertical runtime slice: persistent unprivileged system containers managed through application services.

## Dependencies

- [01-domain-and-storage](01-domain-and-storage.md)
- [02-runtime-contract-and-incus-preflight](02-runtime-contract-and-incus-preflight.md)

## Non-goals

- No VMs.
- No public API.
- No automatic reconciliation loop.

## Proposed files

- `internal/app/instances/`
- `internal/runtime/incus/containers.go`
- `internal/images/`
- `internal/cloudinit/`
- `internal/app/instances/*_test.go`

## Test-first implementation tasks

1. [ ] Write application-service tests for create, inspect, start, stop, restart, and delete using the fake runtime.
2. [ ] Resolve image aliases to immutable fingerprints before creating an instance.
3. [ ] Create only unprivileged Incus system containers in the dedicated project and network.
4. [ ] Apply CPU, memory, disk, owner key, OpenBox labels, and instance metadata without shell-string construction.
5. [ ] Return typed capability errors when an image or requested option is incompatible with containers.
6. [ ] Implement observed-state refresh from Incus and preserve runtime identifiers separately from human names.
7. [ ] Make delete idempotent and verify runtime removal before producing a deleted tombstone.
8. [ ] Add a real-Incus integration scenario covering create through delete and daemon reconnect.

## Verification

- [ ] Application tests with failures injected at each runtime call.
- [ ] Integration lifecycle on a temporary Incus project.
- [ ] Verify root in the guest maps to an unprivileged host ID.
- [ ] Verify an existing unmanaged name collision is never adopted automatically.

## Acceptance gate

- [ ] A container can be created, stopped, restarted, and deleted without direct Incus use by callers.
- [ ] Repeated delete is successful and does not touch a replacement resource with a different identity.
- [ ] Actual isolation is always stored and reported as `container`.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
