#!/usr/bin/env bash
# Durable session: create → extend TTL → exec loop → cleanup.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=../_common.sh
source "$ROOT/_common.sh"

NAME="session-$(date +%s)"
INSTANCE_ID=""

cleanup() {
	if [[ -n "$INSTANCE_ID" ]]; then
		DELETE_JSON="$(openbox rm "$INSTANCE_ID" --idempotency-key "delete-$NAME" --json 2>/dev/null || true)"
		if [[ -n "$DELETE_JSON" ]]; then
			OP_ID="$(echo "$DELETE_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['operation']['id'])" 2>/dev/null || true)"
			if [[ -n "$OP_ID" ]]; then
				wait_operation "$OP_ID" 2>/dev/null || true
			fi
		fi
	fi
}
trap cleanup EXIT

require_openbox

echo "==> create sandbox with short TTL"
CREATE_JSON="$(openbox new "$NAME" --kind sandbox --lifetime 5m --idempotency-key "create-$NAME" --json)"
INSTANCE_ID="$(echo "$CREATE_JSON" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['instance']['id'])")"
wait_operation "$(echo "$CREATE_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['operation']['id'])")"

echo "==> first exec"
openbox sandbox exec "$INSTANCE_ID" -- date -u
echo "==> expires before extend:"
openbox inspect "$INSTANCE_ID" | grep -i '^Expires:'

echo "==> extend TTL by 30m"
openbox sandbox extend "$INSTANCE_ID" --by 30m
echo "==> expires after extend:"
openbox inspect "$INSTANCE_ID" | grep -i '^Expires:'

echo "==> second exec (session still alive)"
openbox sandbox exec "$INSTANCE_ID" -- echo still-running

echo "==> delete sandbox"
DELETE_JSON="$(openbox rm "$INSTANCE_ID" --idempotency-key "delete-$NAME" --json)"
wait_operation "$(echo "$DELETE_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['operation']['id'])")"
INSTANCE_ID=""
trap - EXIT

echo "done"
