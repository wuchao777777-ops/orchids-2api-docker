# Provider 高请求量本地评估（2026-10-01）

> 历史基线：后续代码修复、Grok 流处理补测和 CGO 竞态检测结果见 [provider-performance-fixes.md](provider-performance-fixes.md)。下文“未修改业务源码”“候选尚未落地”等表述只描述初次评估时的状态。旧 overlay 与当前测试类型已不兼容，保留供审查历史实验；当前重跑使用更新后的脚本。

基线提交：762d09d102d36477913ce8549e7ec5d4fd88cdb7。没有连接生产服务器，没有调用真实上游，没有使用真实账号，没有部署或推送业务代码。新增内容仅为本地测试工具、编译覆盖候选文件和结果文档。

## 结论

现有系统不能据此认定支持每秒 100,000 个完整推理请求。发现的首要风险是高并发 JSON 编码崩溃，其次是大量瞬时分配、账号池全量扫描/Redis 命令放大、HTTP/1.1 连接上限、同步认证存储和逐条审计写入。要达到目标，需要优化热路径并进行集群容量设计，不能只提高并发配置。

## 测试边界与方法

- 本机 Intel Core Ultra 5 125H，14 核/18 逻辑处理器，约 31.5 GiB RAM，Windows。
- go1.26.6；初始 Go 可执行文件为 windows/386，正式结果均显式设置 GOARCH=amd64。
- 所有模拟响应由内存 RoundTripper 返回；无 TCP/TLS、真实 DNS、上游排队及额度成本。
- Cline、WorkBuddy、Qoder 跑实际 SendRequestWithPayload：构造请求、凭证快路径、协议编码、响应 SSE 解析和回调。短提示 hello、一个文本块 ok，无工具调用/长历史，诊断关闭。
- Grok 跑 CLIClient.request：OAuth 已有效、头部构造、超时管理及响应体读取。**未包含 Grok 完整 Chat/Responses handler、SSE 转换、账号选择和 scoped rate limit。Grok 的值不能和其他三个 provider 当作同等完整路径比较。**
- 固定到达率：每秒 100,000，持续 5 秒，共计划 500,000；256 worker，队列容量 256。满队列到达丢弃，禁止无限队列。
- Windows 调度器不能保证每个微秒精确到达；按累计时间补发到期请求，可能有微突发。
- Dropped 是本地发生器未被测试执行器接纳的到达，不是生产返回 HTTP 429；Errors 是已接纳调用的错误。
- 延迟从计划到达时刻到适配器完成，包含本地排队/调度。P95/P99 分母仅为已接纳并完成的调用；丢弃不进入延迟样本。
- RPS = 成功完成数 / 到达阶段加排空时间。单轮 5 秒是筛查，非长期稳定性证明。
- 认证/Redis/日志/计费/诊断完整链路没有实测。下面共享瓶颈属于源码分析，单独的账号选择数据属于内存 tracker 微基准。
- 首轮和账号选择基准短暂重叠，首轮不作为最终吞吐表；独立重跑的单包原始文件用于下表。

## 固定到达率原版结果

| Provider/路径 | 计划到达 | 成功完成 | 丢弃 | 完成 RPS | P95 ms | P99 ms |
|---|---:|---:|---:|---:|---:|---:|
| Cline 适配器 | 500,000 | 480,384 | 19,616 (3.92%) | 96,036 | 15.14 | 24.36 |
| WorkBuddy 适配器（独立重跑） | 500,000 | 349,211 | 150,789 (30.16%) | 69,831 | 23.41 | 42.29 |
| Qoder 适配器 | 计划 500,000 | 未完成 | 无有效统计 | 进程崩溃 | 无 | 无 |
| Grok CLI HTTP 子路径 | 500,000 | 500,000 | 0 | 99,998 | 2.26 | 5.47 |

已完成轮次 Errors=0，但存在丢弃，不能称为“100%成功”。Qoder 多次在 go-json 编码路径 panic 或 Go heap bad pointer；WorkBuddy 首轮也出现堆指针崩溃，独立重跑成功。保留原始崩溃堆栈。该现象首先证明本地 Windows/Go1.26.6/依赖组合存在压力下稳定性问题；尚未在隔离 Linux 重现，不能直接断言生产同样崩溃。

