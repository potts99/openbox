---
title: "Slice 19 — Egress allowlists and network UX"
status: complete
milestone: "M4 Sandbox and containment"
depends_on: ["14-egress-and-instance-network-policy"]
spec: "../specs/2026-07-14-openbox-design.md"
design: "../superpowers/specs/2026-07-16-egress-allowlists-and-network-ux-design.md"
---

# Slice 19 — Egress allowlists and network UX

## Goal

Close the intentional gaps left by [Slice 14](14-egress-and-instance-network-policy.md): administrator allowlists that actually program restricted ACLs, host-side DNS resolution on the apply path, dedicated network-policy UX, and live Incus connectivity proof.

## Dependencies

- [14-egress-and-instance-network-policy](14-egress-and-instance-network-policy.md)

## Non-goals

- No user-authored eBPF programs.
- No transparent TLS interception.
- No cross-host / multi-node network policy.
- No Slice 15 LLM Gateway credential work (only destination wiring to the existing management placeholder).

## Test-first implementation tasks

1. [x] Persist and validate profile-linked restricted allowlists as IP/CIDR and exact hostnames.
2. [x] On apply, resolve hostnames via `dnsproxy` with rebinding protection; program `EnsureRestrictedACL(RestrictedACLName(instanceID), destinations)` and stack it in `NICACLs`.
3. [x] Refresh resolved ACL destinations via `dnsproxy` TTL bounds on subsequent apply/resolve (fail closed if refresh yields no safe addresses).
4. [x] Expose profile CRUD + attach on the versioned API and `openbox network` CLI.
5. [x] Add `NetworkPolicy` dashboard page and instance-detail policy fields.
6. [x] Update resolution state on inspect from `idle` to pending/resolved/failed with hostname lists (still no payload/IP logging of traffic).
7. [x] Add opt-in live Incus connectivity matrix hook (`OPENBOX_INCUS_TEST_SOCKET`); full host proof remains operator-gated.
8. [x] Document operator workflow for approving destinations and interpreting fail-closed apply/refresh errors.

## Verification

- [x] Restricted sandbox with empty allowlist still cannot reach arbitrary internet or peers (unit/fake ACL composition + default-deny baseline).
- [x] Restricted sandbox with approved public destination programs only that destination (+ DNS/LLM baseline) on the apply path.
- [x] Hostname allowlist that rebinds to private/bridge ranges is rejected and blocks readiness or ACL refresh.
- [x] Live Incus suite skips unless `OPENBOX_INCUS_TEST_SOCKET` is set.

## Acceptance gate

- [x] Administrators can grant and revoke restricted destinations via profiles without widening peer or host access.
- [x] Host-side DNS resolution is on the apply path; guests cannot relax allowlists.
- [x] Operators can see and edit effective network policy in CLI and dashboard without packet/DNS payload logs.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull Slice 15+ gateway credential work or v0.2 multi-tenant policy into this slice.
