---
title: "Slice 15 — Agent-neutral LLM Gateway security core"
status: planned
milestone: "M5 OpenBox LLM Gateway"
depends_on: ["07-owner-auth-and-dashboard-shell", "14-egress-and-instance-network-policy"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 15 — Agent-neutral LLM Gateway security core

## Goal

Build the separate credential and streaming trust boundary before adding any real provider adapter.

## Dependencies

- [07-owner-auth-and-dashboard-shell](07-owner-auth-and-dashboard-shell.md)
- [14-egress-and-instance-network-policy](14-egress-and-instance-network-policy.md)

## Non-goals

- No real Anthropic, OpenAI, or Google traffic.
- No protocol translation.
- No prompt/response storage.

## Proposed files

- `cmd/openbox-gatewayd/`
- `internal/gateway/`
- `internal/gatewaystore/`
- `internal/grants/`
- `internal/cryptokey/`
- `internal/httpapi/credential_handlers.go`
- `deploy/systemd/openbox-gatewayd.service`

## Test-first implementation tasks

1. [ ] Write a threat-model test matrix for credential confidentiality, grant scope, replay, revocation, cross-instance access, and log redaction.
2. [ ] Create a separate gateway database and authenticated-encryption master key file; keep secret material out of OpenBox SQLite.
3. [ ] Expose credential-management operations only over a root-restricted Unix socket used by `openboxd`.
4. [ ] Issue signed short-lived grants scoped to owner, stable runtime identity, credential profile, provider, audience, and expiry.
5. [ ] Validate grants independently in `openbox-gatewayd`; bind acceptance to the private OpenBox network.
6. [ ] Implement a provider-route registry that cannot forward arbitrary schemes, hosts, ports, or paths.
7. [ ] Build a streaming reverse-proxy core with cancellation, bounded headers/body buffers, rate limits, and concurrency limits.
8. [ ] Log request IDs, provider, model metadata when available, status, latency, and usage fields without bodies or credentials.
9. [ ] Implement immediate credential and grant revocation with cache invalidation.
10. [ ] Add separate backup/export and restore validation for encrypted store and master key.

## Verification

- [ ] Property tests for encrypt/decrypt and key separation.
- [ ] Replay, expiry, audience, provider-scope, and cross-instance grant tests.
- [ ] SSRF, header smuggling, cancellation, backpressure, and redaction tests.
- [ ] Restore with correct, missing, and incorrect master keys.

## Acceptance gate

- [ ] `openboxd` cannot decrypt provider credentials.
- [ ] A fake provider receives upstream auth while the instance sees only its short-lived OpenBox grant.
- [ ] Gateway logs and errors contain neither upstream secrets nor request/response bodies.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
