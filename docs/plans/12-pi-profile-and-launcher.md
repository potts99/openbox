---
title: "Slice 12 — Shared Pi profile and browser-TUI launcher"
status: planned
milestone: "M3 Devbox and Pi"
depends_on: ["09-browser-terminal", "11-images-snapshots-and-devbox-cloning"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 12 — Shared Pi profile and browser-TUI launcher

## Goal

Ship Pi as the default v0.1 agent with shared non-secret configuration and persistent native-TUI sessions.

## Dependencies

- [09-browser-terminal](09-browser-terminal.md)
- [11-images-snapshots-and-devbox-cloning](11-images-snapshots-and-devbox-cloning.md)

## Non-goals

- No OpenBox chat UI.
- No other agent adapters.
- No upstream provider secret injection in this slice.

## Proposed files

- `images/devbox/`
- `internal/pi/`
- `internal/profiles/pi/`
- `packages/pi-openbox/`
- `web/src/pages/PiProfile.tsx`
- `web/src/components/LaunchPi.tsx`
- `cmd/openbox/pi.go`

## Test-first implementation tasks

1. [ ] Pin Pi and tmux versions in the Devbox image manifest and verify installation without executing untrusted lifecycle scripts.
2. [ ] Model a versioned owner-level Pi profile for global settings, packages, extensions, skills, prompts, themes, and model preferences.
3. [ ] Keep project-local `.pi` resources inside the project and preserve Pi’s project-trust prompts.
4. [ ] Materialize shared non-secret profile content atomically into a managed location inside selected instances.
5. [ ] Implement Launch Pi as start-or-attach to a named tmux session in the selected working directory.
6. [ ] Persist Pi sessions and local unsupported product logins on the Devbox filesystem across stop/start.
7. [ ] Show Launch Pi only for Pi-enabled Devboxes and Sandboxes; keep plain VPS images clean.
8. [ ] Build profile preview, version history, apply, and rollback controls.
9. [ ] Document that clean bases must not contain personal Pi authentication.

## Verification

- [ ] Profile merge, version, rollback, and path-traversal tests.
- [ ] Image smoke test running `pi --version`.
- [ ] Browser launch, detach, reconnect, resize, and instance restart tests.
- [ ] Verify plain VPS and clean base images contain no Pi credentials.

## Acceptance gate

- [ ] A user can update one Pi profile and apply it to multiple selected instances.
- [ ] Closing the browser does not terminate Pi.
- [ ] OpenBox stores no terminal transcript or model conversation.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
