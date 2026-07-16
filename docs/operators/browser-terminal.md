# Browser terminal operations

OpenBox exposes an authenticated browser terminal over a WebSocket on the
private API. Consoles run inside managed instances only — never as a host shell.

## Connect

Path:

```text
GET /v1/instances/{instance_id}/terminal
```

Use the OpenBox instance ID from the dashboard or API. The daemon resolves the
runtime identity from the owned instance record; clients cannot target an
arbitrary Incus ID.

### Authentication

- Browser session cookie plus CSRF, or
- Bearer API token (no CSRF).

For browser WebSockets, pass CSRF as a query parameter:

```text
?csrf=<session_csrf_token>
```

(`X-CSRF-Token` is preferred when a custom header is available.) Cookie session
upgrades require a page `Origin` that matches the API host. Bearer upgrades do
not require `Origin`.

### Limits

The daemon enforces frame size, inbound rate, concurrent sessions per owner and
per instance, and idle timeout. Exceeding a limit closes the WebSocket with a
typed error frame (`frame_too_large`, `rate_limited`, `idle_timeout`).

## Persistent sessions (tmux)

Opening with a `session_name` attaches or creates a named `tmux` session inside
the guest. Closing the browser tab or sending `detach` ends the WebSocket
without terminating that named session. Reopen the terminal and reconnect by
`session_id` (from the open ack) or open again with the same `session_name`.

Ephemeral shells (no `session_name`) end when the tab closes or the client
detaches.

The console hides the browser Terminal control for `kind=sandbox` instances
until sandbox images ship `tmux`. Use the SSH gateway (and the console Connect
panel) for sandboxes instead.

## Audit and logging

Terminal audit records contain owner, instance id, optional session id/name,
start/end phase, and an end reason (`exit`, `detach`, `idle_timeout`, limits,
errors). They do **not** contain keystrokes, screen output, or WebSocket frame
payloads. Application logs must not record terminal contents either.

## Failure notes

If the console adapter is unavailable, the upgrade still authorizes but returns
`not_implemented`. Guest or runtime failures surface as `error` / `exit` frames
without writing PTY traffic to logs.

Interactive Incus exec WebSocket production wiring may evolve; operators should
treat the OpenBox instance ID as the only supported target selector.
