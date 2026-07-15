---
title: "Slice 11 — Images, snapshots, and Devbox cloning"
status: implemented
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

1. [x] Write image-resolution tests proving aliases pin immutable fingerprints per instance.
2. [x] Define curated general, sandbox, and Devbox image manifests with architecture and runtime compatibility.
3. [x] Implement snapshot create, list, inspect, restore-as-new, and delete through durable operations.
4. [x] Protect designated Devbox bases from deletion until protection is explicitly removed.
5. [x] Implement `cp source target` using the runtime copy primitive and record source snapshot/fingerprint provenance.
6. [x] Verify copied storage and runtime identity before marking the operation complete.
7. [x] Prove deleting a source or snapshot does not invalidate completed clones.
8. [x] Detect unsupported storage-efficient copy capability and report the slower fallback before execution.
9. [x] Warn when cloning a non-clean personal Devbox because all guest files may include secrets.

## Verification

- [x] Image pinning and alias-update tests.
- [x] Container and VM snapshot/copy integration suites.
- [x] Interrupted copy cleanup and source deletion tests.
- [x] Protected-base and secret-warning UI/CLI tests.

## Acceptance gate

- [x] Existing instances never change when an image alias updates.
- [x] A clone is independently deletable and startable after its source is removed.
- [x] OpenBox never claims copy-on-write behavior unless the selected Incus storage provides it.

## Documentation

- [operators/images-snapshots-cloning](../operators/images-snapshots-cloning.md)
- [development/images-snapshots-cloning](../development/images-snapshots-cloning.md)

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.

## Known gaps (intentionally deferred)

- Dedicated `openbox image|snapshot|cp` CLI subcommands and web pages (SSH `cp` and service APIs cover the control plane for v0.1).
- HTTP `/v1` surface for protect/snapshots (service + SSH available; OpenAPI expansion can follow).
