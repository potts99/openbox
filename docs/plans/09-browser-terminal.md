---
title: "Slice 09 — Browser terminal and persistent PTY sessions"
status: planned
milestone: "M2 SSH and web access"
depends_on: ["03-container-lifecycle", "07-owner-auth-and-dashboard-shell"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 09 — Browser terminal and persistent PTY sessions

## Goal

Open authenticated terminals inside instances from the dashboard without ever spawning a host shell.

## Dependencies

- [03-container-lifecycle](03-container-lifecycle.md)
- [07-owner-auth-and-dashboard-shell](07-owner-auth-and-dashboard-shell.md)

## Non-goals

- No custom chat UI.
- No terminal-content logging.
- No shared collaborative terminal.

## Proposed files

- `internal/terminal/`
- `internal/httpapi/terminal_handlers.go`
- `web/src/terminal/`
- `web/src/pages/InstanceTerminal.tsx`

## Test-first implementation tasks

1. [ ] Write protocol tests for open, input, output, resize, signal, detach, reconnect, exit, and error frames.
2. [ ] Authorize each WebSocket against the browser session, instance ownership, CSRF/origin policy, and actual runtime identity.
3. [ ] Create PTYs only through the runtime adapter and reject requests targeting the host or unmanaged instances.
4. [ ] Enforce frame-size, rate, concurrent-session, and idle limits.
5. [ ] Implement resize and exit-status propagation with cancellation on explicit terminate.
6. [ ] Build the browser terminal with accessible connection state, reconnect controls, and copy/paste behavior.
7. [ ] Provide a named `tmux` helper contract for persistent sessions while keeping generic shells independent of tmux.
8. [ ] Audit start/end metadata only and prove terminal payloads are excluded from logs.

## Verification

- [ ] WebSocket protocol unit tests.
- [ ] Cross-instance authorization and origin tests.
- [ ] Disconnect/reconnect and daemon-restart behavior tests.
- [ ] Browser tests for resize, keyboard input, screen-reader labels, and explicit termination.

## Acceptance gate

- [ ] Closing a tab can detach without terminating a named tmux session.
- [ ] No terminal data is written to application or audit logs.
- [ ] A terminal request can never address an arbitrary Incus instance ID.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
