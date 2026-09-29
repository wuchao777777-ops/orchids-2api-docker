# 配置速查

从仓库根目录执行 `cp config.example.json config.json`，然后 `go run ./cmd/server -config ./config.json`。完整字段及示例值以 [`config.example.json`](../config.example.json) 和 [`internal/config/config.go`](../internal/config/config.go) 为准；不要把凭据提交到 Git。默认也会依次查找 `config.json`、`config.yaml`、`config.yml`（YAML 仅支持扁平键值）。

## 常用字段

| 字段 | 作用 |
|---|---|
| `port` | 监听端口，示例为 `3002` |
| `admin_user`、`admin_pass`、`admin_path` | 管理端账号、密码和访问路径；留空密码在启动日志中随机生成 |
| `redis_addr`、`redis_password`、`redis_db`、`redis_prefix` | Redis 连接与 key 前缀；存储固定使用 Redis |
| `credential_encryption_key_file` | 账号凭据主密钥文件，示例为 `data/credential.key` |
| `trusted_proxies` | 可信反向代理 IP/CIDR，勿信任任意客户端可访问的地址 |
| `anonymous_allow_ips` | 明确允许免 API Key 访问推理接口的来源 IP；默认空数组 |
| `debug_enabled` | 收集诊断内容，生产环境保持 `false` |
| `response_store_ttl_hours` | stored Response 记录保留小时数，示例为 `720` |
| `deployment_instance_id` | Grok 实例标识；多副本部署时为各副本设置唯一值 |
| `proxy_http`、`proxy_https` | 出站代理 |

模型与推理接口默认始终要求管理端创建的 API Key；需要免 Key 的受控来源必须显式配置 `anonymous_allow_ips`。历史配置中的 `inference_auth_enabled` 已废弃并会被忽略。Build 模型经显式路由或 OAuth 账号动态能力发现，不使用历史 `grok_cli_model_ids` 列表。

本地模拟 Prompt 用量缓存及历史估算缓存字段 `enable_token_cache`、`token_cache_ttl`、`token_cache_strategy`、`cache_token_count`、`cache_ttl` 均已废弃；旧配置中的这些字段不再生效，管理接口 `/api/config/save` 会对显式提交返回 HTTP 400。模拟缓存的 `/api/token-cache/stats`、`/api/token-cache/clear` 端点已移除。真实上游缓存仍由 `cache_strategy` 控制请求中的 `cache_control`，不属于上述本地模拟缓存。

## 生效顺序与备份

启动时加载配置文件及默认值，Redis 中若已有 `<redis_prefix>settings:config`，其保存的设置会覆盖文件；部分历史字段还会被代码固定默认值覆盖。修改配置请优先使用管理页面，重启后确认实际生效值，不要只修改本地文件。

首次启动生成的凭据加密密钥**必须和 Redis 一起持久化与备份**。也可通过 `ORCHIDS_CREDENTIAL_ENCRYPTION_KEY` 提供密钥；已有账号后切勿随意更换或删除，否则无法解密账号凭据。多副本共享 Redis 和同一密钥，并为各副本设置唯一实例 ID。

生产环境还应限制服务端口及 `/metrics` 的访问范围。部署注意事项见 [部署说明](../deploy/README.md)。

## 账号快照的刷新

自动刷新循环（`token_refresh_interval`，默认 30 分钟）会按通道重新读取账号目录与额度：Qoder 读目录与额度，WorkBuddy 读目录与**信用计量额度**，Cline 只读目录。因此管理端展示的 WorkBuddy 额度最多滞后一个刷新周期；额度耗尽时该周期内的状态为“仅免费目录模型可用”，计量接口重新显示有余额后自动恢复完整能力。手动“检查”按钮走同一条路径，并额外验证凭据。

计量接口返回的时间是上游的墙上时钟（`2006-01-02 15:04:05`），其所在时区无法从数据中判定，因此按 UTC 解析，且仅作为量级参考：免费层模型冷却设有上限，后台刷新按经过时间触发，不依赖该时刻。
