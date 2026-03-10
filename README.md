# Orchids-2api

[中文](README.md) | [English](README_EN.md)

一个基于 Go 的多通道代理服务，统一暴露兼容 Claude / OpenAI 风格接口，支持 `orchids`、`warp`、`grok` 三类上游账号池、负载均衡、自动切换，以及 Web 管理后台。

## 项目概览

Orchids-2api 的目标是把多个上游账号池封装成一套统一 API：

- 对外暴露统一接口，减少客户端适配成本
- 在账号池内自动选择账号，并在失败时切换
- 统一管理模型、账号、配置、缓存和调试信息
- 为 `grok` 补齐图片生成/编辑、本地媒体缓存与 OpenAI 兼容输出

## 核心能力

- 多账号池 + 负载均衡
- 通道级模型管理与路由
- Claude Messages 兼容接口
- OpenAI Chat Completions 兼容接口
- Grok 图片生成、编辑、本地媒体缓存
- 管理后台与管理 API
- Prometheus 指标、可选 `pprof`
- Redis 持久化存储

## 支持的上游通道

- `orchids`
- `warp`
- `grok`

## 文档目录

- [架构设计](docs/architecture.md)
- [架构复核](docs/architecture-review.md)
- [API 参考](docs/api-reference.md)
- [配置说明](docs/configuration.md)
- [部署指南](docs/deployment.md)
- [Orchids 请求流程](docs/ORCHIDS_API_FLOW.md)
- [Grok 兼容性检查](docs/grok2api-parity-checklist.md)

## 环境要求

- Go `1.22+`
- Redis `7+`
- Linux / macOS 任一可运行 Go 的环境

## 快速开始

### 1. 启动 Redis

```bash
docker run -d --name orchids-redis -p 6379:6379 redis:7
```

### 2. 准备配置

最小可用 `config.json` 示例：

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

### 3. 启动服务

开发模式：

```bash
go run ./cmd/server/main.go -config ./config.json
```

生产模式：

```bash
go build -o orchids-server ./cmd/server
./orchids-server -config ./config.json
```

后台运行：

```bash
nohup ./orchids-server -config ./config.json > server.log 2>&1 &
```

## 常用命令

重新编译并重启：

```bash
pkill -f "./orchids-server -config ./config.json" || true
go build -o orchids-server ./cmd/server
nohup ./orchids-server -config ./config.json > server.log 2>&1 &
```

查看日志：

```bash
tail -n 200 server.log
```

运行测试：

```bash
go test ./...
```

## 主要公开端点

### Claude Messages 风格

- `POST /orchids/v1/messages`
- `POST /warp/v1/messages`

### OpenAI Chat Completions 风格

- `POST /orchids/v1/chat/completions`
- `POST /warp/v1/chat/completions`
- `POST /grok/v1/chat/completions`

### Grok 图片相关

- `POST /grok/v1/images/generations`
- `POST /grok/v1/images/edits`
- `GET /grok/v1/files/{image|video}/{name}`

### 通用接口

- `GET /v1/models`
- `GET /health`
- `GET /metrics`

详细请求与响应示例见 [API 参考](docs/api-reference.md)。

## 管理端

- UI：`{admin_path}/`，默认 `/admin`
- 登录：`POST /api/login`
- 账号、模型、配置、缓存等接口：`/api/*`

管理接口默认使用 session cookie，也支持：

- `Authorization: Bearer <admin_token>`
- `X-Admin-Token: <admin_token>`

## Grok 图片链路说明

- 生成/编辑结果会优先转为本地可访问地址：`/grok/v1/files/image/*`
- 缓存目录：`data/tmp/image`、`data/tmp/video`
- 即使 `assets.grok.com` 不可直连，也可以通过本地缓存地址展示

## 常见问题

### 1. `model not found`

- 先调用 `GET /grok/v1/models` 或 `GET /v1/models` 确认模型名
- 常见拼写错误：`gork-3`，正确写法应为 `grok-3`

### 2. Grok 图片不显示

- 检查返回是否为 `/grok/v1/files/image/...`
- 检查本地缓存目录是否存在文件：`data/tmp/image`

### 3. 服务启动后端口未监听

```bash
lsof -iTCP:3002 -sTCP:LISTEN -n -P
```

### 4. Grok 额度长期显示 `80 / 80`

当前 Grok 额度显示依赖上游返回的 rate-limit 数据；如果上游始终返回 `remaining=80, total=80`，页面会忠实显示该值。需要结合本地 `request_count` 与实际请求日志一起判断真实使用情况。

## 许可证

本仓库遵循仓库内现有许可策略。
