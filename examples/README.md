# OpenBox agent examples

Runnable recipes for agent-style workflows against a live **`openboxd`** host.
These live under repository-root `examples/` (tracked in git). Semantics are
defined in [`docs/api/`](../docs/api/).

**Security:** sandboxes default to **`egress-restricted`** (public internet
denied). Never put API tokens, webhook secrets, or guest credentials in prompts
or checked-in files — use `OPENBOX_TOKEN` / `OPENBOX_SERVER` at runtime only.
OpenBox does not inject secrets into guests in Phase 4.

| Example | Live host? | Summary |
|---------|------------|---------|
| [one-shot-sandbox](one-shot-sandbox/) | **Yes** | Restricted sandbox → exec → artifact upload → delete |
| [durable-session](durable-session/) | **Yes** | Create → extend TTL → exec loop → cleanup |
| [agent-fanout](agent-fanout/) | **Yes** (acceptance test offline) | Sandbox prepare → snapshot → restore fan-out → destroy |
| [agent-server](agent-server/) | **Yes** for run; compile-only offline | Go SDK create/exec/cleanup + webhook receiver stub |

## Prerequisites (all live-host examples)

```sh
# Build CLI once from repo root
go build -o openbox ./cmd/openbox

export OPENBOX_TOKEN='your-owner-token'
export OPENBOX_SERVER='http://127.0.0.1:8443'   # optional; default shown
```

Optional for restore/clone flows:

```sh
export OPENBOX_SSH_PUBLIC_KEY='ssh-ed25519 AAAA…'
```

Run `openbox doctor` (or each example's `run.sh`, which checks health) before
starting.

## Shared helpers

[`_common.sh`](_common.sh) is sourced by shell examples for env wiring and
operation waits. Do not commit secrets into it.

## Related docs

- [`docs/api/sandbox-lifecycle.md`](../docs/api/sandbox-lifecycle.md) — TTL, exec, isolation
- [`docs/api/artifacts.md`](../docs/api/artifacts.md) — instance-attached blobs
- [`docs/api/checkpoint-and-reuse.md`](../docs/api/checkpoint-and-reuse.md) — snapshots and restore
- [`docs/api/agent-safety-controls.md`](../docs/api/agent-safety-controls.md) — egress profiles
- [`docs/api/go-sdk.md`](../docs/api/go-sdk.md) — `pkg/openbox` surface
- [`docs/api/webhooks.md`](../docs/api/webhooks.md) — signed delivery (SDK pending)
- [`docs/plans/04-agent-developer-experience.md`](../docs/plans/04-agent-developer-experience.md) — Phase 4 plan

## Without a live host

- **agent-fanout:** `go test ./internal/acceptance/ -run TestCheckpointReuseFanOut`
- **agent-server:** `go build -o /dev/null ./examples/agent-server/`
- **one-shot-sandbox / durable-session:** no offline runner; require `openboxd`
