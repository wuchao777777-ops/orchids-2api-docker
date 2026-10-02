# 真实上游编程协议验证（2026-10-02）

WorkBuddy、Cline、Grok 的所测基础协议、工具与压缩续接通过；WorkBuddy 严格 JSON Schema 未生效，Qoder 因上游排队 429 未完成生成验证。此次结果来自真实账号调用，不能推广到所有模型与完整客户端行为。

## 验证环境

在目标服务器运行当前工作区构建的临时实例，监听 `127.0.0.1:13002`，使用独立 Redis 前缀和临时网关 Key。从生产读取账号快照，清除副本的刷新凭据，关闭临时实例后台刷新与更新；未修改生产账号、配置和模型状态。真实请求消耗上游用量。

生产服务为 `orchids-2api.service`，版本 `v1.0.11`、提交 `5a62a11`，端口 3002。生产未包含本次新增代码，未替换二进制或重启服务。验证器使用 HTTP 协议请求，没有启动完整 Codex、Claude Code、WorkBuddy、Qoder 或 Cline 客户端。

## 实测结果

| 渠道与模型 | 文本 / SSE / Claude Messages | 单工具与结果续轮 | 双工具 SSE 与两路结果续轮 | compact 与续接 | 严格 JSON Schema |
| --- | --- | --- | --- | --- | --- |
| WorkBuddy · `glm-5.3-flash` | 通过 | 通过 | 修复后通过 | 通过 | 未生效 |
| Cline · `cline-free/deepseek-v4.1-flash` | 通过 | 通过 | 修复后通过 | 通过 | 所测样本通过 |
| Grok · `grok-composer-2.5-fast` | 通过 | 通过 | 通过 | 通过 | 所测样本通过 |
| Qoder · `qwen3.8-flash`、`glm-5.3-flash` | 生成返回 429 | 未验证 | 未验证 | 未验证 | 未验证 |

WorkBuddy 的 `gpt-5.6-luna` 额外严格 schema 测试也未生效。测试故意令提示要求 `PROMPT_WRONG`，而严格 schema 只允许 `SCHEMA_RIGHT`：两个 WorkBuddy 模型均返回前者；Cline 和 Grok 返回后者。这只能确认所测模型和请求的行为，不能宣称整个渠道均支持或均不支持结构化输出。

Qoder 模型目录可读取，但两次 `qwen3.8-flash` 和一次备用模型生成均返回 `429 upstream_queue`。因此判为上游当前不可用，不能作为协议兼容通过或代码缺失的证据。

WorkBuddy、Cline 的通用 compact 返回 `response.compaction`，引用续接保留测试标记；跨网关 Key 使用返回 400。Grok 原有 compact 返回 `response`，包含 compaction item，续接保留标记。通用渠道对不支持的 include 和托管工具明确返回 400。

WorkBuddy、Cline 各两次携带 prompt_cache_key 的请求返回 200，但没有可确认命中的 cached_input_tokens；接受字段不等于缓存命中。Grok 普通响应观测到缓存用量，不代表通用渠道的缓存键已生效。

## 发现并修复的问题

首次双工具 SSE 验证中，WorkBuddy 和 Cline 返回 `response.failed` / `upstream_stream_error`。增加输出预算后仍失败。定位到共享 OpenAI SSE 适配器：工具开始与参数增量将 index 固定为 0，使两个不同工具调用合并到同一索引。

已修复快速路径和普通路径，保留原始内容块索引，并增加索引与完整路由双工具回归测试。修复版真实复验中，两渠道均输出两个独立 call_id、完整参数并产生 completed 事件；提交两路工具结果后的续轮也通过。Grok 对应测试通过。

修复后 `go test ./...` 全部通过。原始记录保留首次失败和修复后成功，判断最终状态应使用后续复验结果。

## 生产保护与清理

验证前后生产 MainPID 均为 `164706`，状态 active，运行中二进制 SHA-256 均为：

`d47dc5a1fdb87d0700d2c77efd7611cdc205484d13f5dc2c64a22edbe675a3c2`

最终临时修复版二进制 SHA-256：

`fb596ce76a16c5887ee3aae17313a9a25205978ac600fe12399556122a87e81d`

临时进程已停止，端口 13002 无监听，295 个隔离 Redis Key 已删除，临时凭据配置与日志已删除。2026-10-02 17:00:35（北京时间）检查生产 health 为 ok、provider ready，版本仍为 v1.0.11。新增能力和 SSE 修复尚未发布到生产。

## 可复核材料与边界

- 脱敏真实调用原始记录仅保留在本地，不随源码发布；本报告保留结果摘要与验证边界。
- [验证脚本](../../scripts/verify-coding-protocols.py)：隔离快照、探测、复验和清理流程。
- [协议实现与限制](../coding-protocol-capabilities.md)：字段映射与拒绝策略。

本轮没有验收 WebSocket、异步 background、所有模型、图片输入、长时间上下文、跨实例重启恢复或完整客户端会话。Qoder 需要上游排队恢复后再测；WorkBuddy 严格 schema 目前不应对外声明为已支持。
