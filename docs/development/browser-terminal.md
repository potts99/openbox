# Browser terminal development notes

The browser terminal slice keeps these boundaries separate:

- `internal/terminal` owns the WebSocket JSON frame protocol, inbound limits,
  idle helpers, and the named `tmux` argv contract.
- `internal/httpapi` owns authorization, origin/CSRF upgrade policy, the PTY
  bridge, the persistent-console reconnect registry, and terminal audit emission.
- `internal/runtime` (and adapters) open guest consoles; handlers never pass a
  client-supplied Incus identity into `OpenConsole`.
- The dashboard UI (`web/src/terminal/`, `InstanceTerminal`) is a thin client of
  the protocol and must not invent alternate auth or target semantics.

## WebSocket path

```text
GET /v1/instances/{openbox_instance_id}/terminal
```

`{openbox_instance_id}` is an OpenBox instance ID. After session/cookie (or
bearer) auth, the handler loads the owned instance record and uses that row's
`RuntimeRef` for the console. A client cannot address an arbitrary Incus
instance by putting a foreign ID in the path or in an `open` frame.

## CSRF and origin

Cookie-authenticated upgrades are state-changing. Browsers cannot set custom
headers on `WebSocket`, so the handshake accepts the per-session CSRF token as
`?csrf=` **only** on `GET` with `Upgrade: websocket`. Non-WebSocket cookie
mutations still require `X-CSRF-Token`.

Origin must match the request host (`Origin` host equals `Host`) for
cookie-authenticated upgrades. Bearer-authenticated clients may omit `Origin`
(no ambient cookie authority). Cross-origin cookie upgrades are rejected before
the console opens.

## Protocol and limits

Frames: `open`, `reconnect`, `input`, `output`, `resize`, `signal`, `detach`,
`exit`, `error`. Defaults live in `terminal.DefaultLimits()` (frame size, inbound
rate, concurrent sessions per owner/instance, idle timeout).

## Named tmux sessions

`open` with `session_name` runs the documented attach-or-create argv
(`tmux new-session -A -s <name>`). Empty `session_name` keeps a generic shell
(`/bin/bash`) with no tmux dependency. Detach / tab close leaves a named
session registered under a daemon `session_id` for `reconnect` or a later
`open` with the same name. See `TestTerminalDetachDoesNotTerminateNamedSession`
and related tests in `internal/httpapi`.

The dashboard terminal always opens `session_name: main` (no ephemeral shell UI).

## Audit metadata only

`TerminalAuditor` records start/end lifecycle metadata (owner, instance id,
session id/name, phase, end reason). It must never receive PTY bytes or encoded
frame payloads. Proof: `TestTerminalAuditRecordsStartEndWithoutPayloads`. The
daemon maps events to durable `terminal.session` audit rows the same way SSH
maps to `ssh.session`.

## Known follow-up

Production Incus interactive exec WebSockets (beyond the current runtime
console adapter) remain a later hardening item; the fake runtime covers the
bridge contract in unit tests.

Run focused checks with:

```sh
go test ./internal/httpapi/... ./internal/terminal/... ./cmd/openboxd -count=1
```
