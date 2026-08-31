# aura-server

Backend API for the Aura desktop application — processes veterinary appointment audio recordings.

## Requirements

- Go 1.26+

## Quick start

```bash
# Copy env template (optional)
cp .env.example .env

# Run the server
make run
```

The server starts on `:8080` by default.

## Health check

```bash
curl http://localhost:8080/health
# {"status":"ok"}
```

## Configuration

| Variable    | Default | Description              |
|-------------|---------|--------------------------|
| `HTTP_ADDR` | `:8080` | HTTP listen address      |
| `LOG_LEVEL` | `info`  | Log level (debug/info/warn/error) |

## Commands

```bash
make run    # start the server
make build  # build binary to bin/api
make test   # run tests
make lint   # run golangci-lint (requires golangci-lint)
make deploy # rsync sources, build on the VPS, restart systemd
```

## Production (VPS + nginx)

DNS for `api.aurameds.ru` must already point at the VPS. The Go process listens on `127.0.0.1:8080`; nginx terminates HTTP/TLS and reverse-proxies to it.

### 1. SSH access

```bash
ssh-copy-id root@YOUR_VPS_IP
```

### 2. Deploy config on your laptop

```bash
cp deploy/deploy.env.example deploy.env
```

Set `DEPLOY_HOST` to `api.aurameds.ru` or the VPS IP if SSH should not rely on DNS.

### 3. First deploy (and every later update)

```bash
./scripts/deploy.sh
# or: make deploy
```

The first run installs Go, nginx, certbot, a systemd unit (`aura-server` as user `aura`), and an nginx site. Later runs rsync the tree (they do not overwrite `/opt/aura-server/.env`), run `go build` on the server, and restart systemd.

Production env lives at `/opt/aura-server/.env` (`HTTP_ADDR=127.0.0.1:8080`).

### 4. TLS

On the VPS, after nginx is serving port 80:

```bash
ssh root@YOUR_VPS_IP 'certbot --nginx -d api.aurameds.ru'
```

Then:

```bash
curl -fsS https://api.aurameds.ru/health
# {"status":"ok"}
```

Do not copy `deploy/nginx.conf` over the live site after certbot — certbot amends that file. Re-running `install.sh` leaves an existing TLS site alone.

### Useful commands on the VPS

```bash
systemctl status aura-server
journalctl -u aura-server -f
nginx -t && systemctl reload nginx
```
