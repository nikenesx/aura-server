#!/usr/bin/env bash
set -euo pipefail

# Sync sources to the VPS, rebuild there, restart systemd.
#
# Once:
#   cp deploy/deploy.env.example deploy.env
#   ssh-copy-id "${DEPLOY_USER}@${DEPLOY_HOST}"
#
# Then, after every code change:
#   ./scripts/deploy.sh
#   make deploy

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

if [[ -f "${ROOT}/deploy.env" ]]; then
	set -a
	# shellcheck disable=SC1091
	source "${ROOT}/deploy.env"
	set +a
fi

: "${DEPLOY_HOST:?Set DEPLOY_HOST in deploy.env (copy deploy/deploy.env.example)}"

DEPLOY_USER="${DEPLOY_USER:-root}"
DEPLOY_DIR="${DEPLOY_DIR:-/opt/aura-server}"
DEPLOY_SERVICE="${DEPLOY_SERVICE:-aura-server}"
HEALTH_URL="${HEALTH_URL:-https://api.aurameds.ru/health}"
REMOTE="${DEPLOY_USER}@${DEPLOY_HOST}"

for cmd in ssh rsync; do
	if ! command -v "${cmd}" >/dev/null 2>&1; then
		echo "Missing required command: ${cmd}" >&2
		exit 1
	fi
done

echo "==> Syncing sources to ${REMOTE}:${DEPLOY_DIR}"
ssh "${REMOTE}" "mkdir -p '${DEPLOY_DIR}'"
rsync -az --delete \
	--filter 'P .env' \
	--filter 'P bin/' \
	--exclude '.git/' \
	--exclude 'bin/' \
	--exclude 'dist/' \
	--exclude '.idea/' \
	--exclude '.env' \
	--exclude 'deploy.env' \
	--exclude '*.exe' \
	"${ROOT}/" "${REMOTE}:${DEPLOY_DIR}/"

remote_sudo=""
if [[ "${DEPLOY_USER}" != "root" ]]; then
	remote_sudo="sudo "
fi

echo "==> Ensuring the server is bootstrapped"
ssh "${REMOTE}" bash -s <<EOF
set -euo pipefail
need_install=0
if ! command -v docker >/dev/null 2>&1; then
	need_install=1
fi
if ! docker compose version >/dev/null 2>&1 && ! command -v docker-compose >/dev/null 2>&1; then
	need_install=1
fi
if [[ "\${need_install}" -eq 1 ]]; then
	echo "Installing Docker, nginx, and systemd unit"
	${remote_sudo}bash "${DEPLOY_DIR}/deploy/install.sh"
fi
EOF

echo "==> Building on the server and restarting ${DEPLOY_SERVICE}"
ssh "${REMOTE}" "${remote_sudo}bash '${DEPLOY_DIR}/deploy/update.sh'"

public_ok=0
echo "==> Public health check"
if curl -fsS --retry 2 --retry-delay 1 "${HEALTH_URL}"; then
	echo
	public_ok=1
else
	http_url="${HEALTH_URL/https:/http:}"
	if [[ "${http_url}" != "${HEALTH_URL}" ]] && curl -fsS --retry 2 --retry-delay 1 "${http_url}"; then
		echo
		echo "App is up over HTTP. Issue TLS on the server:"
		echo "  ssh ${REMOTE} 'certbot --nginx -d api.aurameds.ru'"
		public_ok=1
	fi
fi

if [[ "${public_ok}" -ne 1 ]]; then
	echo
	echo "The process is healthy on the server, but ${HEALTH_URL} failed." >&2
	echo "Check DNS A-record for api.aurameds.ru, nginx, and certbot." >&2
	exit 1
fi

echo "Deploy complete."
