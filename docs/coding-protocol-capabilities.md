# Codex / Claude Code 协议补齐

本次修改保持 Go 与现有渠道架构。客户端协议和上游能力分开判断；本地 mock 验收不等于真实上游已接受或执行参数。

## 路由与能力

| 能力 | WorkBuddy | Qoder | Cline | Grok |
| --- | --- | --- | --- | --- |
| Responses 文本、SSE、函数工具调用与结果续轮 | Chat 桥接（每个事件带递增 sequence_number） | Chat 桥接（每个事件带递增 sequence_number） | Chat 桥接（每个事件带递增 sequence_number） | 原生 Responses |
| store / previous_response_id / 资源查询 | 共享存储 | 共享存储 | 共享存储 | 原生及共享记录 |
| 独立 compact 与压缩历史续接 | 网关摘要引用（`object: response.compaction`） | 网关摘要引用（`object: response.compaction`） | 网关摘要引用（`object: response.compaction`） | 已有原生与密封摘要链路（`object: response.compaction`） |
| text.format / response_format | 转为 Chat response_format | 转为 parameters.response_format | 转为 Chat response_format | 保持已有原生适配 |
| prompt_cache_key | 传递到 Chat 请求 | 明确拒绝 | 传递到 Chat 请求 | 保持已有会话适配 |
| include / 托管工具 / MCP servers | 明确拒绝 | 明确拒绝 | 明确拒绝 | 按已有原生适配和上游能力处理 |
| text.verbosity | 明确拒绝 | 明确拒绝 | 明确拒绝 | 受原生上游限制 |
| 外部 encrypted reasoning / compaction | 明确拒绝 | 明确拒绝 | 明确拒绝 | 按已有原生状态策略处理 |
| WebSocket / background=true | 未实现 / 拒绝 | 未实现 / 拒绝 | 未实现 / 拒绝 | 未实现 / 受已有原生链路约束 |

`/v1` 按模型选择渠道；`/workbuddy/v1`、`/qoder/v1`、`/cline/v1` 固定渠道。统一入口的 `/responses/compact` 也按模型分流。

## 字段传递

通用 Handler 与共享 UpstreamRequest 承接 `text`、`response_format`、`include`、`prompt_cache_key` 和桥接携带的托管工具。JSON Schema 保留 schema、name、strict；Responses 的平铺 text.format 转为 Chat 的嵌套 json_schema。Claude `output_config.format` 使用相同转换。metadata 保留在桥接请求及 Responses 输出。

WorkBuddy / Cline 补充显式 max_tokens、temperature、top_p、stop 和 parallel_tool_calls；Qoder 保留已有 generation controls，并加入 response_format 的 parameters 映射。

字段能出现在上游请求只证明网关没有丢失字段，不能证明模型遵守严格 schema 或实际命中缓存。2026-10-02 真实账号验证中，WorkBuddy 两个模型均未遵守冲突提示下的严格 schema；Cline、Grok 的所测样本通过。Qoder 因上游排队 429 未完成生成验证。WorkBuddy / Cline 接受 prompt_cache_key，但未返回可确认缓存命中的用量，因此不增加缓存命中的能力声明。

### 严格 JSON Schema

`strict: true` 的 `response_format` / `text.format` 在 WorkBuddy、Qoder、Cline 上现在由网关保证，不再只依赖上游是否支持该字段：

- 网关把压缩后的 schema 作为**首个** system 块插入上游请求（附加在 `response_format` 之上，不替代它），因此渠道截断长 system 数组时保留的仍是形状要求。
- 模型回答完成后，网关用同一份已编译 schema 校验答案，不匹配则以 `schema_mismatch`（502）失败，不再用 200 返回调用方无法解析的内容。
- 校验器只覆盖 OpenAI `strict: true` 接受的子集（type 含 `null` 联合、enum、const、required、additionalProperties、items、minimum / maximum、date-time / uri、`#/$defs`）。未实现的关键词视为满足，不会因为校验器不完整而拒绝合法答案。
- 只有 `strict: true` 才会校验；`strict: false` 和 `json_object` 是给模型的提示而非契约，行为完全不变。答案外的 ``` 代码围栏和前后散文会被剥离后再校验。

没有等价传输能力的字段在调用上游前返回 400，不再静默忽略。通用桥接拒绝非 function 工具声明；已有 custom tool 历史输入以包含 input 的 JSON 函数参数保留，不宣称支持生成原生 custom_tool_call 事件。未知历史 item、缺少 call_id 和外部不透明推理状态也明确报错。工具结果中的结构化内容以 JSON 保存，不使用 Go 的 map 文本表示。

## 网关 compact

通用渠道 `/responses/compact` 发起一次无工具的摘要请求，保留用户目标、约束、文件路径、已完成工作和待办。只接受完整成功且非空的摘要，失败或截断时不签发引用。非流式与流式都返回 `object: response.compaction`（此前返回 `object: response`，检查该字段的客户端会误判为普通回答）。流式返回带 compaction item 的 Responses SSE 事件。

Chat 桥接的 Responses 流为每个事件写入从 0 开始、逐次加一的 `sequence_number`，客户端可用 Last-Event-ID 续接。

`encrypted_content` 字段中的 `bridge_compact_v1.<随机ID>` 是网关存储引用，不是上游推理密文。摘要存入现有共享 response store，受网关 Key、模型和入口渠道约束；不复用上游账号签名。下一轮将该引用展开成 developer 摘要消息，保留后续输入。引用随 response store TTL 过期，跨 Key、模型、入口渠道或丢失状态时明确失败。

使用 `/v1` 压缩的历史应继续使用 `/v1`；固定渠道入口同理。共享 Redis 支持跨实例续接；没有共享存储时，只支持同一进程生命周期内的引用。摘要是有损上下文压缩，需要模型生成，会消耗一次请求的实际用量；不执行用户工具。

## 验证边界

新增验证涵盖真实注册路由与网关 Key 鉴权、模型分流、共享 Handler、渠道请求构造、显式零值、compact 输出（`object: response.compaction`）与续接、跨调用方/模型/渠道隔离、缺失引用和失败摘要拒绝、桥接 SSE 的 `sequence_number`、以及严格 schema 的通过/拒绝/非严格不受影响。Grok 既有原生协议测试一并运行。

本次三项修复（桥接 SSE 的 `sequence_number`、compact 的 `object: response.compaction`、严格 JSON Schema 强制）均由本地单元测试与 mock 上游覆盖，**尚未对真实上游复验**；验证使用协议 HTTP 请求，并非运行完整 Codex / Claude Code 客户端。

2026-10-02 已在服务器的临时隔离实例调用真实上游，完成 WorkBuddy、Cline、Grok 的文本、SSE、函数工具调用与结果续轮、compact 与续接、Claude Messages 验证。真实双工具 SSE 测试发现并修复了共享适配器将工具索引固定为 0 的问题，修复后 WorkBuddy / Cline 双工具及结果续轮复验通过。完整记录见 [真实协议验证报告](verification/2026-10-02-coding-protocol-live.md)。

生产二进制未替换，服务未重启；新增能力与修复尚未部署生产。临时实例和隔离 Redis 数据已清理。验证使用协议 HTTP 请求，并非运行完整 Codex / Claude Code 客户端。WebSocket、异步后台执行、官方 OpenAI / Anthropic 账号接入和精确 count_tokens 不在本次实施范围。
