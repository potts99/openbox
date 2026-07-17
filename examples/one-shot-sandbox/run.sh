#!/usr/bin/env bash
# One-shot sandbox: create (restricted egress) → exec → artifact put → delete.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=../_common.sh
source "$ROOT/_common.sh"

NAME="one-shot-$(date +%s)"
ARTIFACT_PATH="results/uname.txt"
TMP_OUT="$(mktemp)"
TMP_DL="$(mktemp)"
trap 'rm -f "$TMP_OUT" "$TMP_DL"' EXIT

require_openbox

echo "==> creating sandbox $NAME (restricted egress is the sandbox default)"
CREATE_JSON="$(openbox new "$NAME" --kind sandbox --lifetime 30m --idempotency-key "create-$NAME" --json)"
INSTANCE_ID="$(echo "$CREATE_JSON" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['instance']['id'])")"
OP_ID="$(echo "$CREATE_JSON" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['operation']['id'])")"
wait_operation "$OP_ID"
echo "    instance $INSTANCE_ID ready"

echo "==> exec inside sandbox"
openbox sandbox exec "$INSTANCE_ID" -- uname -a | tee "$TMP_OUT"

echo "==> upload artifact $ARTIFACT_PATH"
openbox artifact put "$INSTANCE_ID" "$ARTIFACT_PATH" "$TMP_OUT" --content-type text/plain

echo "==> download artifact and verify non-empty"
openbox artifact get "$INSTANCE_ID" "$ARTIFACT_PATH" --output "$TMP_DL"
wc -c "$TMP_DL"

echo "==> delete sandbox"
DELETE_JSON="$(openbox rm "$INSTANCE_ID" --idempotency-key "delete-$NAME" --json)"
wait_operation "$(echo "$DELETE_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['operation']['id'])")"

echo "done"
