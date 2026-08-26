# Grok 与 grok2api 对齐清单

本文以当前实现为准，用于跟踪与 `chenyme/grok2api` 的主要能力差异。

## 已完成

- [x] Grok Build、Web、Console 凭据边界与账号级模型快照
- [x] Chat Completions、Responses、Anthropic Messages JSON/SSE
- [x] Build Responses compact、stored Response 查询/删除、`previous_response_id` 账号固定与 API Key 所有权隔离
- [x] Messages 文本、多模态、`tool_use`、`tool_result` 与工具选择转换
- [x] 图片生成、图片编辑、异步视频生成和本地媒体读取
- [x] Build Device OAuth、Token 刷新、Billing 与 Web quota 同步
- [x] 推理 API Key 默认鉴权，可显式关闭以接入可信上游网关
- [x] API Key 模型白名单、Redis 原子 RPM 限流、到期时间与管理端策略编辑
- [x] Redis 账号敏感字段 AES-256-GCM 加密和旧明文启动迁移
- [x] HTTP/SOCKS5 出口池、健康反馈、粘滞与 FlareSolverr
- [x] Prometheus 指标与受保护的 pprof

## 部分完成

- [~] Build 模型按账号动态发现，但只有本地 `ModelSpec` 已实现的模型会公开
- [~] 语音具有浏览器 Voice Token/工具页，尚无标准 TTS、STT、Realtime 推理 API
- [~] 视频支持创建、查询和内容读取，尚无编辑与延长
- [~] 出口支持静态配置、权重和健康冷却，尚无订阅导入、账号绑定和管理端节点编排

## 尚未实现

- [ ] Provider 前缀模型限定与同名模型多路由聚合
- [ ] 模型路由的 capability、upstream model 和账号绑定数据模型
- [ ] API Key 的账号范围与金额额度（模型白名单、RPM、到期限制已完成）
- [ ] PostgreSQL 持久化及正式多实例拓扑
- [ ] 请求级 Grok 审计、客户端计费和媒体元数据数据库
- [ ] TTS、STT、Realtime、视频编辑与视频延长
- [ ] Trojan、VLESS、Shadowsocks、VMess、Resin 等隧道和出口 Quality Guard
- [ ] React 运维控制台、媒体画廊和在线 Swagger 文档

## 安全基线

- 不要关闭 `inference_auth_enabled`，除非服务只暴露给已完成认证的可信网关。
- 关闭推理鉴权后，stored Responses 会进入共享的 `anonymous` 所有权空间；多租户部署应保持鉴权开启。
- `data/credential.key` 或 `ORCHIDS_CREDENTIAL_ENCRYPTION_KEY` 必须和 Redis 数据一起备份。
- API Key 只在创建时返回完整值；Redis 仅保存 SHA-256 哈希索引。
