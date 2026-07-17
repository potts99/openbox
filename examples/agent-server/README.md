# Agent server (Go SDK)

**Requires a live `openboxd` host** for the create/exec/cleanup path.

Minimal Go program using [`pkg/openbox`](../../pkg/openbox) to create a
restricted-egress sandbox, wait for readiness, exec a command, and delete the
instance. Optionally registers a webhook subscription and verifies signed
deliveries per [`docs/api/webhooks.md`](../../docs/api/webhooks.md).

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

## Optional webhooks

Point OpenBox at a publicly reachable **HTTPS** receiver (or a tunnel to the
local listener). Production delivery rejects non-HTTPS URLs.

```sh
export OPENBOX_WEBHOOK_URL='https://receiver.example/hooks/openbox'
export OPENBOX_WEBHOOK_LISTEN=':8787'   # local verify listener (optional)
go run ./examples/agent-server/
```

When `OPENBOX_WEBHOOK_URL` is set, the example:

1. Creates a subscription for `operation.terminal` and `instance.deleted`
2. Starts a local listener that checks `X-OpenBox-Signature` / timestamp headers
3. Lists deliveries after cleanup, then deletes the subscription

Signing secret comes from the create response — never check secrets into git.

## What the program does

1. `Negotiate` with the daemon.
2. Optional webhook registration via `CreateWebhookSubscription`.
3. `CreateInstance` — sandbox with 30-minute TTL (restricted egress default).
4. Poll `GetOperation` until create succeeds.
5. `ExecInstance` — run `uname -a` and print stdout frames.
6. `DeleteInstance` and wait for delete operation.
7. Optional `ListWebhookDeliveries` and subscription cleanup.

## Without a live host

Compile-only check:

```sh
go build -o /dev/null ./examples/agent-server/
```

## Security notes

- Sandboxes default to **restricted egress**; do not pass secrets in exec argv or
  source code.
- Webhook secrets and tokens must come from the environment at runtime.