## 微基准分配量

18 个逻辑处理器，RunParallel；ns/op 是总墙钟时间/N，而非单请求端到端延迟，也不直接等于固定到达率吞吐。

| Provider | 原版 bytes/op | 原版 allocs/op | 候选 bytes/op | 候选 allocs/op |
|---|---:|---:|---:|---:|
| Cline | 74,208 | 94 | 11,768 | 106 |
| WorkBuddy | 77,993 | 115 | 15,345 | 129 |
| Qoder | 100,708 | 201 | 约 38,193（剖析轮） | 254 |
| Grok CLI HTTP 子路径 | 6,280 | 87 | 未修改 | 未修改 |

候选是组合实验：将三个 provider 的 request/stream/client/catalog 中 go-json 导入替换为 encoding/json，并将 SSE 初始 scanner 缓冲从 64 KiB 改为 4 KiB。最大帧长度保持原来的 8/16 MiB。没有对两个改动单独归因。

按原版字节/请求线性估算，100,000 RPS 会产生 Cline 7.42 GB/s、WorkBuddy 7.80 GB/s、Qoder 10.07 GB/s 的分配流量；这是 GC 分配压力估算，不是 RSS 或实际达到该吞吐的测量值。

## 候选编译覆盖结果

业务源码未修改，通过 go test -overlay 编译。

| Provider | 成功完成/500,000 | 丢弃 | RPS | P95 ms | P99 ms |
|---|---:|---:|---:|---:|---:|
| Cline | 497,134 | 2,866 (0.57%) | 99,417 | 8.32 | 15.68 |
| WorkBuddy | 487,396 | 12,604 (2.52%) | 97,458 | 12.28 | 26.85 |
| Qoder | 247,789 | 252,211 (50.44%) | 49,490 | 37.95 | 70.14 |

三者已完成调用错误数为 0，候选 provider 包回归测试通过。短轮不崩溃只支持候选值得继续验证，不等于证明长期稳定性或所有消息兼容性。

## 需要修改的地方（按优先级）

### P0：稳定性和过载保护

1. JSON 热路径：internal/qoder/request.go:306；Cline/WorkBuddy 的请求序列化同样使用 go-json。先在隔离 Linux 和目标 Go 版本复现；优先验证标准 encoding/json 或稳定的替代编码器，给高压力回归保留并发冷启动和 GC 场景。不能仅为吞吐忽略进程崩溃。
2. 全局/provider/account/API key 的分层准入：限制同时在途请求、队列长度和等待时间，超载及时返回 429/503；监控排队与拒绝。目前的可用账号和 upstream 配额必须成为容量约束，不能无限发请求/自动重试放大压力。
3. HTTP 池：internal/util/http_pool.go:90，MaxConnsPerHost=200，HTTP/2 为可选（Qoder 默认路径可为 HTTP/1.1）。同 proxyKey/timeout 的 client 共享池，多个账号不等于各有 200 条。对于 HTTP/1.1 且持续 10 秒的请求，200 条连接理想完成上限约 20 RPS/host/pool。应配置 provider 独立池、可控连接上限和获取连接等待观测；在协议允许时验证 HTTP/2 多路复用及每连接 stream 上限。不能简单改为无限连接。

### P1：CPU、分配和存储放大

