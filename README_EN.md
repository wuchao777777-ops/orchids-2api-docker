# Orchids-2api

[中文](README.md) | [English](README_EN.md)

A Go-based multi-provider proxy that exposes Claude-style and OpenAI-compatible APIs on top of pooled upstream accounts for `orchids`, `warp`, and `grok`.

## Overview

Orchids-2api provides one unified API surface for multiple upstream account pools:

- expose a consistent API to clients
- select accounts from a pool automatically
- fail over when an upstream account is unavailable
- manage accounts, models, config, cache, and debug traces from one admin UI
- extend `grok` with image generation/edit support, local media caching, and OpenAI-compatible output

## Core Features

- multi-account pools with load balancing
- per-channel model routing and model management
- Claude Messages compatible endpoints
- OpenAI Chat Completions compatible endpoints
- Grok image generation, editing, and local media caching
- admin UI and admin API
- Prometheus metrics and optional `pprof`
- Redis-backed persistence

## Supported Upstream Channels

- `orchids`
- `warp`
- `grok`

## Documentation

Detailed docs currently live under [`docs/`](docs) and are primarily Chinese-first:

- [Architecture](docs/architecture.md)
- [Architecture Review](docs/architecture-review.md)
- [API Reference](docs/api-reference.md)
- [Configuration](docs/configuration.md)
- [Deployment](docs/deployment.md)
- [Orchids Request Flow](docs/ORCHIDS_API_FLOW.md)
- [Grok Parity Checklist](docs/grok2api-parity-checklist.md)

## Requirements

- Go `1.22+`
- Redis `7+`
- Linux or macOS environment capable of running Go

## Quick Start

### 1. Start Redis

```bash
docker run -d --name orchids-redis -p 6379:6379 redis:7
```

### 2. Create `config.json`

Minimal working example:

```json
{
  "port": "3002",
  "store_mode": "redis",
  "redis_addr": "127.0.0.1:6379",
  "admin_user": "admin",
  "admin_pass": "admin123",
  "admin_path": "/admin"
}
```

### 3. Start the server

Development mode:

```bash
go run ./cmd/server/main.go -config ./config.json
```

Production mode:

```bash
go build -o orchids-server ./cmd/server
./orchids-server -config ./config.json
```

Run in background:

```bash
nohup ./orchids-server -config ./config.json > server.log 2>&1 &
```

## Common Commands

Rebuild and restart:

```bash
pkill -f "./orchids-server -config ./config.json" || true
go build -o orchids-server ./cmd/server
nohup ./orchids-server -config ./config.json > server.log 2>&1 &
```

Tail logs:

```bash
tail -n 200 server.log
```

Run tests:

```bash
go test ./...
```

## Public Endpoints

### Claude Messages style

- `POST /orchids/v1/messages`
- `POST /warp/v1/messages`

### OpenAI Chat Completions style

- `POST /orchids/v1/chat/completions`
- `POST /warp/v1/chat/completions`
- `POST /grok/v1/chat/completions`

### Grok image endpoints

- `POST /grok/v1/images/generations`
- `POST /grok/v1/images/edits`
- `GET /grok/v1/files/{image|video}/{name}`

### Generic endpoints

- `GET /v1/models`
- `GET /health`
- `GET /metrics`

For request and response examples, see [API Reference](docs/api-reference.md).

## Admin

- UI: `{admin_path}/`, default `/admin`
- login endpoint: `POST /api/login`
- account, model, config, and cache endpoints: `/api/*`

Admin endpoints use session cookies by default and also support:

- `Authorization: Bearer <admin_token>`
- `X-Admin-Token: <admin_token>`

## Grok Media Notes

- generated or edited media is converted to locally reachable URLs when possible: `/grok/v1/files/image/*`
- cache directories: `data/tmp/image` and `data/tmp/video`
- if `assets.grok.com` is not directly reachable, clients can still render media through the local cache endpoint

## FAQ

### 1. `model not found`

- call `GET /grok/v1/models` or `GET /v1/models` first to verify model IDs
- common typo: `gork-3`; the correct model name is `grok-3`

### 2. Grok images do not render

- check whether the response contains `/grok/v1/files/image/...`
- check whether cached files exist under `data/tmp/image`

### 3. Port is not listening after startup

```bash
lsof -iTCP:3002 -sTCP:LISTEN -n -P
```

### 4. Grok quota stays at `80 / 80`

Current Grok quota display depends on upstream rate-limit data. If upstream keeps returning `remaining=80` and `total=80`, the admin UI will display that value as-is. Use local request logs and `request_count` together when judging actual usage.

## License

This repository follows the existing license policy already used in the repo.
