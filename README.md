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
```
