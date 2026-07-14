---
title: "Slice 04 — KVM-backed VM lifecycle"
status: planned
milestone: "M1 Core instance engine"
depends_on: ["03-container-lifecycle"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 04 — KVM-backed VM lifecycle

## Goal

Add strong-isolation instances through Incus VMs while preserving the same application contract and honest capability behavior.

## Dependencies

- [03-container-lifecycle](03-container-lifecycle.md)

## Non-goals

- No alternative hypervisor backend.
- No live migration.
- No promise that VM and container boot times match.

## Proposed files

- `internal/runtime/incus/vms.go`
- `internal/images/vm.go`
- `internal/cloudinit/vm.go`
- `internal/app/instances/isolation.go`
- `docs/operators/strong-isolation.md`

## Test-first implementation tasks

1. [ ] Extend lifecycle contract tests so both runtime types satisfy identical state and identity behavior.
2. [ ] Write capability tests for KVM absent, permission denied, nested virtualization unavailable, and supported.
3. [ ] Create VM-compatible root disks and cloud-init configuration pinned to image fingerprints.
4. [ ] Wait for the Incus guest agent and SSH readiness with bounded timeouts and useful progress stages.
5. [ ] Apply CPU, memory, disk, network, SSH keys, and OpenBox labels using VM-compatible Incus options.
6. [ ] Make `isolation=strong` fail before operation creation when VM capability is unavailable.
7. [ ] Make `isolation=best` select VM only according to documented policy and always persist the actual choice.
8. [ ] Run the VM integration suite on a KVM-capable CI runner.

## Verification

- [ ] Shared runtime contract suite for container and VM.
- [ ] KVM capability matrix tests.
- [ ] VM lifecycle and restart test on real KVM.
- [ ] Cloud-init timeout and partial-create cleanup tests.

## Acceptance gate

- [ ] Callers do not branch on Incus details; they branch only on declared capabilities.
- [ ] Strong isolation never falls back silently to a container.
- [ ] VM failure cannot damage an existing container with the same historical name.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
