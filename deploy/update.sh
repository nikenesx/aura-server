#!/usr/bin/env bash
set -euo pipefail

# Runs on the VPS after sources have been synced. Builds the binary and
# restarts systemd. Invoked by scripts/deploy.sh; can also be run by hand:
#   sudo bash /opt/aura-server/deploy/update.sh

APP_DIR="${APP_DIR:-/opt/aura-server}"
APP_USER="${APP_USER:-aura}"
SERVICE="${DEPLOY_SERVICE:-aura-server}"

export PATH="/usr/local/go/bin:/usr/local/bin:${PATH}"

if ! command -v go >/dev/null 2>&1; then
	echo "Go is not installed. Run deploy/install.sh first." >&2
	exit 1
fi

cd "${APP_DIR}"
mkdir -p bin

echo "==> go build"
CGO_ENABLED=0 go build -trimpath -o bin/api ./cmd/api
if id -u "${APP_USER}" >/dev/null 2>&1; then
	chown "root:${APP_USER}" bin/api
fi
chmod 755 bin/api
if [[ -f .env ]]; then
	chmod 640 .env
	if id -u "${APP_USER}" >/dev/null 2>&1; then
		chown "root:${APP_USER}" .env
	fi
fi

if ! systemctl cat "${SERVICE}" >/dev/null 2>&1; then
	echo "systemd unit ${SERVICE} is missing. Run deploy/install.sh first." >&2
	exit 1
fi

echo "==> systemctl restart ${SERVICE}"
systemctl restart "${SERVICE}"
systemctl --no-pager --full status "${SERVICE}" | sed -n '1,18p'

echo "==> Health check on 127.0.0.1:8080"
ok=0
for _ in 1 2 3 4 5 6 7 8; do
	if curl -fsS http://127.0.0.1:8080/health; then
		echo
		ok=1
		break
	fi
	sleep 1
done
if [[ "${ok}" -ne 1 ]]; then
	echo "Service did not become healthy on 127.0.0.1:8080" >&2
	journalctl -u "${SERVICE}" -n 40 --no-pager >&2
	exit 1
fi
