# Clean Code 分层重构 — 本地验证

2026-10-04，在 Windows 本机起的隔离实例（端口 3010/3011，Redis db 9，前缀 `smoke:`）上验证。二进制由本次重构后的源码编译。实例与生产无关，结束后已清理。

## 验证了什么

全部为**本机能验证的部分**：启动、路由装配、鉴权、存储读写、管理端接口。真实上游调用一项都没有做（原因见下）。

| 项目 | 结果 |
| --- | --- |
| 服务启动（真实 Redis） | 正常，`/health` 200，四个通道均 ready |
| 无 key 访问 `/v1/messages`、`/v1/responses`、`/workbuddy/v1/responses` | 全部 **401**（不变量 1） |
| `anonymous_allow_ips` 放畸形条目后同上三个路由 | 全部 **401**，名单失效并要求 key（不变量 1 的负面用例） |
| 带 key 访问上述路由 | 400（因无账号），说明鉴权通过、卡在模型/账号层 |
| `GET /v1/responses/{id}`、`/input_items`，`POST .../cancel`（含 `/workbuddy/v1` 前缀） | 404，且响应体是 Responses 信封 `{"error":{"code":"response_not_found",...}}`，不是纯文本 404 |
| `POST /api/models/refresh?channel=workbuddy/qoder/cline` | 200，全部 `source=no_active_account`、`skipped=true`、六个计数均为 0（不变量 4） |
| 刷新后 `GET /api/models` | 0 行，确认没有发布任何模型行 |
| `/api/ops/overview`、`/api/ops/runtime`、`/api/ops/alerts/rules` | 均 200 |
| `/api/accounts`、`/api/keys` 创建与列表 | 均 200/201 |
| 运行日志 | 无 ERROR、无 WARN、无 panic |

## 没验证什么（重要）

以下**没有验证**，本次也不能声称已验证：

- **任何真实上游生成请求**。没有 Grok/WorkBuddy/Qoder/Cline 的可用账号凭据，因此桥接路径的实际调用、SSE 写出、工具调用还原、compaction 续接、严格 JSON Schema 都没有走通一次真实请求。
- 因此 Stage 1/3/4 里**改变请求构造或流式写出**的部分（`internal/httpserver` 的 SSE 写出、`internal/responses` 的流翻译与桥接、compaction 的 SSE 合成）只经过了单元测试和启动验证，**没有上游实测**。
- `scripts/verify-coding-protocols.py` 需要 `probe` 子命令对真实上游探测，同样跑不了。

CLAUDE.md 的要求是对的：本地 mock 通过 ≠ 上游真的接受了。上面这几项需要有人用真实账号补一轮。

## 顺带修掉的一个既有问题

`internal/qoder` 的 `TestProtocolControlsInWireParameters` 是**确定性 flake**，与本次重构无关（在未改动的历史提交上同样失败）。它把两次 `buildChatBodyProfile` 的原始字节串做相等比较，而该函数会在 `business.begin_at` 写入 `time.Now().UnixMilli()`——两次调用跨毫秒就失败。

- 修复前：隔离运行 10 次失败 3 次；全量跑时因两次调用通常落在同一毫秒内而通过，所以看起来是"顺序相关"。
- 修复后：比较改为解码后的 map 并剔除 `begin_at`，只断言"接受可选 cache hint 后请求其余部分不变"，并额外断言 hint 没有被转发。隔离运行 30 次失败 0 次。

## 清理

临时实例进程、隔离 Redis 数据（db 9）、临时配置文件与临时 API key 均已删除；生产服务未改动。