4. SSE 缓冲：internal/cline/stream.go:285、internal/workbuddy/stream.go:79、internal/qoder/stream.go:261。4 KiB 起步按需增长或容量有上限的 buffer pool；避免每条流预分配 64 KiB。保持大帧和分片工具调用的边界测试。
5. Qoder 模型目录：internal/qoder/client.go:598。每次 loadCatalog 都 catalogFromIDs 并 newCatalog，应在 account client 中缓存不可变解析结果，账号模型目录变化时重建。候选分配剖析 catalogFromIDs 累计约 15.37%，newCatalog 自身约 8.21%（累计包含重复，不能简单相加）。
6. Qoder body/sign：internal/qoder/request.go、body.go。candidate alloc_space 显示 attemptChat 累计约 57.47%，readSSE 累计约 16%，signRequest 自身约 6.14%，EncodeBody 累计约 8.21%。减少 map/interface 热路径、重复 string/byte 转换和多份编码缓冲。认证/signature 不应缓存请求级签名或省略 nonce，必须保留协议正确性。
7. 账号选择：internal/loadbalancer/loadbalancer.go:425 全量遍历；内存 tracker 顺序微基准：
   - 16 账号：935 ns/op、664 B/op；
   - 256 账号：13,007 ns/op、9,569 B/op；
   - 4096 账号：357,069 ns/op、478,614 B/op。
   4096 池在目标 100k RPS 下仅此步骤线性估算约 35.7 CPU秒/秒、47.9 GB/s 分配。按 provider/model/tier 预索引候选池，再以 P2C/小样本择优或分层队列替代全扫；最后仍用权威原子租约校验，保持权重、公平与排除语义。
8. RedisConnTracker.GetCounts：internal/loadbalancer/conn_tracker.go:243 每个候选账号两条命令。4096 账号×2×100k RPS=约 8.19 亿 Redis 命令/秒的理论放大；pipeline 只减少往返，不减少命令数。统计采样/缓存，选中后原子获取租约。internal/loadbalancer/conn_tracker.go:315 每租约 goroutine+ticker 改为分片批量续期调度，避免百万在途租约同时有百万续期协程/计时器。
9. 认证：internal/store/store.go:913 每请求 GetApiKeyByHash，并在无限 RPM 情况写 last_used（949）；internal/store/redis_store.go:438 池为 200。拆开认证策略快照与计数，短缓存配可靠失效/吊销，last_used 合并更新；RPM、余额预留和结算保留原子性，不能用本地缓存绕过跨节点计费。
10. 审计：internal/audit/audit.go:115 256 队列，183 单 worker 逐条 XADD。100k RPS 下满队列可能大规模丢日志；改批量 pipeline、有界队列、可观测丢失、异步持久化或专用消息总线。请求指标和计费不可依赖 best-effort 审计队列的成功率。
11. 白名单：cmd/server/routes.go:67 每请求 NewAnonymousAllowlist，应配置变更时解析、请求读取不可变快照。
12. 诊断：internal/middleware/observability.go:123 按每个推理请求采集并 Save；高流量应采样、预算限制、按请求 ID 定向捕获及错误采样。更详细诊断不能默认每次都完整存储报文。
13. streaming 输出按帧 Flush 会放大写系统调用；考虑小时间窗/字节阈值合并，并保持首 token 及时发送及正确的取消传播。需要 TCP 实测，当前内存 callback 无法量化收益。

### P2：证明集群能达到目标

若每秒 100,000 新模型请求，平均 10 秒，Little 定律估算约 1,000,000 在途；仅 64 KiB reader 就约 61 GiB，不含 goroutine、TLS、响应缓存、诊断、连接和操作系统开销。平均请求+输出若 10 KB，应用载荷约 1 GB/s（8 Gbit/s），不含协议开销。这是容量假设，需真实分布校准。

必须区分“每秒 10 万新请求”和“10 万条同时连接”，以及总集群目标和单 provider 目标。此次按每个 provider 分别 100k 新请求/秒发起筛查。后续验收应在隔离 Linux 测试集群，用真实 Redis 测试实例、模拟上游 HTTP/1.1/HTTP/2、真实服务路由/认证/计费/审计；压测机和服务机分离，1k→10k→30k→100k 梯度；长流10/30/60秒、工具、长上下文、断流、401/429/503、Redis 故障、30–60分钟稳态。记录实际发出/完成/拒绝/发生器丢弃、TTFT和端到端P50/P95/P99、CPU/RSS/分配/GC、Redis命令/延迟、连接等待、租约续期、审计丢失。实际生产账号/限额不参与本轮。

## 重跑

见 scripts/provider-performance.ps1。原版、候选和原始堆栈在 performance-artifacts 及本目录结果 txt 中；pprof 记录 Qoder 候选 CPU 和 alloc_space。候选覆盖源文件可审查但尚未写入业务源码。原版 Qoder 的压力命令可能按已发现问题崩溃，退出码必须保留，不能当成功。
