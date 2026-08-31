# Orchids-2api

[中文](README.md) | [English](README_EN.md)

一个基于 Go 的多通道代理服务，统一暴露 Claude Messages 风格与 OpenAI 兼容接口，当前支持 `warp`、`puter`、`grok` 三类通道。

## 当前状态

- `internal/handler` 统一处理 `warp` / `puter` 的 `/v1/messages` 与 `/v1/chat/completions`
- `internal/grok` 独立处理 `grok` 的 Messages、Responses、Chat、图片、视频和本地媒体接口
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
- 推理 API Key 默认鉴权、模型白名单/RPM/到期策略与 Redis 账号凭据 AES-GCM 加密

## 支持通道

| 通道 | 对外入口 |
|---|---|
| `warp` | `/warp/v1/messages`、`/warp/v1/chat/completions` |
| `puter` | `/puter/v1/messages`、`/puter/v1/chat/completions` |
| `grok` | `/grok/v1/messages`、`/grok/v1/responses`、`/grok/v1/chat/completions`、图片、视频与文件接口 |

统一模型查询入口：

- `GET /v1/models`
- `GET /v1/models/{id}`

## 文档目录

- [架构设计](docs/architecture.md)
- [架构复核](docs/architecture-review.md)
- [API 参考](docs/api-reference.md)
- [配置说明](docs/configuration.md)
- [部署指南](docs/deployment.md)
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
  "inference_auth_enabled": true,
  "credential_encryption_key_file": "data/credential.key",
  "response_store_ttl_hours": 720,
  "debug_enabled": false
}
```

说明：

- 未设置 `admin_pass` 时，程序会在启动时自动生成随机密码并打印日志
- 生产部署建议显式设置高强度 `admin_pass`，并保持 `debug_enabled` 为 `false`
- 运行后若 Redis 中存在 `settings:config`，会覆盖文件配置
- 首次启动会生成 `data/credential.key`；该文件必须和 Redis 数据一起持久化、备份，丢失后无法解密账号凭据
- 登录管理端创建 API Key 后，使用 `Authorization: Bearer <API Key>` 调用模型和推理接口；Anthropic SDK 也可使用 `x-api-key`

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
curl -s http://127.0.0.1:3002/v1/models -H 'Authorization: Bearer sk-...'
```

## 模型管理说明

- 管理接口：`POST /api/models/refresh`
- 请求体示例：`{"channel":"puter"}`
- 当前刷新策略是“按来源同步”，不同通道按各自上游能力验证
- Puter 先读取官方模型目录，再用账号 `test_mode` 逐模型验证；目录缺失或验证失败的型号不会进入公开模型表
- `verified` 表示本轮通过通道验证并纳入同步集合的数量

当前各通道模型来源：

- `warp`：账号 GraphQL 发现结果，失败时退回内置种子
- `puter`：Puter 公开模型列表 + 账号 test_mode 保守验证
- `grok`：内置支持列表 + 现存模型 + 账号 console 探测

## Puter 当前对齐点

- 请求使用 Puter 原生 `tools`、assistant `tool_calls` 和 `role: tool` 历史格式，不再通过 system prompt 模拟 `<tool_call>`
- 流式响应直接处理 `text`、`reasoning`、`tool_use`、`usage` 和 `error` 事件
- `/puter/v1/messages` 非流式响应会保留 `tool_use` content block，不再返回 `content: null`
- `tool_result` follow-up 可以继续返回新的 `tool_use`，也可以正常收敛成最终文本
- 已有回归测试覆盖 `Read`、`Write`、`Edit`、`Delete`、长上下文、多轮 `tool_result`

