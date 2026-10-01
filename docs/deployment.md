# 部署注意事项

本地启动步骤见 [README](../README.md)；配置字段见 [配置速查](configuration.md)。构建需符合 `go.mod` 中的 Go 版本（当前为 **1.26.6**），使用 Redis 保存账号及配置。以下沿用当前部署工具使用的 `orchids-server` 二进制文件名；项目展示名称已改为 **API Console**，此处不是二进制改名。

```bash
go test ./...
go build -o orchids-server ./cmd/server
./orchids-server -config ./config.json
```

- 显式设置强管理密码；`debug_enabled` 保持关闭。账号凭据的 `data/credential.key`（或环境变量密钥）必须与 Redis 一同备份和恢复。
- Redis 的 `<redis_prefix>settings:config` 可覆盖文件配置，升级或修改参数后通过管理端核对实际值。
- 后端默认监听所有网卡的端口；在防火墙或反向代理上阻止直接公网访问，尤其注意 `/metrics`。`trusted_proxies` 仅填写实际代理地址。
- 多副本共享 Redis 和凭据加密密钥，并为每个副本配置不同的 `deployment_instance_id`；如需共享媒体与 egress 健康状态，请显式挂载同一媒体目录。
- 启动后检查 `/health`，再使用 API Key 请求 `/v1/models`；必要时在管理端按通道刷新模型。回归测试执行 `go test ./...`。

## 回归验证与手工探针

默认回归不启用真实上游探针：

```bash
go test ./... -cover -count=1 -p 1
go test -race -count=1 -p 1 -timeout 15m ./...
node --test web/*.test.cjs
./scripts/check-provider-registry.sh
```

渠道注册表检查使用生成器的 `-check` 模式，只比较生成结果，不会重写工作树。

Qoder、WorkBuddy 和 TLS/ALPN 手工探针需同时满足 `-tags live` 与对应显式开关；它们不属于默认 CI。只有明确需要验证真实上游时才运行，Qoder 会使用部署配置及 Redis，聊天探针可能消耗实际额度：

```bash
# Qoder：在部署主机上使用部署配置与账号
QODER_PROBE=1 go test -tags live ./internal/qoder -run '^TestLiveProbe$' -v -count=1 -timeout 20m
# WorkBuddy：OAuth bootstrap 不需要账号；聊天/目录检查还需 WB_AUTH_FILE
WB_LIVE=1 go test -tags live ./internal/workbuddy/live -run '^TestLive_StartAuthLogin$' -v -count=1
# TLS：显式指定目标；本地 HTTP/2 探针只使用进程内 httptest 服务器
TLS_PROBE_HOST=api2.qoder.sh:443 go test -tags live ./internal/util -run '^TestTLSALPNToRealHost$' -v -count=1
TLS_PROBE_H2_LOCAL=1 go test -tags live ./internal/util -run '^TestSharedTransportProtocolAgainstLocalH2$' -v -count=1
```

仓库附带 [Caddy 与 systemd 主机部署手册](../deploy/README.md) 和 [`scripts/deploy-orchids.sh`](../scripts/deploy-orchids.sh)；这是特定主机环境的示例，应用前请核对地址、路径及防火墙规则。
