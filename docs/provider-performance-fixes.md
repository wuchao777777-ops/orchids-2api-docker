# 高并发本地修复与验收（2026-10-01）

本轮只修改、测试本地源码，没有连接生产服务器，没有部署或推送 GitHub。`provider-performance-100k.md` 保留修复前历史结果。

## 后续验收：CGO、Qoder 达标与 Grok 覆盖修正

已通过 winget 安装 WinLibs POSIX/UCRT GCC 16.2.0，安装程序哈希校验成功；工具来源为 [WinLibs](https://winlibs.com/)。用户 PATH 加入 mingw64/bin，Go 用户配置设为 amd64、CGO_ENABLED=1，并配置 gcc/g++ 绝对路径。之前 Go 默认目标为 386，现在默认构建 64 位目标，支持 Windows race 检测。新终端读取新的 PATH。

Qoder 继续消除 body 的 byte/string 往返、签名中 io.WriteString 导致的复制、固定 JSON context/parameters 的 map/反射成本、未采集时的诊断 map，以及每个 header 独立的值分配；缓存 runtime 快路径只读，UUID 格式化改为定长编码，三个独立 UUID 共用一次 48 字节随机读取。中间 JSON buffer 池化并清理，最终请求 body 保持独立所有权；COSY bearer 以一次最终字符串构造。nonce、签名、客户端控制、模型上下文与重试语义均保留。

在相同 18 逻辑处理器、256 workers、256 队列、每秒计划 100,000 次条件下，Qoder 取得两轮 30 秒零错误/零丢弃结果；第二轮启用严格验收，任何错误、丢弃、未完成或低于 99% 名义完成 RPS 都令命令失败。不能仅看 Go benchmark 的 PASS。

| 条件 | 计划/完成 | 丢弃 | 完成 RPS | P95 ms | P99 ms |
|---|---:|---:|---:|---:|---:|
| GOGC=400，512 MiB 软限额，预热，30 秒 | 3,000,000 / 2,999,911 | 89 | 99,996 | 1.02 | 2.19 |
| GOGC=600，同软限额/预热，30 秒 | 3,000,000 / 3,000,000 | 0 | 99,999 | 0.86 | 1.62 |
| GOGC=600，严格验收重复 30 秒 | 3,000,000 / 3,000,000 | 0 | 100,000 | 0.85 | 1.52 |

严格轮累计分配约 14,186 B/完成请求（包括发生器样本等），GC 732 次、累计停顿 27.46 ms；测试结束 HeapInuse 66.16 MiB、HeapSys 207.69 MiB。这些是运行时堆指标，不是峰值 RSS。512 MiB 是 Go 内存软限额，不是操作系统硬限制。未把 GOGC=600 或该软限额写入服务默认值/生产配置；真实长流与诊断会改变活跃堆，需重新选择参数。

这项达标依赖 **预热及所列 GC 配置**。最新冷启动默认 GC 短测仍约 98,653 RPS、丢弃 6,242/500,000；不能宣称默认启动即稳定零丢弃。空调用控制的一轮 30 秒也丢弃了 3,738/3,000,000，表明本机调度/发生器存在波动；两轮达标不等于长期 SLO 或生产容量证明。

复跑严格 Qoder 验收：

```powershell
./scripts/provider-performance.ps1 -OpenLoop -Warmup -RequireTarget -Providers qoder -Seconds 30 -GCPercent 600 -MemoryLimit 512MiB
```

Grok 之前的 99,981 RPS 是 HTTP 子路径成绩。当前源码只保留 Build/OAuth，Web/开发者路径已移除。新的默认 Grok benchmark 覆盖 chat→Responses 归一化、请求构造/认证、响应 SSE 解析、Chat 输出转换与写入；旧测量保留为 BenchmarkProviderLocalHTTP。补测发现 Grok reader 仍预分配 64 KiB，已改为池化 4 KiB并保留 8 MiB 事件上限。**仍排除真实网络、公共路由鉴权、账号准入、Redis、质量重试等完整链路成本。**

Grok 新流处理路径默认 GC 短测约 68,516 RPS；GOGC=400、512 MiB 软限额、预热的 5 秒测试完成 488,926/500,000，约 97,661 RPS，丢弃 11,074（2.21%），P99 20.50 ms；尚未达到零丢弃目标，不能沿用旧 HTTP 成绩宣称达标。

已补充大 JSON/并发池复用、返回 body 不别名、header.Add 不破坏相邻值、COSY bearer 字节一致性、独立 UUID 和不足熵失败的回归。全量 race 和最终结果见 performance-artifacts/final-all-race-tests.txt；两轮 Qoder 达标原始结果为 qoder-30s-gc600-load.txt、qoder-strict-acceptance.txt，Grok 为 grok-full-gc400-load.txt，对照为 control-30s-load.txt。

## 以下为第一轮修复时的结果

## 已落实的修改

| 问题 | 实现与边界 |
|---|---|
| JSON 高并发崩溃 | 移除 goccy/go-json，统一标准 encoding/json；原协议、工具与签名回归通过。 |
| 过载保护 | 全局准入移到推理鉴权前；嵌套不重复占槽；显式 provider 路由独立预算，超载 503/Retry-After。统一模型路由使用全局和账号原子准入，不应用该显式路由预算。 |
| HTTP 池 | 三个通用 provider 聊天池分离，可配置连接/空闲连接上限；HTTP/2 按渠道显式开关；Grok 浏览器传输接入配置。连接参数主要限制 HTTP/1.1，HTTP/2 流并发仍受对端限制。 |
| SSE 预分配 | Cline、WorkBuddy、Qoder 和后续补测的 Grok reader 均改为池化 4 KiB，保留各自大帧上限，归还清理初始缓冲。 |
| Qoder catalog | 缓存不可变目录，模型 ID 快照变化时重建。 |
| 编码/签名 | Qoder 原地交换编码首尾第三段、签名逐字段计算；保留请求身份和 nonce。WorkBuddy 去除重复 byte/string 转换。 |
| 账号扫描 | 渠道索引缓存，底层 64 窗口选择，首窗口满才继续；原子租约最终裁决。原外层已有 64 窗口，原底层全池基准不能代表每个线上请求。 |
| Redis 计数/续期 | 50 ms 选择提示缓存与查询合并，最终获取不绕过原子限制；每 tracker 一个续期调度器，按账号批量 Lua/pipeline，不复活释放租约。 |
| 认证/Redis pool | 缓存 hash→ID 索引，每次仍读取权威 key/策略/额度；轮换/删除回归通过。无限 RPM 的 last_used 每分钟合并；RPM 和计费保持原子。池大小可配置。 |
| 审计 | 有界 4096 队列、8 MiB 字节预算；最多 128 条 pipeline，逐条统计成功/失败。仍可能满队列丢失，不能作为计费账本。 |
| 白名单 | 配置变化才解析，非法新配置拒绝，不能沿用旧放行规则。 |
| 诊断 | 每 N 次采样及同时采集预算；响应头 X-Diagnostic-Capture 标明 enabled、sampled-out、budget-exhausted。预算包含存储阶段。 |
| Flush | 首数据帧/结束事件即时；连续小帧最多合并默认 2 ms，2 KiB 提前刷，稀疏尾帧定时刷，返回前停止定时器。 |

## 配置

通过配置 JSON/配置 API 设置，默认值保守，不应直接拉满。

| 配置 | 默认/边界 | 生效 |
|---|---|---|
| concurrency_limit | 100，上限 1,000,000 | 重启 |
| provider_concurrency_limits | 如 {"qoder":256,"grok":128}，未配置不单独限制 | 运行时，显式 provider 路由 |
| redis_pool_size | 200，上限 4096 | 重启 |
| upstream_max_conns_per_host | 200，上限 16384 | 客户端/池更新 |
| upstream_max_idle_conns_per_host | 100，不超过总上限 | 客户端/池更新 |
| cline_http2_enabled / workbuddy_http2_enabled / qoder_http2_enabled | 不自动开启，需要真实兼容验证 | 客户端更新 |
| diagnostics_sample_every | 1，高流量可设置 100/1000 | 运行时 |
| diagnostics_max_concurrent | 8 | 运行时 |
| stream_flush_interval_ms | 2，最大 20，负数关闭 | 运行时 |

## 本地复测

Windows amd64，同主机每渠道独立运行，目标 100,000 次/秒持续 5 秒，共 500,000 次；固定模拟 SSE、256 workers、有界队列。延迟包含计划到达后的等待。丢弃是发生器队列丢弃，不是 HTTP 失败。

| Provider | 成功完成 | 发生器丢弃 | 完成 RPS | P95 ms | P99 ms |
|---|---:|---:|---:|---:|---:|
| Cline | 500,000 | 0 | 99,988 | 4.17 | 8.50 |
| WorkBuddy | 499,974 | 26 (0.0052%) | 99,988 | 6.99 | 12.42 |
| Qoder | 458,621 | 41,379 (8.28%) | 91,669 | 20.73 | 48.96 |
| Grok | 499,916 | 84 (0.0168%) | 99,981 | 3.35 | 7.23 |

已完成调用错误全部为 0，未出现原 JSON 崩溃。基线 Cline 96,036 RPS、WorkBuddy 69,831 RPS；Qoder 原版崩溃，初步候选 49,490 RPS。当前 Qoder 微基准 25,969 B/op、206 allocs/op，初步候选约 38,004 B/op、254 allocs/op。

底层 4096 账号选择从约 357,069 ns/op、478,614 B/op 降至 4,371 ns/op、2,394 B/op；首窗口有容量时成立，全满/仅末端可用仍检查后续窗口。

第一轮验证：Go 全量测试通过；前端 141/141 通过；linux/amd64 CGO=0 编译通过；diff 检查通过。新增回归覆盖目录失效、key 轮换/删除、原子并发、批量续期、过载释放、嵌套准入、白名单失效、诊断预算和流刷新。当时缺少 CGO 工具链；现已安装并补跑竞态检测，见上文后续验收。

第一轮原始输出位于 performance-artifacts：implementation-open-loop.txt、implementation-selection.txt、qoder-implementation-bench.txt、implementation-full-tests.txt、implementation-web-tests.txt。`scripts/provider-performance.ps1 -OpenLoop` 测量当前源码。旧 -Candidate 已移除，因为历史 overlay 与当前测试的内部类型不兼容。

## 容量边界

第一轮覆盖代码热点，当时 Qoder 未达零丢弃、Grok 只测 HTTP 子路径；后续 Qoder 条件达标和 Grok 流处理补测已列于本文开头。所有这些本地模拟测试均没有完整真实 TCP/TLS、Redis 网络、鉴权计费、长流、模型计算、故障重试及集群成本，不能证明完整网关 100k RPS。

100k 新请求/秒且平均持续 10 秒意味着约百万在途请求，需要多机、上游配额和网络支撑。下一阶段必须在独立非生产 Linux/Redis/模拟上游环境逐级提升负载，压测机与服务机分离，验证真实路由、工具、长流、重试放大、连接等待、续期和审计丢失，运行 30–60 分钟稳态。不能仅凭短测提高连接和准入上限。