当前公开型号：`claude-opus-5`、`claude-sonnet-5`、`claude-fable-5`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna`、`gemini-3.5-flash`、`grok-4.5`、`grok-4.6`、`deepseek-v4-pro`、`deepseek-v4-flash`、`mistral-small-2603`。

## 主要公开端点

### Claude Messages 风格

- `POST /warp/v1/messages`
- `POST /puter/v1/messages`
- `POST /grok/v1/messages`

### OpenAI Chat Completions 风格

- `POST /warp/v1/chat/completions`
- `POST /puter/v1/chat/completions`
- `POST /grok/v1/chat/completions`

### OpenAI Responses 风格

- `POST /grok/v1/responses`
- `POST /grok/v1/responses/compact`
- `GET /grok/v1/responses/{response_id}`
- `DELETE /grok/v1/responses/{response_id}`

Build stored Responses 会按客户端 API Key 隔离，并固定回创建该 Response 的 OAuth 账号；归属记录默认保留 720 小时。

### Grok 图片与文件

- `POST /grok/v1/images/generations`
- `POST /grok/v1/images/edits`
- `GET /grok/v1/files/{image|video}/{name}`

### Grok 视频与语音

- `POST /grok/v1/videos/generations`（Console 标准生成）
- `POST /grok/v1/videos/edits`、`POST /grok/v1/videos/extensions`
- `POST /grok/v1/videos`（保留的 Web app-chat 旧版生成入口）
- `GET /grok/v1/videos/{video_id}` 与 `/content`
- `POST /grok/v1/media/inputs`，以及 `GET` / `DELETE /grok/v1/media/inputs/{file_id}`
- `POST /grok/v1/tts`、`GET /grok/v1/tts/voices`
- `POST /grok/v1/stt`，以及 `GET /grok/v1/stt` WebSocket
- `GET /grok/v1/realtime` WebSocket
- `POST /grok/v1/audio/speech`、`POST /grok/v1/audio/tasks`
- `POST /grok/v1/audio/transcriptions`（`json`、`verbose_json`、`text`）

以上接口同时提供 `/v1/*` 别名。标准视频和语音接口使用 Grok Console SSO + DPoP；`grok_console_base_url` 可覆盖默认的 `https://console.x.ai/v1`。标准视频任务按调用方 API Key 隔离，任务元数据以一小时 TTL 写入 Redis；已提交到 Console 并取得上游 `request_id` 的任务会在服务重启后使用原账号继续轮询和下载。多实例通过 30 秒 Redis 原子租约和心跳保证同一任务只有一个 worker，租约过期后可由其他实例接管。`media_dir` 可挂载共享文件系统；多副本模式要求 Redis、每实例唯一的 `deployment_instance_id`、共同的 `deployment_cluster_id` 和 `shared_media=true`，启动时会校验集群标记及共享目录读写。媒体输入接口接受 20 MiB 以内的图片或视频 multipart `file`，返回保留 24 小时且按 API Key 隔离的 `file_id`；标准视频的 `image`、`reference_images` 和 `video` 均可使用。支持 `grok-imagine-video`，生成还支持 `grok-imagine-video-1.5`；编辑和延长目前遵循上游限制，仅支持基础模型。托管 Grok 出口池暂不支持语音 WebSocket 拨号，启用时会安全拒绝该类连接；静态 HTTP/SOCKS 代理不受影响。

OpenAI 转录兼容层会拒绝 Console STT 无法无损表示的 `prompt`、非零 `temperature` 和 `timestamp_granularities`，并且不实现语义不同的 `/audio/translations`。

## Grok 上游模式

Grok 代码保留三种上游传输；当前公开模型按 `internal/grok/models.go` 的 `ModelSpec` 路由：

| 模式 | 上游 | 账号凭据 | 典型模型 |
|---|---|---|---|
| app-chat（Web） | `grok.com/rest/app-chat/...` | SSO Cookie（`client_cookie`） | 当前 imagine 图片、编辑和视频模型 |
| console | `console.x.ai/v1/*` + DPoP | Console SSO Cookie | Responses、标准视频、TTS、STT、Realtime；只有显式加入兼容表的模型才会公开 |
| cli（Build） | `cli-chat-proxy.grok.com/v1` + Bearer | OAuth token（`credential_type="oauth"` + access/refresh token） | 当前 `grok-4.5`、`grok-4.6` |

### 新增配置（config.json / Redis）

- `grok_cli_base_url` / `grok_cli_user_agent` / `grok_cli_client_version` / `grok_cli_client_identifier`
- `grok_console_base_url`（默认 `https://console.x.ai/v1`，用于 Console Responses、标准视频、TTS、STT 与 Realtime）
- `grok_cli_oauth_client_id` / `grok_cli_oauth_token_url`（默认官方 client/token 端点）
- `grok_cli_model_ids`（为未显式标注上游的兼容模型指定 CLI 路由；当前 4.5/4.6 已显式标注）
- `grok_session_identity_refresh`（默认 true，后台刷新 SSO 账号时拉取 `/api/auth/session` 学习 teamId）
- `response_store_ttl_hours`（Build stored Response 归属记录 TTL，默认 720 小时）
- `grok_egress_enabled`（默认 false；开启后走代理池 + FlareSolverr + clearance 缓存）
- `grok_egress_nodes`（代理池节点列表）、`grok_flaresolverr_url`、`grok_clearance_mode`（`manual`/`flaresolverr`）、`grok_clearance_refresh_interval`

注意：这些字段带 json tag 且不会被 `ApplyHardcoded` 覆盖；通过管理端 `/api/config` 保存后不会被抹掉。

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

模型与推理接口默认要求管理端创建的 API Key。管理端可为每个 Key 设置允许模型、每分钟请求数和到期时间；旧 Key 默认不限制这些策略。仅在已有可信上游网关负责认证时，才设置 `inference_auth_enabled=false`。

## 许可证

本仓库遵循仓库内现有许可策略。
