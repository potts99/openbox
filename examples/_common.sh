#!/usr/bin/env bash
# Shared helpers for OpenBox CLI examples. Source from example scripts; do not run directly.
set -euo pipefail

: "${OPENBOX_SERVER:=http://127.0.0.1:8443}"
: "${OPENBOX_TOKEN:?set OPENBOX_TOKEN to an owner API token}"

openbox() {
	command openbox --server "$OPENBOX_SERVER" --token "$OPENBOX_TOKEN" "$@"
}

require_openbox() {
	if ! command -v openbox >/dev/null 2>&1; then
		echo "openbox CLI not found; build with: go build -o openbox ./cmd/openbox" >&2
		exit 1
	fi
	openbox doctor >/dev/null
}

# Stream operation progress, then verify terminal status succeeded.
wait_operation() {
	local op_id="$1"
	openbox operation watch "$op_id" >/dev/null
	OPENBOX_SERVER="$OPENBOX_SERVER" OPENBOX_TOKEN="$OPENBOX_TOKEN" python3 - <<'PY' "$op_id"
import json, os, sys, urllib.request

op_id = sys.argv[1]
server = os.environ["OPENBOX_SERVER"].rstrip("/")
token = os.environ["OPENBOX_TOKEN"]
req = urllib.request.Request(
    f"{server}/v1/operations/{op_id}",
    headers={
        "Authorization": f"Bearer {token}",
        "X-OpenBox-API-Version": "v1",
        "Accept": "application/json",
    },
)
with urllib.request.urlopen(req) as resp:
    op = json.load(resp)
status = op.get("status", "")
if status == "failed":
    code = op.get("error_code") or "unknown"
    print(f"operation {op_id} failed: {code}", file=sys.stderr)
    sys.exit(1)
if status != "succeeded":
    print(f"operation {op_id} ended in unexpected state: {status}", file=sys.stderr)
    sys.exit(1)
PY
}

instance_id_for_name() {
	local name="$1"
	openbox ls --json | python3 -c "
import json, sys
name = sys.argv[1]
data = json.load(sys.stdin)
for inst in data.get('instances', []):
    if inst.get('name') == name:
        print(inst['id'])
        break
else:
    print(f'instance {name!r} not found', file=sys.stderr)
    sys.exit(1)
" "$name"
}

snapshot_id_for_name() {
	local instance_id="$1"
	local snap_name="$2"
	openbox snapshot list "$instance_id" --json | python3 -c "
import json, sys
name = sys.argv[1]
data = json.load(sys.stdin)
for snap in data.get('snapshots', []):
    if snap.get('name') == name:
        print(snap['id'])
        break
else:
    print(f'snapshot {name!r} not found', file=sys.stderr)
    sys.exit(1)
" "$snap_name"
}
