# aura-server

Backend API for the Aura desktop application — processes veterinary appointment audio recordings.

## Requirements

For Docker (recommended locally): Docker Desktop with WSL2 and an NVIDIA GPU.

Without Docker: Go 1.26+, Python 3.12, ffmpeg, and a CUDA-capable PyTorch install.

## Quick start

Copy `.env.example` to `.env` and put `HF_TOKEN` there after accepting Hugging Face access for `pyannote/speaker-diarization-3.1` and `pyannote/segmentation-3.0`. On a 16 GB GPU also set `LLM_GPU_MEMORY_UTILIZATION` and `LLM_MAX_MODEL_LEN` (see "Appointment card"), or the `llm` container dies with "No available memory for the cache blocks".

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
# {"status":"ok","asr":"ok","llm":"ok"}
```

`llm` is `disabled` when `LLM_URL` is empty.

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

`status` is `queued`, `processing`, `done`, or `failed`. The job also carries `card_status` (see below). Jobs live in memory only.

## Appointment card

Once the transcript is done, the same job goes on to fill the appointment card with the local LLM ([T-lite-it-2.1-FP8](https://huggingface.co/t-tech/T-lite-it-2.1-FP8) in vLLM). The transcript is available as soon as it is ready; poll the card separately:

```bash
curl.exe -fsS http://127.0.0.1:8080/v1/transcribe/JOB_ID/card
```

```json
{
  "job_id": "...",
  "status": "done",
  "card": {
    "template": "vet_appointment_v1",
    "roles": {"speaker_0": "doctor", "speaker_1": "owner"},
    "sections": [
      {"title": "", "fields": [
        {"key": "pet_name", "title": "Питомец", "value": "Муся", "sources": [0]},
        {"key": "breed", "title": "Порода", "value": null, "sources": []}
      ]},
      {"title": "Рекомендации и назначения", "fields": [
        {"key": "recommendations", "title": "Рекомендации", "items": [
          {"text": "Преднизолон 5 мг в табл., по 1/2 табл. внутрь через день, 7 дней.", "sources": [12]}
        ]}
      ]}
    ],
    "text": "Ф.И.О. владельца: —\nПитомец: Муся\n..."
  }
}
```

- `status`: `queued`, `processing`, `done`, `failed` (with `error`), or `disabled` when no LLM is configured. A failed transcript fails the card too.
- `sources` are indices into `result.phrases` of the transcript, so the client can jump to the audio a value came from.
- Fields nobody mentioned are `null`; the model is told not to guess.
- `text` is the card laid out like the printed form.

The template (`internal/card/template.go`, `VetAppointment`) is a constant for now; templates will move to the database.

A card for a 5-minute appointment takes about 18 s on an RTX 4080 (~60 tokens/s). The first request after a restart is slower: vLLM compiles kernels for the new shapes.

The LLM runs in the `llm` compose service, started after ASR. vLLM does not run natively on Windows, so it runs in Docker even in local mode:

```powershell
.\scripts\run-local.ps1 -Llm   # ASR and the API on the host, vLLM in Docker
```

Without `-Llm` the API starts with no `LLM_URL` and cards are `disabled`. If the LLM is down, cards wait for it for up to 15 minutes before failing.

On a 16 GB card ASR and the LLM do fit, but only just, and the GPU starts paging to system RAM: generation drops from ~52 to ~10 tokens/s, and a card for a 5-minute appointment takes ~107 s instead of ~22 s. For anything but a plumbing check, run them one at a time (stop ASR, or give it `CUDA_VISIBLE_DEVICES=` to keep it on the CPU) and put this in `.env`:

```
LLM_GPU_MEMORY_UTILIZATION=0.85
LLM_MAX_MODEL_LEN=8192
```

24 GB has room for both: ASR takes about 3 GB and vLLM 14.4 GB at the default `0.6`.

The local scripts and the Compose stack both bind port 8080, so run one or the other: `docker compose down` before `run-local.ps1`.

To check card quality against a real recording:

```bash
cd asr && python bench.py recording.m4a --result result.json && cd ..
AURA_LLM_URL=http://127.0.0.1:8000 AURA_TRANSCRIPT=asr/result.json go test ./internal/card -run Live -v
```

**The card is a draft for the doctor to check, not a record to trust.** On a full synthetic appointment the model filled the diagnosis, prescriptions and doses correctly. Two guards drop what it makes up: a value with no `sources`, and a prescription with no word in common with the phrase it cites. Together they removed an invented owner name, and an entire treatment plan (fipronil, premises treatment, a follow-up date) that the model produced for a clip that ended before any treatment was discussed.

What the guards do not catch:

- An inference the model can point at a phrase for. It wrote `Порода: б/п` from "подобрали на улице" — what a doctor would write, but never said aloud.
- Junk that echoes the transcript. On the same truncated clip one recommendation came out as "Сейчас взвесим".

So: full recordings give good drafts, partial ones give noise, and the doctor checks either way.

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
| `LLM_URL`       | empty (cards disabled)          | OpenAI-compatible server for cards; `http://llm:8000` in Compose |
| `LLM_MODEL`     | `t-lite`                        | Served model name                    |
| `LLM_TIMEOUT`   | `5m`                            | One card request                     |
| `VLLM_TAG`      | `v0.29.0`                       | vLLM image tag (Compose)             |
| `LLM_HF_MODEL`  | `t-tech/T-lite-it-2.1-FP8`      | Model the `llm` service loads (Compose) |
| `LLM_GPU_MEMORY_UTILIZATION` | `0.6`              | Share of the whole GPU vLLM may take (Compose); ~0.7 on 16 GB cards |
| `LLM_MAX_MODEL_LEN` | `32768`                     | Context length (Compose); a 60-minute transcript is ~20–30k tokens |

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
