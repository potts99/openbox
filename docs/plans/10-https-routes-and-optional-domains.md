---
title: "Slice 10 — HTTPS routes and optional domains"
status: planned
milestone: "M2 SSH and web access"
depends_on: ["03-container-lifecycle", "07-owner-auth-and-dashboard-shell"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 10 — HTTPS routes and optional domains

## Goal

Expose explicitly approved instance ports privately or publicly while keeping domain ownership optional.

## Dependencies

- [03-container-lifecycle](03-container-lifecycle.md)
- [07-owner-auth-and-dashboard-shell](07-owner-auth-and-dashboard-shell.md)

## Non-goals

- No automatic exposure of every listening port.
- No OpenBox-operated shared domain.
- No arbitrary reverse-proxy targets.

## Proposed files

- `internal/routes/`
- `internal/caddy/`
- `internal/httpapi/route_handlers.go`
- `cmd/openbox/route.go`
- `cmd/openbox/forward.go`
- `web/src/pages/Routes.tsx`
- `deploy/caddy/`

## Test-first implementation tasks

1. [ ] Write route-policy tests for managed target identity, allowed port range, private-by-default visibility, and hostname uniqueness.
2. [ ] Implement route CRUD and detected-port suggestions without automatic publication.
3. [ ] Generate Caddy configuration only from approved database routes and apply it atomically with rollback.
4. [ ] Implement a certificate-issuance allow endpoint that authorizes only active approved hostnames.
5. [ ] Implement private-route authentication through owner sessions or scoped route tokens; public routes bypass OpenBox login.
6. [ ] Forward WebSockets, SSE, streaming responses, and canonical `X-Forwarded-*` headers.
7. [ ] Validate custom-domain DNS before activation and provide actionable pending/invalid states.
8. [ ] Implement `openbox forward` as an SSH tunnel for installations without a domain.
9. [ ] Test gateway failure independently from instance lifecycle and host SSH.

## Verification

- [ ] Policy and SSRF tests.
- [ ] Caddy configuration golden tests.
- [ ] Private/public browser and API route tests.
- [ ] WebSocket/SSE streaming integration tests.
- [ ] Certificate allowlist abuse and DNS failure tests.

## Acceptance gate

- [ ] A route cannot target the host, gateway, another owner, or an unmanaged address.
- [ ] New routes are private and require an explicit publish action.
- [ ] All core workflows remain usable through SSH tunnelling with no domain.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
