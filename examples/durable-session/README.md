# Durable session

**Requires a live `openboxd` host** with Incus runtime, owner API token, and the
`openbox` CLI.

Keep a sandbox alive across multiple exec rounds by **extending its TTL** before
it expires. Uses restricted egress (sandbox default) and ordinary cleanup.

See [`docs/api/sandbox-lifecycle.md`](../docs/api/sandbox-lifecycle.md) (TTL and
extend semantics).

## Prerequisites

| Requirement | Notes |
|-------------|-------|
| Live host | Local or remote `openboxd` |
| `OPENBOX_TOKEN` | Owner bearer token |
| `OPENBOX_SERVER` | API URL (default `http://127.0.0.1:8443`) |
| `openbox` CLI | Built from `./cmd/openbox` |

## Quick run

```sh
export OPENBOX_TOKEN='…'
./run.sh
```

## What the script does

1. **Create** a sandbox with a short initial TTL (`--lifetime 5m`).
2. **Exec** a first command and inspect `expires_at` via `openbox inspect`.
3. **Extend** TTL by 30 minutes (`openbox sandbox extend --by 30m`).
4. **Exec** again to show the session is still usable.
5. **Delete** the sandbox.

Extend is synchronous (no operation). Total lifetime cannot exceed 24 hours from
`created_at`.

## Manual steps

```sh
openbox new session-1 --kind sandbox --lifetime 5m --idempotency-key create-session-1
openbox operation watch OPERATION_ID

openbox sandbox exec INSTANCE_ID -- date -u
openbox inspect INSTANCE_ID

openbox sandbox extend INSTANCE_ID --by 30m
openbox inspect INSTANCE_ID

openbox sandbox exec INSTANCE_ID -- echo still-running

openbox rm INSTANCE_ID --idempotency-key delete-session-1
```

## Without a live host

No offline runner. TTL extension and exec are exercised only against a running
daemon.

## Security notes

- Restricted egress applies by default; do not assume outbound internet from the
  guest unless you attach a different profile deliberately.
- Never embed tokens or SSH private keys in scripts or agent prompts.
