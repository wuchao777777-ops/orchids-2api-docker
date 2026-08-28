# Grok 与 grok2api 对齐清单

本文以当前实现为准，用于跟踪与 `chenyme/grok2api` 的主要能力差异。

## 已完成

- [x] Grok Build、Web、Console 凭据边界与账号级模型快照
- [x] Chat Completions、Responses、Anthropic Messages JSON/SSE
- [x] Web fast/auto/expert/heavy、Console 文本目录、Provider 前缀和账号发现的动态 Build 文本模型
- [x] 非 Build Responses 的逐事件实时转换（不再完整缓冲 Chat 流）及 `max_output_tokens` 映射
- [x] Messages adaptive/enabled thinking、预算、stop sequences、output_config、MCP servers、server tools 和 encrypted signature 回放
- [x] Build/Console 流式响应缺失思考质量门控、跨账号重试、账号冷却及短回答 fail-open
- [x] 租户/模型隔离的 Prompt Cache identity、Provider 级账号粘性和 Redis encrypted reasoning replay
- [x] Build Responses compact、stored Response 查询/删除、`previous_response_id` 账号固定与 API Key 所有权隔离
- [x] Messages 文本、多模态、`tool_use`、`tool_result` 与工具选择转换
- [x] 图片生成、图片编辑、异步视频生成和本地媒体读取
- [x] Console 标准 `/videos/generations`、`/videos/edits`、`/videos/extensions`，并保留 Web 旧 `/videos` 入口
- [x] `grok-imagine-video-1.5` 生成能力、标准视频任务 API Key 所有权隔离和可信媒体下载
- [x] 视频任务元数据 Redis 持久化，已完成任务可在进程重启后恢复查询和本地内容读取
- [x] 已取得上游 `request_id` 的标准 Console 视频任务在服务重启后固定原账号续跑
- [x] 视频任务 Redis 原子租约、心跳续期、租约丢失取消和多实例过期接管
- [x] 可配置共享媒体目录、多副本实例/集群身份、集群标记和启动读写探针
- [x] 临时图片/视频输入上传、24 小时 Redis 元数据、API Key 隔离及标准视频 `file_id` 解析
- [x] Console 原生 TTS、Voice 查询、HTTP/流式 STT 和 Realtime WebSocket
- [x] OpenAI 风格 `/audio/speech` 与 `/audio/tasks` 到 Console TTS 的转换
- [x] OpenAI `/audio/transcriptions` 的 `json`、`verbose_json` 与 `text` 兼容转换
- [x] Build Device OAuth、Token 刷新、Billing 与 Web quota 同步
- [x] 推理 API Key 默认鉴权，可显式关闭以接入可信上游网关
- [x] API Key 模型白名单、Redis 原子 RPM 限流、到期时间与管理端策略编辑
- [x] Redis 账号敏感字段 AES-256-GCM 加密和旧明文启动迁移
- [x] HTTP/SOCKS5 出口池、健康反馈、粘滞与 FlareSolverr
- [x] Prometheus 指标与受保护的 pprof

## 部分完成

- [~] Provider 前缀和动态 Build 路由已完成；同一公开模型的多 Provider 自动聚合、路由级 capability/账号绑定仍待独立领域模型
- [~] 标准视频生成、编辑、延长、查询、内容读取、`file_id`、重启续跑、多实例任务租约及共享文件系统已完成；提交前中断及旧 Web 分段任务不重放，尚无 S3 等对象存储后端
- [~] 出口支持静态配置、权重和健康冷却，尚无订阅导入、账号绑定和管理端节点编排；托管出口尚不支持语音 WebSocket 租约拨号

## 尚未实现

- [ ] 同名模型多 Provider 路由自动聚合
- [ ] 模型路由的 capability、upstream model 和账号绑定数据模型
- [ ] API Key 的账号范围与金额额度（模型白名单、RPM、到期限制已完成）
- [ ] PostgreSQL 持久化及正式多实例拓扑
- [ ] 请求级 Grok 审计、客户端计费和完整媒体图库/结果资产元数据数据库（临时输入元数据已使用 Redis）
- [ ] Trojan、VLESS、Shadowsocks、VMess、Resin 等隧道和出口 Quality Guard
- [ ] React 运维控制台、媒体画廊和在线 Swagger 文档

## 安全基线

- 不要关闭 `inference_auth_enabled`，除非服务只暴露给已完成认证的可信网关。
- 关闭推理鉴权后，stored Responses 和视频任务会进入共享的 `anonymous` 所有权空间；多租户部署应保持鉴权开启。
- `data/credential.key` 或 `ORCHIDS_CREDENTIAL_ENCRYPTION_KEY` 必须和 Redis 数据一起备份。
- API Key 只在创建时返回完整值；Redis 仅保存 SHA-256 哈希索引。
