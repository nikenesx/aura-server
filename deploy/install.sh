#!/usr/bin/env bash
set -euo pipefail

# First-time VPS bootstrap. Run as root on the server:
#   sudo bash deploy/install.sh
# or after copying this directory:
#   sudo bash /tmp/aura-deploy/install.sh

if [[ "${EUID}" -ne 0 ]]; then
	echo "Run this script as root (sudo bash deploy/install.sh)" >&2
	exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOMAIN="${DOMAIN:-api.aurameds.ru}"
APP_USER="${APP_USER:-aura}"
APP_DIR="${APP_DIR:-/opt/aura-server}"
GO_VERSION="${GO_VERSION:-1.26.5}"

if [[ ! -f /etc/os-release ]] || ! grep -qiE 'debian|ubuntu' /etc/os-release; then
	echo "This installer targets Debian/Ubuntu. Install nginx, Go, and certbot manually." >&2
	exit 1
fi

echo "==> Installing packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends \
	ca-certificates curl rsync nginx certbot python3-certbot-nginx ufw

install_go() {
	local goarch
	case "$(uname -m)" in
		x86_64) goarch=amd64 ;;
		aarch64 | arm64) goarch=arm64 ;;
		*)
			echo "Unsupported architecture: $(uname -m)" >&2
			exit 1
			;;
	esac

	local archive="go${GO_VERSION}.linux-${goarch}.tar.gz"
	echo "==> Installing Go ${GO_VERSION} (${goarch})"
	curl -fsSL "https://go.dev/dl/${archive}" -o "/tmp/${archive}"
	rm -rf /usr/local/go
	tar -C /usr/local -xzf "/tmp/${archive}"
	rm -f "/tmp/${archive}"
	ln -sfn /usr/local/go/bin/go /usr/local/bin/go
	ln -sfn /usr/local/go/bin/gofmt /usr/local/bin/gofmt
}

need_go=1
go_bin="/usr/local/go/bin/go"
if [[ -x "${go_bin}" ]]; then
	installed="$("${go_bin}" env GOVERSION 2>/dev/null || true)"
	if [[ "${installed}" == "go${GO_VERSION}" ]]; then
		need_go=0
	fi
fi
if [[ "${need_go}" -eq 1 ]]; then
	install_go
fi
echo "    $(/usr/local/go/bin/go version)"

echo "==> Creating ${APP_USER} user and ${APP_DIR}"
if ! id -u "${APP_USER}" >/dev/null 2>&1; then
	useradd --system --home "${APP_DIR}" --shell /usr/sbin/nologin "${APP_USER}"
fi
mkdir -p "${APP_DIR}/bin"
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
echo "  cp deploy/deploy.env.example deploy.env   # if you have not already"
echo "  ./scripts/deploy.sh"
echo
echo "Then issue TLS on this server:"
echo "  certbot --nginx -d ${DOMAIN}"
echo
echo "Check:"
echo "  curl -fsS https://${DOMAIN}/health"
