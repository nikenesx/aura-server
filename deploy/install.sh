#!/usr/bin/env bash
set -euo pipefail

# First-time VPS bootstrap. Run as root on the server:
#   sudo bash deploy/install.sh

if [[ "${EUID}" -ne 0 ]]; then
	echo "Run this script as root (sudo bash deploy/install.sh)" >&2
	exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOMAIN="${DOMAIN:-api.aurameds.ru}"
APP_USER="${APP_USER:-aura}"
APP_DIR="${APP_DIR:-/opt/aura-server}"

if [[ ! -f /etc/os-release ]] || ! grep -qiE 'debian|ubuntu' /etc/os-release; then
	echo "This installer targets Debian/Ubuntu." >&2
	exit 1
fi

echo "==> Installing packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends \
	ca-certificates curl rsync nginx certbot python3-certbot-nginx ufw docker.io
apt-get install -y docker-compose-v2 || apt-get install -y docker-compose-plugin || apt-get install -y docker-compose || true
systemctl enable --now docker

if ! docker compose version >/dev/null 2>&1; then
	echo "==> Installing Docker Compose plugin from GitHub"
	arch="$(uname -m)"
	case "${arch}" in
		x86_64) compose_arch=x86_64 ;;
		aarch64 | arm64) compose_arch=aarch64 ;;
		*)
			echo "Unsupported architecture for Compose: ${arch}" >&2
			exit 1
			;;
	esac
	mkdir -p /usr/libexec/docker/cli-plugins /usr/local/lib/docker/cli-plugins
	curl -fsSL "https://github.com/docker/compose/releases/download/v2.32.4/docker-compose-linux-${compose_arch}" \
		-o /usr/libexec/docker/cli-plugins/docker-compose
	chmod +x /usr/libexec/docker/cli-plugins/docker-compose
	ln -sfn /usr/libexec/docker/cli-plugins/docker-compose /usr/local/lib/docker/cli-plugins/docker-compose
fi

if ! docker compose version >/dev/null 2>&1; then
	echo "docker compose is not available after install." >&2
	exit 1
fi
echo "    $(docker compose version)"

echo "==> Creating ${APP_USER} user and ${APP_DIR}"
if ! id -u "${APP_USER}" >/dev/null 2>&1; then
	useradd --system --home "${APP_DIR}" --shell /usr/sbin/nologin "${APP_USER}"
fi
mkdir -p "${APP_DIR}"
if [[ ! -f "${APP_DIR}/.env" ]]; then
	cp "${SCRIPT_DIR}/env.example" "${APP_DIR}/.env"
fi
chown -R "root:${APP_USER}" "${APP_DIR}"
chmod 755 "${APP_DIR}"
chmod 640 "${APP_DIR}/.env"

echo "==> Installing systemd unit"
cp "${SCRIPT_DIR}/aura-server.service" /etc/systemd/system/aura-server.service
systemctl daemon-reload
systemctl enable aura-server

echo "==> Installing nginx site for ${DOMAIN}"
nginx_site=/etc/nginx/sites-available/aura-server
if [[ -f "${nginx_site}" ]] && grep -q ssl_certificate "${nginx_site}"; then
	echo "    TLS already configured, leaving ${nginx_site} unchanged"
else
	cp "${SCRIPT_DIR}/nginx.conf" "${nginx_site}"
	sed -i "s/server_name .*/server_name ${DOMAIN};/" "${nginx_site}"
fi
ln -sfn "${nginx_site}" /etc/nginx/sites-enabled/aura-server
rm -f /etc/nginx/sites-enabled/default
nginx -t
systemctl reload nginx

echo "==> Configuring firewall"
ufw allow OpenSSH
ufw allow 'Nginx Full'
if ! ufw status | grep -q 'Status: active'; then
	ufw --force enable
fi

echo
echo "Bootstrap complete."
echo
echo "Next, from your laptop:"
echo "  cp deploy/deploy.env.example deploy.env"
echo "  ./scripts/deploy.sh"
echo
echo "Then issue TLS on this server:"
echo "  certbot --nginx -d ${DOMAIN}"
echo
echo "Check:"
echo "  curl -fsS https://${DOMAIN}/health"
