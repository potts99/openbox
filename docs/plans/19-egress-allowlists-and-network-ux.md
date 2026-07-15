---
title: "Slice 19 — Egress allowlists and network UX"
status: planned
milestone: "M4 Sandbox and containment"
depends_on: ["14-egress-and-instance-network-policy"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 19 — Egress allowlists and network UX

## Goal

Close the intentional gaps left by [Slice 14](14-egress-and-instance-network-policy.md): administrator allowlists that actually program restricted ACLs, host-side DNS resolution on the apply path, dedicated network-policy UX, and live Incus connectivity proof.

## Dependencies

- [14-egress-and-instance-network-policy](14-egress-and-instance-network-policy.md)

## Why this slice exists

Slice 14 shipped host-enforced `standard` / `restricted` egress, baseline NIC ACLs, fail-closed apply/verify, and inspect telemetry. The following were deliberately deferred so the containment spine could land first:

- Restricted instances start with an **empty** allowlist (baseline DNS + LLM placeholder only).
- `internal/dnsproxy` is tested in isolation but **not** called from `ApplyNetworkPolicy`.
- `EnsureRestrictedACL` / `RestrictedACLName` exist but are unused on the create path when destinations are empty.
- Dashboard shows egress mode on instance detail; there is no dedicated network-policy page or CLI `network` surface.
- Connectivity matrix and peer-isolation proofs are unit/fake only; live Incus e2e remains optional.

## Non-goals

- No user-authored eBPF programs.
- No transparent TLS interception.
- No cross-host / multi-node network policy.
- No Slice 15 LLM Gateway credential work (only destination wiring to the existing management placeholder).

## Proposed files

- `internal/networkpolicy/` (allowlist attach + apply orchestration)
- `internal/dnsproxy/` (wire into apply; refresh hooks)
- `internal/runtime/incus/acl.go` (non-empty restricted ACL ensure + NIC stack)
- `internal/httpapi/` + OpenAPI (create/update allowlist fields)
- `cmd/openbox/network.go`
- `web/src/pages/NetworkPolicy.tsx`
- `docs/operators/` + `docs/security/networking.md` (allowlist ops)
- `tests/` or `internal/runtime/incus/*_integration_test.go` (live matrix)

## Test-first implementation tasks

1. [ ] Persist and validate per-instance (or profile-linked) restricted allowlists as IP/CIDR and exact hostnames.
2. [ ] On apply, resolve hostnames via `dnsproxy` with rebinding protection; program `EnsureRestrictedACL(RestrictedACLName(instanceID), destinations)` and stack it in `NICACLs`.
3. [ ] Refresh resolved ACL destinations on bounded TTL expiry without trusting the guest; fail closed if refresh yields no safe addresses.
4. [ ] Expose allowlist CRUD (or create-time + patch) on the versioned API and `openbox` CLI (`network` / inspect fields).
5. [ ] Add `NetworkPolicy` dashboard page: effective mode, ACL names, resolution state, denied-flow counters, allowlist editor for restricted instances.
6. [ ] Update resolution state on inspect from `idle` to pending/resolved/failed with hostname lists (still no payload/IP logging of traffic).
7. [ ] Add opt-in live Incus connectivity matrix covering host, peer, DNS, internet, LLM gateway placeholder, and allowlist destinations for container and VM.
8. [ ] Document operator workflow for approving destinations and interpreting fail-closed apply/refresh errors.

## Verification

- [ ] Restricted sandbox with empty allowlist still cannot reach arbitrary internet or peers.
- [ ] Restricted sandbox with approved public destination can reach only that destination (plus DNS/LLM baseline).
- [ ] Hostname allowlist that rebinds to private/bridge ranges is rejected and blocks readiness or ACL refresh.
- [ ] Live Incus suite (when `OPENBOX_INCUS_TEST_SOCKET` is set) passes the connectivity matrix for both runtimes.

## Acceptance gate

- [ ] Administrators can grant and revoke restricted destinations without widening peer or host access.
- [ ] Host-side DNS resolution is on the apply path; guests cannot relax allowlists.
- [ ] Operators can see and edit effective network policy in CLI and dashboard without packet/DNS payload logs.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull Slice 15+ gateway credential work or v0.2 multi-tenant policy into this slice.

## Notes

Carried forward from Slice 14 completion (`feat/slice-14-egress-and-instance-network-policy` @ `cdb7330`).
