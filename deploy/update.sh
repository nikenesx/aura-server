#!/usr/bin/env bash
set -euo pipefail

# Rebuild Docker images on the VPS and restart compose.
#   sudo bash /opt/aura-server/deploy/update.sh

APP_DIR="${APP_DIR:-/opt/aura-server}"
APP_USER="${APP_USER:-aura}"
SERVICE="${DEPLOY_SERVICE:-aura-server}"

cd "${APP_DIR}"

compose() {
	if docker compose version >/dev/null 2>&1; then
		docker compose "$@"
	elif command -v docker-compose >/dev/null 2>&1; then
		docker-compose "$@"
	else
		echo "Docker Compose is not installed. Installing..." >&2
		bash "${APP_DIR}/deploy/install.sh"
		docker compose "$@"
	fi
}

if [[ ! -f docker-compose.yml ]]; then
	echo "docker-compose.yml is missing in ${APP_DIR}" >&2
	exit 1
fi

if [[ -f .env ]]; then
	chmod 640 .env
	if id -u "${APP_USER}" >/dev/null 2>&1; then
		chown "root:${APP_USER}" .env
	fi
fi

if [[ -f deploy/aura-server.service ]]; then
	echo "==> Refreshing systemd unit"
	cp deploy/aura-server.service /etc/systemd/system/aura-server.service
	systemctl daemon-reload
	systemctl enable aura-server
fi

echo "==> docker compose build"
compose build

if systemctl cat "${SERVICE}" >/dev/null 2>&1; then
	echo "==> systemctl restart ${SERVICE}"
	systemctl daemon-reload
	systemctl restart "${SERVICE}"
else
	echo "==> docker compose up"
	compose up -d --remove-orphans
fi

echo "==> Health check on 127.0.0.1:8080"
ok=0
for _ in $(seq 1 60); do
	if curl -fsS http://127.0.0.1:8080/health; then
		echo
		ok=1
		break
	fi
	sleep 5
done
if [[ "${ok}" -ne 1 ]]; then
	echo "API did not become healthy on 127.0.0.1:8080" >&2
	compose ps >&2 || true
	compose logs --tail 80 >&2 || true
	exit 1
fi
