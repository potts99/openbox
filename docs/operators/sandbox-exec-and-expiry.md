# Sandbox exec and expiry

OpenBox Sandboxes are disposable instances with a required TTL. Agents and
automation create them, run argv commands over the API, extend lifetime when
needed, and rely on durable expiry cleanup.

## Defaults

| Policy | Value |
|---|---|
| Default TTL | 1 hour |
| Maximum TTL from create time | 24 hours |
| Default resources | 2 vCPU, 2 GiB RAM, 10 GiB disk |
| Default image | curated `openbox:sandbox/ubuntu/24.04` |
| Default isolation | `standard` (container; warm pool) |
| Warm pool | Hybrid stopped + running clones from golden snapshot when `--storage-pool` is set |
| Egress label (v0.1) | `default` (network profiles arrive in slice 14) |
| Concurrent execs per instance | 2 |
| Output rate limit | 1 MiB framed stdout/stderr per second |

VPS and Devbox kinds do not receive an implicit expiry.

## Exec API

`POST /v1/instances/{id}/exec` accepts an argv array (never a shell string) and
streams `application/x-ndjson` frames:

- `stdout` / `stderr` — base64 payloads
- `exit` — process exit code
- `error` — policy/timeout/cancel failures

Output is never written to SQLite. Large streams are chunked under the frame
size limit and subject to the per-second byte budget.

CLI:

```sh
openbox sandbox exec INSTANCE --workdir /workspace -- python -c 'print(1)'
openbox sandbox extend INSTANCE --by 30m
openbox inspect INSTANCE
```

`inspect` shows desired/observed state, isolation, egress label, expiry
countdown, and cleanup/error codes when present.

## Expiry

`expires_at` is stored in SQLite. The daemon sweeps due Sandboxes on the
reconcile interval:

1. Mark desired state `deleted` (observed stays `deleting` while the runtime exists).
2. Reconcile retries runtime deletion until Incus confirms removal.
3. Only then is the instance tombstoned.

TTL extension (`POST .../extend` or `openbox sandbox extend`) is atomic and
rejected after irreversible deletion begins.

## Strong isolation

`requested_isolation=strong` still requires KVM. When unavailable, create fails
before an instance record is written (unchanged from the VM lifecycle policy).
