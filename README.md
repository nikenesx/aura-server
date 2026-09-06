# aura-server

Backend API for the Aura desktop application — processes veterinary appointment audio recordings.

## Requirements

- Go 1.26+ (local development)
- Docker and Docker Compose (API + T-one)

## Quick start

```bash
cp .env.example .env
docker compose up --build
```

The API listens on `127.0.0.1:8080`. T-one runs in a separate container; you do not install Python.

```bash
curl http://127.0.0.1:8080/health
# {"status":"ok","asr":"ok"}
```

First start downloads the T-one image (~4 GB) and can take several minutes.

## Transcribe

```bash
curl -fsS -F file=@recording.wav http://127.0.0.1:8080/v1/transcribe
```

```json
{
  "text": "привет это я",
  "phrases": [
    {"text": "привет", "start": 1.79, "end": 2.04},
    {"text": "это я", "start": 3.72, "end": 4.26}
  ]
}
```

`POST /v1/transcribe` is synchronous: send audio (`wav`, `mp3`, `m4a`, `ogg`, `flac`, …) as multipart field `file` and wait for the JSON body. Limit is 100 MB. Without the ASR container the endpoint returns `503`.

## Configuration

| Variable           | Default                 | Description                         |
|--------------------|-------------------------|-------------------------------------|
| `HTTP_ADDR`        | `:8080`                 | HTTP listen address (inside Docker) |
| `LOG_LEVEL`        | `info`                  | Log level                           |
| `ASR_URL`          | `http://127.0.0.1:8090` | T-one service URL                   |
| `MAX_UPLOAD_BYTES` | `104857600`             | Upload size limit                   |
| `ASR_TIMEOUT`      | `30m`                   | Transcription timeout               |
| `VAD_ENABLED`      | `true`                  | Skip silence with Silero VAD        |
| `VAD_THRESHOLD`    | `0.5`                   | Speech probability cutoff (0–1)     |
| `VAD_MIN_SPEECH_MS`| `300`                   | Drop speech shorter than this       |
| `VAD_MIN_SILENCE_MS`| `400`                  | Silence needed to split segments    |
| `VAD_PAD_MS`       | `300`                   | Extra audio kept around speech      |

## Commands

```bash
make up     # docker compose up --build
make down   # docker compose down
make run    # Go API only (no Docker; transcribe needs ASR)
make build  # build binary to bin/api
make test   # run tests
make deploy # rsync + rebuild containers on the VPS
```

## Production (VPS + nginx)

DNS for `api.aurameds.ru` must already point at the VPS. nginx terminates HTTP/TLS and proxies to the Docker API on `127.0.0.1:8080`.

### 1. SSH access

```bash
ssh-copy-id root@YOUR_VPS_IP
```

### 2. Deploy config on your laptop

```bash
cp deploy/deploy.env.example deploy.env
```

Set `DEPLOY_HOST` to `api.aurameds.ru` or the VPS IP.

### 3. First deploy (and every later update)

```bash
./scripts/deploy.sh
# or: make deploy
```

The first run installs Docker, nginx, certbot, and a systemd unit that starts `docker compose`. Later runs rsync the tree (they do not overwrite `/opt/aura-server/.env`), rebuild images on the server, and restart compose. The first image build is slow because of T-one.

### 4. TLS

```bash
ssh root@YOUR_VPS_IP 'certbot --nginx -d api.aurameds.ru'
```

```bash
curl -fsS https://api.aurameds.ru/health
curl -fsS -F file=@recording.wav https://api.aurameds.ru/v1/transcribe
```

Do not copy `deploy/nginx.conf` over the live site after certbot — certbot amends that file. Re-running `install.sh` leaves an existing TLS site alone.

### Useful commands on the VPS

```bash
docker compose -f /opt/aura-server/docker-compose.yml ps
docker compose -f /opt/aura-server/docker-compose.yml logs -f
systemctl status aura-server
nginx -t && systemctl reload nginx
```
