# Grok 平台化改造边界与迁移顺序

本文记录关系数据库、代理管理台和媒体画廊的后续改造边界。现阶段不直接替换 Redis 或本地/共享文件系统；先稳定数据契约，再双写迁移，避免一次发布同时改变路由、计费、任务恢复和资产读取。

## 目标拓扑

| 数据类型 | 权威存储 | Redis 职责 |
|---|---|---|
| 账号、客户端密钥、模型 Route/Capability/AccountBinding | PostgreSQL | 短 TTL 读取缓存、分布式并发计数 |
| 请求审计、attempt、usage 账本 | PostgreSQL 分区表 | 异步写入队列/短期 Stream 缓冲 |
| 视频任务、媒体资产元数据、所有权 | PostgreSQL | 任务租约、进度和幂等锁 |
| 图片、视频、上传内容 | S3 兼容对象存储 | 不保存二进制 |
| egress 节点、账号绑定、健康策略 | PostgreSQL | 实时健康分、冷却和 sticky affinity |

## 建议领域表

- `model_routes`：公开模型、provider、upstream model、优先级、状态和 origin。
- `model_route_capabilities`：route 与 chat/responses/messages/image/video/voice 等能力的多对多关系。
- `model_route_accounts`：route 与账号绑定、权重、启停和覆盖策略。
- `request_audits`：request ID、API key、模型、最终状态、总耗时。
- `request_attempts`：每次 provider/account、序号、状态码、错误分类、耗时；不保存凭据和原始请求体。
- `usage_ledger`：input/output/cached/reasoning token、媒体计量、上游账单维度和幂等键。
- `media_assets`：owner、类型、对象 key、MIME、大小、hash、来源任务、状态和保留期。
- `egress_nodes`、`egress_account_bindings`：节点配置、账号亲和、健康策略及审计字段。

## 迁移阶段

1. 冻结当前 Redis JSON 契约，并给 Route、Audit、VideoJob、MediaAsset 增加明确版本号。
2. 引入 repository 接口；保持 Redis 实现不变，补 PostgreSQL 实现和迁移脚本。
3. 开启 PostgreSQL 双写与一致性指标，读取仍以 Redis 为准。
4. 按领域切换读取：先模型路由，再审计/usage，最后视频任务和媒体资产。
5. 接入 S3 兼容对象存储，使用内容 hash 幂等上传；保留旧共享目录只读回退窗口。
6. 管理台先做只读页面：路由矩阵、attempt 诊断、egress 健康、媒体画廊；验证后再开放写操作。
7. 停止 Redis 持久数据双写，仅保留缓存、租约、并发和 Stream 缓冲。

## 发布门槛

- 所有写操作具备幂等键；usage 不能因重试重复入账。
- 数据库迁移可向前滚动，至少一个版本周期内可回读旧 Redis 数据。
- 管理台写操作必须记录管理员身份、变更前后值和 request ID。
- 媒体使用短期签名 URL，数据库只保存对象 key；删除采用软删除加异步回收。
- egress 凭据单独加密，列表接口永不返回代理密码；节点探测不得携带用户账号凭据。
- 多实例压测覆盖账号并发硬上限、视频租约接管、WebSocket 长连接和 usage 幂等。

## 当前实现与下一步

当前 Redis 已承载模型 Route/Capability/AccountBinding、视频任务租约、请求审计 Stream 和 usage 字段，可作为关系数据库迁移的数据契约原型。下一阶段建议先实现 repository 抽象和 PostgreSQL schema，不同时改管理前端与媒体存储。
