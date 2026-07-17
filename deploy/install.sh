#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Idempotent OpenBox host install: directories, binaries, systemd unit, enable.
#
# Usage:
#   sudo deploy/install.sh [--restart] [--openboxd PATH] [--openbox PATH]
#   sudo deploy/install.sh [--restart] /tmp/openboxd-linux /tmp/openbox-linux
#
# Binaries default to the first existing path among:
#   /tmp/openboxd-linux, /tmp/openboxd, /tmp/openbox-linux, /tmp/openbox

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

OPENBOXD_BIN=""
OPENBOX_BIN=""
RESTART=0
PREFIX=/usr/local
STATE_DIR=/var/lib/openbox
ENV_FILE=/etc/openbox/openboxd.env

usage() {
	cat <<'EOF'
Usage: deploy/install.sh [OPTIONS] [OPENBOXD_BIN] [OPENBOX_BIN]

Install openboxd and openbox, create state directories, and enable the
systemd unit. Does not restart a running openboxd unless --restart is set.

Options:
  --restart          Restart openboxd after installing binaries and unit
  --openboxd PATH    openboxd binary to install (default: search /tmp)
  --openbox PATH     openbox CLI binary to install (default: search /tmp)
  -h, --help         Show this help

After install, configure Caddy using deploy/caddy/ and point it at
/var/lib/openbox/caddy/routes.caddyfile. See docs/operators/install.md.
EOF
}

first_existing() {
	for candidate in "$@"; do
		if [[ -n "$candidate" && -f "$candidate" ]]; then
			printf '%s\n' "$candidate"
			return 0
		fi
	done
	return 1
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--restart)
		RESTART=1
		shift
		;;
	--openboxd)
		OPENBOXD_BIN=$2
		shift 2
		;;
	--openbox)
		OPENBOX_BIN=$2
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	-*)
		echo "install.sh: unknown option: $1" >&2
		usage >&2
		exit 1
		;;
	*)
		if [[ -z "$OPENBOXD_BIN" ]]; then
			OPENBOXD_BIN=$1
		elif [[ -z "$OPENBOX_BIN" ]]; then
			OPENBOX_BIN=$1
		else
			echo "install.sh: unexpected argument: $1" >&2
			exit 1
		fi
		shift
		;;
	esac
done

if [[ -z "$OPENBOXD_BIN" ]]; then
	OPENBOXD_BIN=$(first_existing /tmp/openboxd-linux /tmp/openboxd) || true
fi
if [[ -z "$OPENBOX_BIN" ]]; then
	OPENBOX_BIN=$(first_existing /tmp/openbox-linux /tmp/openbox) || true
fi

if [[ $(id -u) -ne 0 ]]; then
	echo "install.sh: run as root (sudo deploy/install.sh)" >&2
	exit 1
fi

if [[ -z "$OPENBOXD_BIN" || ! -f "$OPENBOXD_BIN" ]]; then
	echo "install.sh: openboxd binary not found; pass --openboxd PATH or place a build at /tmp/openboxd-linux" >&2
	exit 1
fi
if [[ -z "$OPENBOX_BIN" || ! -f "$OPENBOX_BIN" ]]; then
	echo "install.sh: openbox binary not found; pass --openbox PATH or place a build at /tmp/openbox-linux" >&2
	exit 1
fi

install -d -m 0755 "$STATE_DIR" "$STATE_DIR/ssh" "$STATE_DIR/caddy" "$STATE_DIR/artifacts"
install -d -m 0755 /etc/openbox

ROUTES_FILE="$STATE_DIR/caddy/routes.caddyfile"
if [[ ! -f "$ROUTES_FILE" ]]; then
	install -m 0644 "$SCRIPT_DIR/caddy/routes.caddyfile" "$ROUTES_FILE"
fi

if [[ ! -f "$ENV_FILE" ]]; then
	install -m 0644 "$SCRIPT_DIR/openboxd.env.example" "$ENV_FILE"
	echo "install.sh: created $ENV_FILE from template (edit before production use)"
fi

install -m 0755 "$OPENBOXD_BIN" "$PREFIX/bin/openboxd"
install -m 0755 "$OPENBOX_BIN" "$PREFIX/bin/openbox"
install -m 0644 "$SCRIPT_DIR/systemd/openboxd.service" /etc/systemd/system/openboxd.service

systemctl daemon-reload
systemctl enable openboxd.service

was_active=0
if systemctl is-active --quiet openboxd.service; then
	was_active=1
fi

if [[ "$RESTART" -eq 1 ]]; then
	systemctl restart openboxd.service
	echo "install.sh: restarted openboxd"
elif [[ "$was_active" -eq 1 ]]; then
	echo "install.sh: openboxd is already running; re-run with --restart to load new binaries"
else
	systemctl start openboxd.service
	echo "install.sh: started openboxd"
fi

cat <<EOF

OpenBox install complete.

Next steps:
  1. Ensure Incus is running and OPENBOX_STORAGE_POOL in $ENV_FILE exists.
  2. Check health: curl -sS -H "X-OpenBox-API-Version: v1" http://127.0.0.1:8443/v1/health
  3. Complete owner bootstrap (one-time secret in journal): journalctl -u openboxd -b | grep bootstrap
  4. Configure Caddy for HTTPS:
       - copy deploy/caddy/Caddyfile to your Caddy config directory
       - add: import $ROUTES_FILE
       - set on_demand_tls ask http://127.0.0.1:8443/v1/certificates/allow
     See deploy/caddy/README.md and docs/operators/https-routes.md

Full walkthrough: docs/operators/install.md
EOF
