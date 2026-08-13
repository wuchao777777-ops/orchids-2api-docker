# Orchids-2api

[中文](README.md) | [English](README_EN.md)

一个基于 Go 的多通道代理服务，统一暴露 Claude Messages 风格与 OpenAI 兼容接口，当前支持 `orchids`、`warp`、`puter`、`grok` 四类通道。

## 当前状态

- `internal/handler` 统一处理 `orchids` / `warp` / `puter` 的 `/v1/messages` 与 `/v1/chat/completions`
- `internal/grok` 独立处理 `grok` 的 `/v1/chat/completions`、`/v1/images/*`、`/v1/files/*`
- 模型管理支持按通道刷新：`/api/models/refresh`
- Puter 非流式 Claude Messages 已覆盖 `Read`、`Write`、`Edit`、`Delete`、长上下文、多轮 `tool_result` 回归

## 核心能力

- 多账号池与按通道负载均衡
- Claude Messages 兼容接口
- OpenAI Chat Completions 兼容接口
- 通道级模型管理、默认模型与排序
- 管理后台与管理 API
- Redis 持久化存储
- Prometheus 指标与可选 `pprof`
- Grok 图片生成、编辑和本地媒体缓存

## 支持通道

| 通道 | 对外入口 |
|---|---|
| `orchids` | `/orchids/v1/messages`、`/orchids/v1/chat/completions` |
| `warp` | `/warp/v1/messages`、`/warp/v1/chat/completions` |
| `puter` | `/puter/v1/messages`、`/puter/v1/chat/completions` |
| `grok` | `/grok/v1/chat/completions`、`/grok/v1/images/*`、`/grok/v1/files/*` |

统一模型查询入口：

- `GET /v1/models`
- `GET /v1/models/{id}`

## 文档目录

- [架构设计](docs/architecture.md)
- [架构复核](docs/architecture-review.md)
- [API 参考](docs/api-reference.md)
- [配置说明](docs/configuration.md)
- [部署指南](docs/deployment.md)
- [请求流程](docs/ORCHIDS_API_FLOW.md)
- [Grok 与 grok2api 对齐清单](docs/grok2api-parity-checklist.md)

## 环境要求

- Go `1.24+`
- Redis `7+`
- Windows / Linux / macOS

## 快速开始

### 1. 启动 Redis

```bash
docker run -d --name orchids-redis -p 6379:6379 redis:7
```

### 2. 准备配置

从安全示例创建本地配置（`config.json` 不纳入 Git）：

```bash
cp config.example.json config.json
```

最小可用配置：

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

说明：

- 未设置 `admin_pass` 时，程序会在启动时自动生成随机密码并打印日志
- 生产部署建议显式设置高强度 `admin_pass`，并保持 `debug_enabled` 为 `false`
- 运行后若 Redis 中存在 `settings:config`，会覆盖文件配置

### 3. 启动服务

开发模式：

```bash
go run ./cmd/server -config ./config.json
```

生产模式：

```bash
go build -o orchids-server ./cmd/server
./orchids-server -config ./config.json
```

Windows：

```powershell
go build -o server.exe ./cmd/server
.\server.exe -config .\config.json
```

## 常用命令

运行全部测试：

```bash
go test ./...
```

只跑 Puter 相关回归：

```bash
go test ./internal/handler -run "Puter_"
```

重新编译：

```bash
go build -o orchids-server ./cmd/server
```

查看健康状态：

```bash
curl -s http://127.0.0.1:3002/health
curl -s http://127.0.0.1:3002/v1/models
```

测量接口首字节、首个流式帧和总耗时：

```bash
go run ./cmd/ttfbbench -url http://127.0.0.1:3002/grok/v1/chat/completions
```

`cmd/ttfbbench` 是独立诊断工具，不会被主服务编译或启动。

## 模型管理说明

- 管理接口：`POST /api/models/refresh`
- 请求体示例：`{"channel":"puter"}`
- 当前刷新策略是“按来源同步”，不同通道按各自上游能力验证
- Puter 先读取官方模型目录，再用账号 `test_mode` 逐模型验证；目录缺失或验证失败的型号不会进入公开模型表
- `verified` 表示本轮通过通道验证并纳入同步集合的数量

当前各通道模型来源：

- `orchids`：账号上游 `/v1/models`，失败时退回公开页面/内置列表
- `warp`：账号 GraphQL 发现结果，失败时退回内置种子
- `puter`：Puter 公开模型列表 + 账号 test_mode 保守验证
- `grok`：内置支持列表 + 现存模型 + 账号 console 探测

## Puter 当前对齐点

- 请求使用 Puter 原生 `tools`、assistant `tool_calls` 和 `role: tool` 历史格式，不再通过 system prompt 模拟 `<tool_call>`
- 流式响应直接处理 `text`、`reasoning`、`tool_use`、`usage` 和 `error` 事件
- `/puter/v1/messages` 非流式响应会保留 `tool_use` content block，不再返回 `content: null`
- `tool_result` follow-up 可以继续返回新的 `tool_use`，也可以正常收敛成最终文本
- 已有回归测试覆盖 `Read`、`Write`、`Edit`、`Delete`、长上下文、多轮 `tool_result`

当前公开型号：`claude-opus-5`、`claude-sonnet-5`、`claude-fable-5`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna`、`gemini-3.5-flash`、`grok-4.5`、`deepseek-v4-pro`、`deepseek-v4-flash`、`mistral-small-2603`。

## 主要公开端点

### Claude Messages 风格

- `POST /orchids/v1/messages`
- `POST /warp/v1/messages`
- `POST /puter/v1/messages`

### OpenAI Chat Completions 风格

- `POST /orchids/v1/chat/completions`
- `POST /warp/v1/chat/completions`
- `POST /puter/v1/chat/completions`
- `POST /grok/v1/chat/completions`

### Grok 图片与文件

- `POST /grok/v1/images/generations`
- `POST /grok/v1/images/edits`
- `GET /grok/v1/files/{image|video}/{name}`

## 管理端

- UI：`{admin_path}/`
- 登录：`POST /api/login`
- 账号管理：`/api/accounts*`
- 模型管理：`/api/models*`
- 配置管理：`/api/config*`
- Token 缓存：`/api/token-cache/*`

管理接口认证方式：

- `session_token` cookie
- `Authorization: Bearer <admin_token>`
- `X-Admin-Token: <admin_token>`
- Basic Auth，密码等于 `admin_pass`

## 许可证

本仓库遵循仓库内现有许可策略。
