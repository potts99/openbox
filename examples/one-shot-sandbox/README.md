# One-shot sandbox

**Requires a live `openboxd` host** with Incus runtime, owner API token, and the
`openbox` CLI on your PATH.

Run a disposable sandbox with **restricted egress** (the default for
`kind: sandbox`), execute a command, upload results via the artifact store, and
delete the instance. No SSH/SCP and no credentials in prompts or repo files.

See [`docs/api/sandbox-lifecycle.md`](../docs/api/sandbox-lifecycle.md) and
[`docs/api/artifacts.md`](../docs/api/artifacts.md).

## Prerequisites

| Requirement | Notes |
|-------------|-------|
| Live host | Local `openboxd` or remote test host |
| `OPENBOX_TOKEN` | Owner bearer token (never commit) |
| `OPENBOX_SERVER` | API base URL (default `http://127.0.0.1:8443`) |
| `openbox` CLI | `go build -o openbox ./cmd/openbox` from repo root |

Sandboxes bind the system **`egress-restricted`** profile by default. Public
internet egress is denied; DNS and operator allowlists remain available per
[`docs/api/agent-safety-controls.md`](../docs/api/agent-safety-controls.md).

## Quick run

```sh
export OPENBOX_TOKEN='…'
export OPENBOX_SERVER='http://127.0.0.1:8443'   # optional

./run.sh
```

## What the script does

1. **Create** a sandbox (`openbox new … --kind sandbox --lifetime 30m`).
2. **Wait** for the durable create operation.
3. **Exec** `uname -a` inside the sandbox (`openbox sandbox exec`).
4. **Artifact put** — upload exec output to `results/uname.txt`.
5. **Artifact get** — download to a temp file and print a checksum line.
6. **Delete** the sandbox (`openbox rm`).

## Manual steps

```sh
openbox new job-1 --kind sandbox --lifetime 30m --idempotency-key create-job-1
openbox operation watch OPERATION_ID

openbox sandbox exec INSTANCE_ID -- uname -a
echo 'hello' > /tmp/out.txt
openbox artifact put INSTANCE_ID results/out.txt /tmp/out.txt

openbox artifact list INSTANCE_ID
openbox artifact get INSTANCE_ID results/out.txt --output /tmp/downloaded.txt

openbox rm INSTANCE_ID --idempotency-key delete-job-1
openbox operation watch DELETE_OP_ID
```

## Without a live host

There is no offline simulation for this flow. The no-live-host acceptance suite
covers related API semantics under `internal/acceptance/`, but artifact upload
and exec require a running daemon.

## Security notes

- Do **not** pass API tokens or guest secrets on the exec argv or in checked-in
  files. Use environment variables at runtime.
- OpenBox does **not** inject credentials into guests in Phase 4; see
  [`docs/security/secret-delivery.md`](../docs/security/secret-delivery.md).
