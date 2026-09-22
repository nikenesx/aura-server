# aura-server

Backend API for the Aura desktop application — processes veterinary appointment audio recordings.

## Requirements

For Docker (recommended locally): Docker Desktop with WSL2 and an NVIDIA GPU.

Without Docker: Go 1.26+, Python 3.12, ffmpeg, and a CUDA-capable PyTorch install.

## Quick start

Copy `.env.example` to `.env` and put `HF_TOKEN` there after accepting Hugging Face access for `pyannote/speaker-diarization-3.1` and `pyannote/segmentation-3.0`.

```powershell
# Windows: Docker Desktop must be running, GPU enabled
.\scripts\run-docker.ps1
# .\scripts\run-docker.ps1 -Stop
```

Or:

```bash
cp .env.example .env
docker compose up --build -d --wait
```

The API listens on `http://127.0.0.1:8080`. Go, Python, CUDA, ffmpeg, and models stay inside the images; model weights are cached in a Docker volume after the first start.

## Health check

```bash
curl http://localhost:8080/health
# {"status":"ok","asr":"ok"}
```

## Transcribe

Recognition is async. Docker already starts the Python ASR worker (GigaAM + pyannote) and the Go API.

Without Docker, on the host GPU:

```powershell
.\scripts\setup-local-gpu.ps1
.\scripts\run-local.ps1
# .\scripts\run-local.ps1 -Stop
```

```bash
curl.exe -D - -F file=@recording.wav http://127.0.0.1:8080/v1/transcribe
# HTTP 202  {"job_id":"..."}

curl.exe -fsS http://127.0.0.1:8080/v1/transcribe/JOB_ID
```

```json
{
  "job_id": "...",
  "status": "done",
  "result": {
    "text": "что случилось с барсиком он не ест",
    "phrases": [
      {"text": "что случилось с барсиком", "start": 1.2, "end": 3.4, "speaker": "speaker_0"},
      {"text": "он не ест", "start": 3.8, "end": 5.1, "speaker": "speaker_1"}
    ]
  }
}
```

`status` is `queued`, `processing`, `done`, or `failed`. Jobs live in memory only.

Pipeline: ffmpeg decodes to 16 kHz PCM in memory → pyannote finds who speaks when → speech is cut into chunks of one speaker each (up to ~22 s, cut in pauses) → GigaAM recognises the chunks in batches with word timestamps → each word goes to the speaker whose turn overlaps it most → consecutive words of one speaker become a phrase. `speaker_0` is whoever spoke first.

On an RTX 4080 a 60-minute two-speaker recording takes about 45 s (about 25 s with `DIAR_SEGMENTATION_STEP=0.2`); diarization is most of it.

### Benchmark and quality

```bash
cd asr
python bench.py recording.m4a --runs 2 --json run.json --result result.json --transcript result.txt
python bench.py recording.m4a --tile-minutes 60   # timing only: repeats the file to 60 min
python evaluate.py reference.json result.json     # WER, cpWER, DER against a hand-labelled reference
```

A reference is `{"segments": [{"start", "end", "speaker", "text"}]}`. Write numbers the way GigaAM does (digits), or WER counts the difference as errors.

## Configuration

| Variable        | Default                         | Description                          |
|-----------------|---------------------------------|--------------------------------------|
| `HTTP_ADDR`     | `:8080`                         | HTTP listen address                  |
| `LOG_LEVEL`     | `info`                          | Log level                            |
| `ASR_URL`       | `http://127.0.0.1:8090`         | Python ASR worker                    |
| `MAX_UPLOAD_BYTES` | `104857600`                  | Upload size limit                    |
| `ASR_TIMEOUT`   | `30m`                           | Worker timeout                       |
| `JOB_TTL`       | `2h`                            | In-memory job lifetime               |
| `HF_TOKEN`      |                                 | Hugging Face token for pyannote      |
| `GIGAAM_MODEL`  | `v3_e2e_rnnt`                   | GigaAM checkpoint                    |
| `DIAR_SPEAKERS` | `2`                             | Expected speakers; `auto` to detect  |
| `DIAR_MODEL`    | `pyannote/speaker-diarization-3.1` | Diarization pipeline; `pyannote/speaker-diarization-community-1` is worth comparing on real recordings |
| `DIAR_SEGMENTATION_STEP` | `0.1`                  | Window step as a share of the window; `0.2` is ~2x faster |
| `DIAR_SEGMENTATION_BATCH` | from model config     | pyannote segmentation batch size     |
| `DIAR_EMBEDDING_BATCH` | from model config        | pyannote embedding batch size; larger does not speed it up but costs VRAM |
| `ASR_BATCH_SIZE` | `16`                           | GigaAM chunks per batch              |

## Commands

```bash
make run        # start the Go API on the host
make build      # build binary to bin/api
make test       # run tests
make lint       # run golangci-lint (requires golangci-lint)
make docker-up  # build and start API + ASR in Docker
make docker-down
make deploy     # rsync sources, build on the VPS, restart systemd
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
