---
title: "Slice 02 — Runtime contract and Incus preflight"
status: planned
milestone: "M1 Core instance engine"
depends_on: ["00-foundation"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 02 — Runtime contract and Incus preflight

## Goal

Create a narrow runtime contract, fake implementation, and read-mostly Incus adapter capable of validating a host safely.

## Dependencies

- [00-foundation](00-foundation.md)

## Non-goals

- No instance creation.
- No automatic deletion of unmanaged Incus resources.
- No KVM-specific lifecycle yet.

## Proposed files

- `internal/runtime/runtime.go`
- `internal/runtime/fake/`
- `internal/runtime/incus/`
- `internal/doctor/`
- `cmd/openboxd/`

## Test-first implementation tasks

1. [ ] Write contract tests describing capabilities, images, instance inspection, lifecycle operations, exec, snapshot, copy, and deletion semantics.
2. [ ] Implement a deterministic fake runtime used by application and failure-injection tests.
3. [ ] Connect the Incus adapter only through the local Unix socket with explicit timeouts and cancellation.
4. [ ] Implement capability discovery for namespaces, cgroups, storage drivers, network tooling, Incus version, architecture, `/dev/kvm`, and VM support.
5. [ ] Implement `openbox doctor --json` and human output with pass, warning, unavailable, and fatal results.
6. [ ] Create an idempotent bootstrap operation for the dedicated Incus project, managed bridge, storage reference, profiles, and OpenBox labels.
7. [ ] Refuse to mutate unknown projects or resources; report conflicts with repair guidance.
8. [ ] Add Incus integration tests that run only when an explicit test environment is supplied.

## Verification

- [ ] Runtime contract suite against fake adapter.
- [ ] Read-only preflight tests against a real Incus daemon.
- [ ] Bootstrap twice and assert identical resulting configuration.
- [ ] Verify an unrelated Incus project remains unchanged.

## Acceptance gate

- [ ] A host without KVM passes container capability checks and reports strong isolation unavailable.
- [ ] The adapter never shells out to the `incus` CLI.
- [ ] Every managed Incus resource carries an OpenBox ownership label.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
