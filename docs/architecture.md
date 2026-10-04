# 架构概览

`cmd/server` 只负责启动服务与注册路由（`routes.go` 是唯一装配点），业务逻辑已经下沉：`internal/modelrefresh` 负责模型目录发现与对账，`internal/refresh` 负责定时刷新，`internal/alerting` 负责告警。`internal/handler` 处理 WorkBuddy、Qoder、Cline 的消息与聊天请求；`internal/grok` 处理 Grok Build OAuth 的原生 Messages、Chat Completions 和 Responses。统一 `/v1` 前缀由 `internal/dispatch` 按模型分发，通道前缀则固定对应账号池；非 Grok 的 Responses 通过 `internal/responses` 的 Chat 桥接。

请求经 API Key 校验后按通道选择模型与账号，调用上游并转换响应。`internal/loadbalancer` 管理账号选择与冷却，`internal/store` 将账号、模型、API Key 和配置保存在 Redis。凭据加密密钥单独保存在文件或环境变量中，不存入 Redis。

OpenAI Responses 协议的实现（线类型、SSE 编解码、子资源、compaction 与 Chat 桥接）位于 `internal/responses`，Chat Completions 线类型位于 `internal/chatwire`，通用 HTTP/SSE 读写位于 `internal/httpserver`，HTTP 传输位于 `internal/httpclient`。

各通道的模型刷新从已授权账号读取上游目录：WorkBuddy `/v3/config`、Qoder `/algo/api/v2/model/list`、Cline `/ai/cline/recommended-models`、Grok Build `/v1/models`。没有可用账号或读取失败时不以本地内置清单替代上游目录。

入口用法见 [README](../README.md)，接口见 [API 速查](api-reference.md)。
