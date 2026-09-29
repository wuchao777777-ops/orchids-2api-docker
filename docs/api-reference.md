# API 速查

接口以 [`cmd/server/routes.go`](../cmd/server/routes.go) 为准。管理端先添加账号并创建 API Key；以下模型/推理接口需要 `Authorization: Bearer <API_KEY>`，Anthropic 客户端也可使用 `x-api-key`。只有 `anonymous_allow_ips` 显式允许的来源可免 Key；已废弃的 `inference_auth_enabled` 配置会被忽略。

## 推理与模型

| 前缀 | 适用范围 |
|---|---|
| `/v1` | 按 `model` 自动选择通道 |
| `/workbuddy/v1`、`/qoder/v1`、`/cline/v1`、`/grok/v1` | 固定通道 |

上述前缀提供 `POST /messages`、`POST /chat/completions`、`POST /responses`、`GET /models` 和 `GET /models/{id}`；统一前缀及 WorkBuddy、Qoder、Cline 前缀还提供 `POST /messages/count_tokens` 估算 token。Responses 对 Grok 使用 Build 原生实现，其他通道桥接 Chat Completions；可用 `GET/DELETE /responses/{id}` 管理已存响应，以及 `POST /responses/{id}/cancel`、`GET /responses/{id}/input_items`。Grok 和统一前缀还支持 `POST /responses/compact`。具体能力仍取决于模型与上游。

```bash
curl http://127.0.0.1:3002/v1/chat/completions \
  -H 'Authorization: Bearer <API_KEY>' -H 'Content-Type: application/json' \
  -d '{"model":"<MODEL_ID>","messages":[{"role":"user","content":"你好"}]}'
```

`GET /health` 用于健康检查；`GET /metrics` 提供 Prometheus 指标，生产环境建议只对内开放。

## 管理端

访问 `{admin_path}/` 登录。管理接口使用管理会话（或支持时使用 Basic Auth），**不能**以推理 API Key 代替管理身份。

| 接口 | 用途 |
|---|---|
| `POST /api/login`、`POST /api/logout` | 管理会话 |
| `/api/accounts*` | 账号管理 |
| `/api/keys*` | 创建、更新、禁用与删除推理 API Key |
| `/api/models`、`/api/models/refresh`、`/api/models/{id}` | 模型管理及按通道刷新 |
| `/api/config/list`、`/api/config/save` | 读取与保存配置 |
| `/api/ops/overview`、`/api/ops/runtime`、`/api/ops/alerts/rules` | 运维概览、运行信息与告警规则 |
| `/api/journal/records`、`/api/journal/diagnostics*` | 日志与诊断 |
| `/api/export`、`/api/import` | 账号备份与恢复；导出文件包含凭据，请按密钥保管 |
| `POST /api/workbuddy/login`、`POST /api/qoder/login`、`POST /api/cline/login` | 发起相应渠道官方授权 |
| `POST /api/grok/device-auth` | 发起 Grok Build OAuth 设备授权 |

`/api/token-cache/stats` 与 `/api/token-cache/clear` 是已移除的本地模拟缓存管理端点，不再提供统计或清除功能。`/api/config/save` 对 `enable_token_cache`、`token_cache_ttl`、`token_cache_strategy`、`cache_token_count`、`cache_ttl` 的显式提交返回 HTTP 400；真实上游缓存的 `cache_strategy` 与 `cache_control` 不受影响。

登录事务创建后，在返回的官方授权页面完成操作，通过对应 `GET /api/{channel}/login/{id}`（Grok：`GET /api/grok/device-auth/{id}`）轮询；同路径的 `DELETE` 可取消。管理页面已集成流程，建议直接在页面操作。Qoder、Cline、Grok 不接受手填个人 token；WorkBuddy 的凭据导入仅用于迁移。

模型刷新示例（需已登录的管理会话）：`POST /api/models/refresh`，JSON 请求体 `{"channel":"qoder"}`。只有成功读取上游目录时才同步；读取失败会保留原有目录，不回退到内置模型。不同通道的上游目录分别来自 WorkBuddy `/v3/config`、Qoder `/algo/api/v2/model/list`、Cline `/ai/cline/recommended-models`（免费推荐列表）和 Grok Build `/v1/models`。

API Key 可设置允许模型、限速和过期策略；请求报错时先检查 Key、账号状态、模型目录与上游响应。更多配置见 [配置说明](configuration.md)。

## 请求失败时客户端看到的状态

账号池无法受理请求时，状态码取决于原因，不再一律按限流返回：

| 情况 | 状态 | 说明 |
|---|---|---|
| 账号额度耗尽、并发打满、模型处于限流冷却 | `429` | 等待后重试可能成功 |
| 请求的模型不在该通道任何匹配账号的套餐内 | `404` | 套餐不会因等待改变，换模型或补账号 |
| 上游自述该模型服务当前不可用（如 Qoder `serviceAvailable:false`） | `503` | 带 `Retry-After` 头说明何时可重试 |

模型级冷却的原因随冷却一起持久化在账号的 `model_cooldown_reasons` 字段（`throttled` / `unavailable`），冷却过期时原因一并清除；选择账号时据此区分"稍后重试"与"这里不可用"。该字段是新增的并列字段，`model_cooldowns` 的结构未变。
