# Agent server (Go SDK)

**Requires a live `openboxd` host** for the create/exec/cleanup path. Webhook
delivery is documented below but **not yet wired** in `pkg/openbox` (Slice E
pending).

Minimal Go program using [`pkg/openbox`](../../pkg/openbox) to create a
restricted-egress sandbox, wait for readiness, exec a command, and delete the
instance. Includes a stub HTTP handler showing the webhook receiver pattern from
[`docs/api/webhooks.md`](../docs/api/webhooks.md).

## Prerequisites

| Requirement | Notes |
|-------------|-------|
| Live host | For `go run` against real API |
| `OPENBOX_TOKEN` | Owner bearer token |
| `OPENBOX_SERVER` | API URL (default `http://127.0.0.1:8443`) |
| Go 1.24+ | Same module as repo root |

## Build and run

From the repository root:

```sh
export OPENBOX_TOKEN='…'
export OPENBOX_SERVER='http://127.0.0.1:8443'   # optional

go run ./examples/agent-server/
```

Environment variables are read at runtime; nothing is checked in.

## What the program does

1. `Negotiate` with the daemon.
2. `CreateInstance` — sandbox with 30-minute TTL (restricted egress default).
3. Poll `GetOperation` until create succeeds.
4. `ExecInstance` — run `uname -a` and print stdout frames.
5. `DeleteInstance` and wait for delete operation.

## Webhook receiver (TODO)

When Slice E lands, extend this example to:

1. `POST /v1/webhook-subscriptions` (via forthcoming SDK methods).
2. Run an HTTPS listener that verifies `X-OpenBox-Signature` HMAC per
   [`docs/api/webhooks.md`](../docs/api/webhooks.md).
3. React to `operation.succeeded` / `instance.deleted` instead of polling.

`main.go` includes a `webhookStubHandler` and comments where subscription CRUD
will plug in. **No webhook client types exist in `pkg/openbox` today.**

## Without a live host

Compile-only check:

```sh
go build -o /dev/null ./examples/agent-server/
```

SDK contract tests under `pkg/openbox/contract_test.go` cover the HTTP mapping
without a daemon.

## Security notes

- Sandboxes default to **restricted egress**; do not pass secrets in exec argv or
  source code.
- Webhook `secret` values must come from environment at runtime, never from git.
