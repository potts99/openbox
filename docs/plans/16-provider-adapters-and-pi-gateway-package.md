---
title: "Slice 16 — Provider adapters and Pi gateway package"
status: planned
milestone: "M5 OpenBox LLM Gateway"
depends_on: ["12-pi-profile-and-launcher", "15-llm-gateway-security-core"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 16 — Provider adapters and Pi gateway package

## Goal

Connect Pi to shared credentials through provider-native Anthropic, OpenAI, and Google gateway routes.

## Dependencies

- [12-pi-profile-and-launcher](12-pi-profile-and-launcher.md)
- [15-llm-gateway-security-core](15-llm-gateway-security-core.md)

## Non-goals

- No universal normalized model API.
- No guarantee that product-subscription tokens can be brokered.
- No Claude Code launcher in v0.1.

## Proposed files

- `internal/gateway/providers/anthropic/`
- `internal/gateway/providers/openai/`
- `internal/gateway/providers/google/`
- `internal/gateway/oauth/`
- `packages/pi-openbox/`
- `web/src/pages/Credentials.tsx`
- `web/src/pages/GatewayUsage.tsx`

## Test-first implementation tasks

1. [ ] Create provider conformance fixtures and mock streaming upstreams before real adapters.
2. [ ] Implement provider-native path, header, streaming, error, cancellation, and usage handling without protocol translation.
3. [ ] Implement API-key credential creation and one-time verification for each initial provider.
4. [ ] Implement OAuth only for flows whose technical behavior and provider terms permit brokered use; label unsupported product logins explicitly.
5. [ ] Serialize refresh per credential profile and use compare-and-swap versions so concurrent agents cannot overwrite rotated refresh tokens.
6. [ ] Extend the Pi package to override provider base URLs and retrieve short-lived instance grants without exposing upstream credentials.
7. [ ] Attach credential profiles explicitly per instance and show provider, scope, rate limit, and revocation status.
8. [ ] Allow Pi-supported providers without gateway adapters to continue using local Devbox credentials.
9. [ ] Test two concurrent Pi sessions sharing one credential through refresh, revocation, and stream cancellation.

## Verification

- [ ] Provider fixture and protocol conformance suites.
- [ ] OAuth refresh race and rotation tests.
- [ ] Pi extension unit tests and Devbox end-to-end tests.
- [ ] Credential attach/detach, immediate revoke, quota exhaustion, and no-body-logging tests.

## Acceptance gate

- [ ] Two contained Pi sessions use one supported shared credential without receiving it.
- [ ] Detaching a credential blocks the next request without restarting Pi.
- [ ] Unsupported subscription auth remains persistent only in its Devbox and is never copied silently.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
