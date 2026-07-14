---
title: "Slice 11 — Images, snapshots, and Devbox cloning"
status: planned
milestone: "M3 Devbox and Pi"
depends_on: ["04-vm-lifecycle", "05-durable-operations-and-reconciliation"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 11 — Images, snapshots, and Devbox cloning

## Goal

Create reproducible images, safe snapshots, protected Devbox bases, and verified storage-efficient copies.

## Dependencies

- [04-vm-lifecycle](04-vm-lifecycle.md)
- [05-durable-operations-and-reconciliation](05-durable-operations-and-reconciliation.md)

## Non-goals

- No scheduled custom image builder.
- No cross-host copies.
- No separate Template resource.

## Proposed files

- `internal/images/`
- `internal/snapshots/`
- `internal/app/clones/`
- `cmd/openbox/image.go`
- `cmd/openbox/snapshot.go`
- `cmd/openbox/cp.go`
- `web/src/pages/Images.tsx`
- `web/src/pages/Snapshots.tsx`

## Test-first implementation tasks

1. [ ] Write image-resolution tests proving aliases pin immutable fingerprints per instance.
2. [ ] Define curated general, sandbox, and Devbox image manifests with architecture and runtime compatibility.
3. [ ] Implement snapshot create, list, inspect, restore-as-new, and delete through durable operations.
4. [ ] Protect designated Devbox bases from deletion until protection is explicitly removed.
5. [ ] Implement `cp source target` using the runtime copy primitive and record source snapshot/fingerprint provenance.
6. [ ] Verify copied storage and runtime identity before marking the operation complete.
7. [ ] Prove deleting a source or snapshot does not invalidate completed clones.
8. [ ] Detect unsupported storage-efficient copy capability and report the slower fallback before execution.
9. [ ] Warn when cloning a non-clean personal Devbox because all guest files may include secrets.

## Verification

- [ ] Image pinning and alias-update tests.
- [ ] Container and VM snapshot/copy integration suites.
- [ ] Interrupted copy cleanup and source deletion tests.
- [ ] Protected-base and secret-warning UI/CLI tests.

## Acceptance gate

- [ ] Existing instances never change when an image alias updates.
- [ ] A clone is independently deletable and startable after its source is removed.
- [ ] OpenBox never claims copy-on-write behavior unless the selected Incus storage provides it.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
