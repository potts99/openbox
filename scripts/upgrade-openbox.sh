#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Upgrade OpenBox binaries and roll them back if the restarted daemon fails its
# loopback health gate.

set -euo pipefail

OPENBOXD_SOURCE=""
OPENBOX_SOURCE=""
OPENBOXD_TARGET=/usr/local/bin/openboxd
OPENBOX_TARGET=/usr/local/bin/openbox
SERVICE=openboxd
HEALTH_URL=http://127.0.0.1:8443/v1/health
HEALTH_ATTEMPTS=15
HEALTH_INTERVAL=2
BACKUP_ROOT=/var/lib/openbox/upgrades

usage() {
	cat <<'EOF'
Usage: sudo scripts/upgrade-openbox.sh --openboxd PATH --openbox PATH [OPTIONS]

Swap OpenBox binaries, restart openboxd, and require its loopback health
endpoint to return HTTP 200. On failure, restore the prior binaries and restart
the service. The old binaries are retained under /var/lib/openbox/upgrades.

Options:
  --openboxd PATH       New openboxd binary (required)
  --openbox PATH        New openbox CLI binary (required)
  --service NAME        systemd service (default: openboxd)
  --health-url URL      Health endpoint (default: http://127.0.0.1:8443/v1/health)
  --health-attempts N   Number of health attempts (default: 15)
  --health-interval S   Seconds between attempts (default: 2)
  --backup-root PATH    Retained previous binaries directory
  -h, --help            Show this help

Example:
  sudo scripts/upgrade-openbox.sh --openboxd /tmp/openboxd-linux --openbox /tmp/openbox-linux
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--openboxd) OPENBOXD_SOURCE=${2:?--openboxd requires a value}; shift 2 ;;
	--openbox) OPENBOX_SOURCE=${2:?--openbox requires a value}; shift 2 ;;
	--service) SERVICE=${2:?--service requires a value}; shift 2 ;;
	--health-url) HEALTH_URL=${2:?--health-url requires a value}; shift 2 ;;
	--health-attempts) HEALTH_ATTEMPTS=${2:?--health-attempts requires a value}; shift 2 ;;
	--health-interval) HEALTH_INTERVAL=${2:?--health-interval requires a value}; shift 2 ;;
	--backup-root) BACKUP_ROOT=${2:?--backup-root requires a value}; shift 2 ;;
	-h|--help) usage; exit 0 ;;
	*) echo "upgrade-openbox.sh: unknown option: $1" >&2; usage >&2; exit 2 ;;
	esac
done

if [[ $(id -u) -ne 0 ]]; then
	echo "upgrade-openbox.sh: run as root" >&2
	exit 1
fi
if [[ -z "$OPENBOXD_SOURCE" || ! -f "$OPENBOXD_SOURCE" || -z "$OPENBOX_SOURCE" || ! -f "$OPENBOX_SOURCE" ]]; then
	echo "upgrade-openbox.sh: --openboxd and --openbox must name existing files" >&2
	exit 2
fi
if ! [[ "$HEALTH_ATTEMPTS" =~ ^[1-9][0-9]*$ ]] || ! [[ "$HEALTH_INTERVAL" =~ ^[0-9]+$ ]]; then
	echo "upgrade-openbox.sh: health attempts must be positive and interval must be non-negative integers" >&2
	exit 2
fi
if [[ ! -f "$OPENBOXD_TARGET" || ! -f "$OPENBOX_TARGET" ]]; then
	echo "upgrade-openbox.sh: expected installed binaries at $OPENBOXD_TARGET and $OPENBOX_TARGET" >&2
	exit 1
fi

health_gate() {
	local attempt
	for ((attempt = 1; attempt <= HEALTH_ATTEMPTS; attempt++)); do
		if curl --fail --silent --show-error \
			-H "X-OpenBox-API-Version: v1" \
			"$HEALTH_URL" >/dev/null; then
			return 0
		fi
		if (( attempt < HEALTH_ATTEMPTS )); then
			sleep "$HEALTH_INTERVAL"
		fi
	done
	echo "upgrade-openbox.sh: health gate failed after ${HEALTH_ATTEMPTS} attempts: $HEALTH_URL" >&2
	return 1
}

install -d -m 0700 "$BACKUP_ROOT"
BACKUP_DIR=$(mktemp -d "$BACKUP_ROOT/openbox-upgrade.XXXXXXXX")
OPENBOXD_PREVIOUS=$BACKUP_DIR/openboxd
OPENBOX_PREVIOUS=$BACKUP_DIR/openbox
cp -a "$OPENBOXD_TARGET" "$OPENBOXD_PREVIOUS"
cp -a "$OPENBOX_TARGET" "$OPENBOX_PREVIOUS"

swapped=0
rollback() {
	local status=$?
	trap - ERR
	if [[ "$swapped" -eq 1 ]]; then
		echo "upgrade-openbox.sh: restoring prior binaries from $BACKUP_DIR" >&2
		install -m 0755 "$OPENBOXD_PREVIOUS" "$OPENBOXD_TARGET"
		install -m 0755 "$OPENBOX_PREVIOUS" "$OPENBOX_TARGET"
		systemctl restart "$SERVICE" || true
	fi
	exit "$status"
}
trap rollback ERR

swapped=1
install -m 0755 "$OPENBOXD_SOURCE" "$OPENBOXD_TARGET"
install -m 0755 "$OPENBOX_SOURCE" "$OPENBOX_TARGET"
systemctl restart "$SERVICE"
health_gate
trap - ERR

echo "upgrade-openbox.sh: upgraded $SERVICE successfully"
echo "upgrade-openbox.sh: previous binaries retained at $BACKUP_DIR"
