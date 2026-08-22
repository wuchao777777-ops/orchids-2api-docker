# Orchids-2api

[中文](README.md) | [English](README_EN.md)

A Go-based multi-channel proxy that exposes Claude Messages style and OpenAI-compatible APIs across three upstream channels: `warp`, `puter`, and `grok`.

## Current Status

- `internal/handler` serves `warp` / `puter` for both `/v1/messages` and `/v1/chat/completions`
- `internal/grok` handles `grok` chat, image, and local file endpoints
- per-channel model sync is available through `POST /api/models/refresh`
- Puter non-stream Claude Messages regressions are covered for `Read`, `Write`, `Edit`, `Delete`, long-context, and multi-round `tool_result`

## Core Features

- multi-account pools with per-channel load balancing
- Claude Messages compatible endpoints
- OpenAI Chat Completions compatible endpoints
- model management, default model selection, and sorting
- admin UI and admin API
- Redis-backed persistence
- Prometheus metrics and optional `pprof`
- Grok image generation, editing, and local media caching

## Supported Channels

| Channel | Public routes |
|---|---|
| `warp` | `/warp/v1/messages`, `/warp/v1/chat/completions` |
| `puter` | `/puter/v1/messages`, `/puter/v1/chat/completions` |
| `grok` | `/grok/v1/chat/completions`, `/grok/v1/images/*`, `/grok/v1/files/*` |

Unified model lookup:

- `GET /v1/models`
- `GET /v1/models/{id}`

## Documentation

- [Architecture](docs/architecture.md)
- [Architecture Review](docs/architecture-review.md)
- [API Reference](docs/api-reference.md)
- [Configuration](docs/configuration.md)
- [Deployment](docs/deployment.md)
- [Grok parity checklist](docs/grok2api-parity-checklist.md)

## Requirements

- Go `1.24+`
- Redis `7+`
- Windows / Linux / macOS

## Quick Start

### 1. Start Redis

```bash
docker run -d --name orchids-redis -p 6379:6379 redis:7
```

### 2. Create `config.json`

Copy the safe example first (`config.json` is not tracked by Git):

```bash
cp config.example.json config.json
```

```json
{
  "port": "3002",
  "store_mode": "redis",
  "redis_addr": "127.0.0.1:6379",
  "admin_user": "admin",
  "admin_pass": "",
  "admin_path": "/admin",
  "debug_enabled": false
}
```

Notes:

- if `admin_pass` is omitted, the server generates a random password at startup and prints it to logs
- in production, set a strong `admin_pass` explicitly and keep `debug_enabled` set to `false`
- if Redis already contains `settings:config`, that stored config overrides the file on boot

### 3. Start the server

Development:

```bash
go run ./cmd/server -config ./config.json
```

Production:

```bash
go build -o orchids-server ./cmd/server
./orchids-server -config ./config.json
```

Windows:

```powershell
go build -o server.exe ./cmd/server
.\server.exe -config .\config.json
```

## Common Commands

Run all tests:

```bash
go test ./...
```

Run Puter-specific regressions:

```bash
go test ./internal/handler -run "Puter_"
```

Rebuild:

```bash
go build -o orchids-server ./cmd/server
```

Basic health checks:

```bash
curl -s http://127.0.0.1:3002/health
curl -s http://127.0.0.1:3002/v1/models
```

Measure time to headers, first streaming frame, and total response time:

```bash
go run ./cmd/ttfbbench -url http://127.0.0.1:3002/grok/v1/chat/completions
```

`cmd/ttfbbench` is a standalone diagnostic utility and is not built into the server.

## Model Sync Behavior

- endpoint: `POST /api/models/refresh`
- example body: `{"channel":"puter"}`
- sync is source-driven; Puter additionally verifies official-catalog candidates with account `test_mode`
- newly discovered models are inserted
- locally stored models missing from the source are deleted
- `verified` reports the number of models accepted into the synced set for that run

## Puter Notes

- requests use native `tools`, assistant `tool_calls`, and `role: tool` history instead of prompt-level `<tool_call>` emulation
- streams are decoded by native `text`, `reasoning`, `tool_use`, `usage`, and `error` event type
- `/puter/v1/messages` non-stream responses preserve `tool_use` content blocks
- `tool_result` follow-ups can either continue the tool chain or converge to final text
- regressions are covered for `Read`, `Write`, `Edit`, `Delete`, long context, and multi-round `tool_result`

## Admin

- UI: `{admin_path}/`
- login: `POST /api/login`
- account management: `/api/accounts*`
- model management: `/api/models*`
- config management: `/api/config*`
- token cache: `/api/token-cache/*`

Auth methods:

- `session_token` cookie
- `Authorization: Bearer <admin_token>`
- `X-Admin-Token: <admin_token>`
- Basic Auth with password equal to `admin_pass`

## License

This repository follows the existing license policy already used in the repo.
