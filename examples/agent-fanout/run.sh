#!/usr/bin/env bash
# Agent fan-out: prepare sandbox → snapshot → restore workers → destroy.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=../_common.sh
source "$ROOT/_common.sh"

STAMP="$(date +%s)"
GOLDEN="golden-$STAMP"
WORKER_A="worker-a-$STAMP"
WORKER_B="worker-b-$STAMP"
SNAP_NAME="ready"
GOLDEN_ID=""
WORKER_A_ID=""
WORKER_B_ID=""
SNAP_ID=""

cleanup() {
	for id in "$WORKER_A_ID" "$WORKER_B_ID"; do
		[[ -n "$id" ]] && openbox rm "$id" --idempotency-key "del-$id" --json >/dev/null 2>&1 || true
	done
	[[ -n "$SNAP_ID" ]] && openbox snapshot delete "$SNAP_ID" --idempotency-key "snap-del-$STAMP" --json >/dev/null 2>&1 || true
	[[ -n "$GOLDEN_ID" ]] && openbox rm "$GOLDEN_ID" --idempotency-key "del-golden-$STAMP" --json >/dev/null 2>&1 || true
}
trap cleanup EXIT

require_openbox

echo "==> create golden sandbox (kind sandbox → restricted egress default)"
CREATE_JSON="$(openbox new "$GOLDEN" --kind sandbox --lifetime 2h --idempotency-key "create-$GOLDEN" --json)"
GOLDEN_ID="$(echo "$CREATE_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['instance']['id'])")"
wait_operation "$(echo "$CREATE_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['operation']['id'])")"

echo "==> prepare: write marker inside golden"
openbox sandbox exec "$GOLDEN_ID" -- bash -lc 'echo prepared > /tmp/ready && cat /tmp/ready'

echo "==> checkpoint"
SNAP_JSON="$(openbox snapshot create "$GOLDEN_ID" "$SNAP_NAME" --idempotency-key "snap-$STAMP" --json)"
wait_operation "$(echo "$SNAP_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['operation']['id'])")"
SNAP_ID="$(snapshot_id_for_name "$GOLDEN_ID" "$SNAP_NAME")"
echo "    snapshot $SNAP_ID"

restore_worker() {
	local name="$1"
	local key="$2"
	local json
	json="$(openbox restore "$SNAP_ID" "$name" --idempotency-key "$key" --json)"
	wait_operation "$(echo "$json" | python3 -c "import json,sys; print(json.load(sys.stdin)['operation']['id'])")"
	echo "$json" | python3 -c "import json,sys; print(json.load(sys.stdin)['instance']['id'])"
}

echo "==> fan out worker-a"
WORKER_A_ID="$(restore_worker "$WORKER_A" "restore-a-$STAMP")"
echo "    worker-a $WORKER_A_ID"

echo "==> fan out worker-b"
WORKER_B_ID="$(restore_worker "$WORKER_B" "restore-b-$STAMP")"
echo "    worker-b $WORKER_B_ID"

echo "==> exec on workers"
openbox sandbox exec "$WORKER_A_ID" -- cat /tmp/ready
openbox sandbox exec "$WORKER_B_ID" -- cat /tmp/ready

echo "==> destroy workers, snapshot, golden"
for id in "$WORKER_A_ID" "$WORKER_B_ID"; do
	DELETE_JSON="$(openbox rm "$id" --idempotency-key "del-$id" --json)"
	wait_operation "$(echo "$DELETE_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['operation']['id'])")"
done
WORKER_A_ID=""
WORKER_B_ID=""

DELETE_SNAP_JSON="$(openbox snapshot delete "$SNAP_ID" --idempotency-key "snap-del-$STAMP" --json)"
wait_operation "$(echo "$DELETE_SNAP_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['id'])")"
SNAP_ID=""

DELETE_GOLDEN_JSON="$(openbox rm "$GOLDEN_ID" --idempotency-key "del-golden-$STAMP" --json)"
wait_operation "$(echo "$DELETE_GOLDEN_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['operation']['id'])")"
GOLDEN_ID=""
trap - EXIT

echo "done"
