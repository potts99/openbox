#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Bake a fast OpenBox Incus VM image from an existing instance (typically
# obx-pool-golden): openssh already present, generic DHCP, no agent templates
# that force a first-boot reboot.
#
# Usage:
#   scripts/bake-openbox-vm-image.sh [--project openbox] [--source obx-pool-golden] [--alias openbox-vm-fast]
#
# After import, retarget catalog aliases via the Incus API, e.g.:
#   incus query -X DELETE /1.0/images/aliases/openbox%3Asandbox%2Fubuntu%2F24.04%2Fvm
#   incus query -X POST /1.0/images/aliases -d '{"name":"openbox:sandbox/ubuntu/24.04/vm","target":"<fp>"}'

set -euo pipefail

PROJECT=openbox
SOURCE=obx-pool-golden
ALIAS=openbox-vm-fast
WORKDIR=
KEEP_WORKDIR=0

while [[ $# -gt 0 ]]; do
	case "$1" in
	--project)
		PROJECT=$2
		shift 2
		;;
	--source)
		SOURCE=$2
		shift 2
		;;
	--alias)
		ALIAS=$2
		shift 2
		;;
	--workdir)
		WORKDIR=$2
		KEEP_WORKDIR=1
		shift 2
		;;
	-h | --help)
		sed -n '2,16p' "$0"
		exit 0
		;;
	*)
		echo "unknown flag: $1" >&2
		exit 2
		;;
	esac
done

if [[ -z "$WORKDIR" ]]; then
	WORKDIR=$(mktemp -d /tmp/openbox-vm-bake.XXXXXX)
fi
cleanup() {
	if [[ "$KEEP_WORKDIR" -eq 0 ]]; then
		rm -rf "$WORKDIR"
	fi
}
trap cleanup EXIT

BAKE="obx-vm-bake-$$"
RAW_ALIAS="${ALIAS}-raw"

echo "copying ${SOURCE} -> ${BAKE} (project=${PROJECT})"
incus delete -f "$BAKE" --project "$PROJECT" >/dev/null 2>&1 || true
incus copy "$SOURCE" "$BAKE" --project "$PROJECT"
incus config device override "$BAKE" eth0 security.acls=openbox-default-deny,openbox-egress-standard --project "$PROJECT" 2>/dev/null || true
incus start "$BAKE" --project "$PROJECT"

echo "waiting for agent..."
for _ in $(seq 1 120); do
	if incus exec "$BAKE" --project "$PROJECT" -- /bin/true >/dev/null 2>&1; then
		break
	fi
	sleep 1
done
if ! incus exec "$BAKE" --project "$PROJECT" -- /bin/true >/dev/null 2>&1; then
	echo "agent never became ready" >&2
	incus delete -f "$BAKE" --project "$PROJECT" || true
	exit 1
fi

incus exec "$BAKE" --project "$PROJECT" -- bash -s <<'GUEST'
set -euo pipefail
cat >/etc/netplan/50-cloud-init.yaml <<'YAML'
network:
  version: 2
  ethernets:
    enp5s0:
      dhcp4: true
      dhcp6: false
YAML
chmod 600 /etc/netplan/50-cloud-init.yaml
netplan apply || true
cloud-init clean --logs --seed || true
rm -rf /var/lib/cloud/instances /var/lib/cloud/instance /var/lib/cloud/data /var/lib/cloud/sem
mkdir -p /var/lib/cloud/seed/nocloud-net
truncate -s 0 /etc/machine-id
rm -f /var/lib/dbus/machine-id /root/.ssh/authorized_keys
hostnamectl set-hostname ubuntu || true
echo ubuntu >/etc/hostname
systemctl enable ssh >/dev/null
truncate -s 0 /var/log/wtmp /var/log/btmp 2>/dev/null || true
GUEST

incus stop "$BAKE" --project "$PROJECT"
for _ in $(seq 1 60); do
	[[ "$(incus list "$BAKE" --project "$PROJECT" --format csv -c s)" == "STOPPED" ]] && break
	sleep 1
done

incus image delete "$RAW_ALIAS" >/dev/null 2>&1 || true
incus publish "$BAKE" --project "$PROJECT" --alias "$RAW_ALIAS" --reuse \
	description="OpenBox fast VM bake (openssh, generic dhcp)"
incus delete -f "$BAKE" --project "$PROJECT"

FP=$(incus image list "$RAW_ALIAS" --format csv -c f | head -1)
echo "exported raw fingerprint ${FP}"
incus image export "$FP" "$WORKDIR/raw"
mkdir -p "$WORKDIR/unpack"
tar -xzf "$WORKDIR/raw.tar.gz" -C "$WORKDIR/unpack"
rm -rf "$WORKDIR/unpack/templates"
python3 - <<PY
from pathlib import Path
p = Path("$WORKDIR/unpack/metadata.yaml")
text = p.read_text()
if "templates:" in text:
	text = text.split("templates:")[0].rstrip() + "\n"
lines = []
for line in text.splitlines():
	if line.strip().startswith("description:"):
		lines.append("  description: OpenBox fast Ubuntu VM (no agent-reboot templates)")
	else:
		lines.append(line)
p.write_text("\n".join(lines) + "\n")
PY
tar -czf "$WORKDIR/fast.tar.gz" -C "$WORKDIR/unpack" metadata.yaml rootfs.img

incus image delete "$ALIAS" >/dev/null 2>&1 || true
incus image import "$WORKDIR/fast.tar.gz" --alias "$ALIAS"
incus image delete "$RAW_ALIAS" >/dev/null 2>&1 || true
FAST=$(incus image list "$ALIAS" --format csv -c f | head -1)
echo "baked alias=${ALIAS} fingerprint=${FAST}"
echo "retarget openbox:*/ubuntu/24.04/vm aliases to this fingerprint via /1.0/images/aliases"
