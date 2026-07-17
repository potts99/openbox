# Agent fan-out

**Requires a live `openboxd` host** with Incus runtime, owner API token, SSH
public key, and the `openbox` CLI.

Prepare a known-good **sandbox** environment, checkpoint it, fan out independent
workers from that checkpoint, then destroy them. Workers get **restricted egress**
(the sandbox default) — restore does not copy source egress profiles.

Disk-only checkpoints — no suspended-memory resume. Derived instances get
rewritten OpenBox-managed SSH access; rotate application credentials after fan-out
if the source may have retained guest secrets.

See [`docs/api/checkpoint-and-reuse.md`](../docs/api/checkpoint-and-reuse.md) and
[`docs/operators/checkpoint.md`](../docs/operators/checkpoint.md).

## Prerequisites

| Requirement | Notes |
|-------------|-------|
| Live host | Local or remote `openboxd` |
| `OPENBOX_TOKEN` | Owner bearer token |
| `OPENBOX_SERVER` | API URL (default `http://127.0.0.1:8443`) |
| SSH public key | `--ssh-key` path or `OPENBOX_SSH_PUBLIC_KEY` (restore requires a key) |
| `openbox` CLI | Built from `./cmd/openbox` |

## Quick run

```sh
export OPENBOX_TOKEN='…'
export OPENBOX_SSH_PUBLIC_KEY='ssh-ed25519 AAAA…'   # or --ssh-key in manual steps

./run.sh
```

## What the script does

1. **Prepare** — create a golden sandbox and exec a setup command.
2. **Checkpoint** — `openbox snapshot create golden ready`.
3. **Fan out** — restore `worker-a` and `worker-b` from the checkpoint.
4. **Work** — exec a trivial command in each worker sandbox.
5. **Destroy** — delete workers, checkpoint, and golden source.

Warnings and `storage_efficiency` are printed on restore. Copy-on-write is
claimed only when efficiency is `confirmed`.

## Manual steps

```sh
# 1) Prepare
openbox new golden --kind sandbox --lifetime 2h --idempotency-key create-golden
openbox operation watch OPERATION_ID
GOLDEN_ID=…

openbox sandbox exec "$GOLDEN_ID" -- bash -lc 'echo prepared > /tmp/ready'

# 2) Checkpoint
openbox snapshot create "$GOLDEN_ID" ready --idempotency-key snap-ready
openbox operation watch SNAPSHOT_OP_ID
SNAP_ID=$(openbox snapshot list "$GOLDEN_ID" --json | …)

# 3) Fan out (sandbox kind → egress-restricted default on each worker)
openbox restore "$SNAP_ID" worker-a --idempotency-key restore-a
openbox operation watch RESTORE_A_OP
openbox restore "$SNAP_ID" worker-b --idempotency-key restore-b
openbox operation watch RESTORE_B_OP

# 4) Work
openbox sandbox exec WORKER_A_ID -- cat /tmp/ready
openbox sandbox exec WORKER_B_ID -- cat /tmp/ready

# 5) Destroy
openbox rm WORKER_A_ID --idempotency-key del-a
openbox rm WORKER_B_ID --idempotency-key del-b
openbox snapshot delete "$SNAP_ID" --idempotency-key snap-del
openbox rm "$GOLDEN_ID" --idempotency-key del-golden
```

## Without a live host

The no-live-host acceptance suite exercises the same flow against the fake
runtime:

```sh
go test ./internal/acceptance/ -run TestCheckpointReuseFanOut
```

That test does not run real exec or SSH; use a live host to validate end-to-end.

## Security notes

- Sandboxes use **`egress-restricted`** by default; restored sandboxes re-bind
  sandbox defaults rather than copying the golden profile attachment history.
- Do not store credentials in the golden image or agent prompts.
