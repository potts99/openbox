---
title: "Slice 14 — Egress and instance network policy"
status: planned
milestone: "M4 Sandbox and containment"
depends_on: ["02-runtime-contract-and-incus-preflight", "13-sandbox-exec-and-expiry"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 14 — Egress and instance network policy

## Goal

Enforce host-external network policy so instances cannot relax containment from inside the guest.

## Dependencies

- [02-runtime-contract-and-incus-preflight](02-runtime-contract-and-incus-preflight.md)
- [13-sandbox-exec-and-expiry](13-sandbox-exec-and-expiry.md)

## Non-goals

- No user-authored eBPF programs.
- No transparent TLS interception.
- No cross-host network policy.

## Proposed files

- `internal/networkpolicy/`
- `internal/runtime/incus/acl.go`
- `internal/dnsproxy/`
- `cmd/openbox/network.go`
- `web/src/pages/NetworkPolicy.tsx`
- `docs/security/networking.md`

## Test-first implementation tasks

1. [ ] Write a connectivity matrix for host, peer instance, DNS, internet, LLM Gateway, and administrator allowlist destinations.
2. [ ] Create default-deny east-west Incus ACLs and explicitly permit required gateway and DNS traffic.
3. [ ] Implement `standard` egress with outbound internet and `restricted` egress with administrator-approved destinations.
4. [ ] Resolve allowlist DNS outside the guest with bounded refresh and rebinding protection.
5. [ ] Ensure policies are keyed to stable instance identity rather than mutable guest IP alone.
6. [ ] Apply policy before reporting a new instance ready and remove it only after runtime deletion.
7. [ ] Prevent guest root from changing host ACLs, routes, or DNS policy.
8. [ ] Expose effective policy, resolution state, and denied-flow counters without logging payloads.
9. [ ] Add a fail-closed mode when policy programming is inconsistent.

## Verification

- [ ] Connectivity matrix integration suite for containers and VMs.
- [ ] DNS rebinding, wildcard, IPv6, private-range, and stale-resolution tests.
- [ ] Peer isolation and host-service denial tests.
- [ ] Policy update rollback and daemon-restart reconciliation tests.

## Acceptance gate

- [ ] Restricted Sandboxes cannot reach arbitrary internet or peer instances.
- [ ] Standard Devboxes retain ordinary package, Git, and provider access.
- [ ] A policy failure prevents readiness instead of silently granting broader access.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
