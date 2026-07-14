---
title: "Slice 07 — Owner authentication and dashboard shell"
status: planned
milestone: "M2 SSH and web access"
depends_on: ["01-domain-and-storage", "06-versioned-api-and-cli"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 07 — Owner authentication and dashboard shell

## Goal

Create secure single-owner bootstrap, browser/API sessions, token management, and an accessible dashboard foundation.

## Dependencies

- [01-domain-and-storage](01-domain-and-storage.md)
- [06-versioned-api-and-cli](06-versioned-api-and-cli.md)

## Non-goals

- No terminal WebSocket.
- No instance web routes.
- No multi-user invitation flow.

## Proposed files

- `internal/auth/`
- `internal/httpapi/auth_handlers.go`
- `web/src/auth/`
- `web/src/app/`
- `web/src/components/`
- `web/src/pages/`
- `internal/assets/`

## Test-first implementation tasks

1. [ ] Write bootstrap tests proving the one-time secret expires and cannot create a second owner.
2. [ ] Generate the bootstrap secret locally, store only a hash, and refuse remote bootstrap unless explicitly tunneled or TLS-protected.
3. [ ] Store the administrator password with Argon2id parameters that can be upgraded on login.
4. [ ] Implement secure session cookies, CSRF tokens, logout, expiration, rotation, and rate-limited login.
5. [ ] Implement scoped bearer-token creation, listing, revocation, and hash-only persistence.
6. [ ] Implement SSH-key CRUD by fingerprint with duplicate and malformed-key validation.
7. [ ] Build the React application shell, login/setup screens, capability banner, operation drawer, and instance-list placeholder.
8. [ ] Embed versioned frontend assets into `openboxd` and add an accessible no-JavaScript failure page.

## Verification

- [ ] Authentication unit tests including timing-safe comparisons.
- [ ] CSRF, cookie, session fixation, brute-force, and token-revocation integration tests.
- [ ] React component tests and automated accessibility checks.
- [ ] Asset embedding and cache-header tests.

## Acceptance gate

- [ ] A fresh installation can create exactly one owner and then disables bootstrap.
- [ ] Revoking an API token prevents its next request.
- [ ] The dashboard is keyboard navigable and communicates unavailable capabilities.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
