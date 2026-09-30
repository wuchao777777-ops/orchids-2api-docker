# 自动发现与在线升级

管理后台侧栏的版本按钮打开“版本与升级”。登录后自动检查 GitHub 最新稳定发行版，20 分钟缓存/检查一次；“检查更新”可强制刷新。网络失败显示检查失败，不作为“已是最新”的依据。安装需要管理员点击“升级并重启”，不会无人值守安装。

## 发布来源与构建

默认仓库为 `zhangdailin/API-Console`，编译期可覆盖 `internal/buildinfo.Repository`；浏览器不能指定下载 URL 或仓库。正式版本来自 `vMAJOR.MINOR.PATCH` 标签；比较遵循 SemVer，忽略构建元数据并正确处理预发行版本，自动发现只接受稳定 Release。

`.github/workflows/release.yml` 将 Version、Commit、Date 和 BuildType=release 编译进程序，构建后用 `--version` 验证。嵌入的前端随二进制一起发布。当前发行平台是 linux/amd64，必须提供：

- `orchids-server-linux-amd64`
- `orchids-server-linux-amd64.sha256`，标准 sha256sum 格式
- `orchids-server-linux-amd64.build-info.txt`，包含 version、commit、built_at、goos、goarch

手动 workflow_dispatch 默认 dry_run，构建并验证，但不发布 Release。推送正式标签按原工作流发布。本次在线升级与前端改动随 v1.0.3 一起提交；发行产物由该标签触发的工作流构建，只有工作流成功发布后才能用于在线升级。

## 运行环境

在线替换仅在显式启用的 root Linux systemd 服务上提供，检查 ExecStart 与当前真实可执行路径、MainPID、重启策略和目录写权限。Windows、无版本 source 构建、手工前台运行、普通 Docker 均保留版本检查，但禁用安装；Docker 应更新镜像并重建。

在目标应用服务的 drop-in 中配置，重载并重启后生效：

```ini
[Service]
Environment=ORCHIDS_UPDATE_ENABLED=true
Environment=ORCHIDS_UPDATE_SERVICE=orchids-2api.service
```

服务须为持久 systemd 单元，具备 Restart=always 或 on-failure，以及运行 `systemd-run` 的权限。升级守护进程在独立 transient unit 运行，主服务重启不会杀死它。不要把应用本身设置为失败后自动回收的临时单元。

## 操作与验证

管理员接口 `/api/system/version`、`check-updates`、`operation` 为 GET；`update` 和 `rollback` 为 POST，要求 application/json、同源浏览器请求和 Idempotency-Key。update 的 body 为 `{"version":"v1.0.3"}`，rollback 为 `{}`。接口受现有管理员认证及操作审计保护。

任务异步执行，POST 返回 202 只表示已接受。幂等键和最后一次操作状态保存在磁盘；flock 串行化同一部署的系统操作。不是跨主机滚动升级协调器。下载最多 15 分钟，独立于浏览器请求取消；刷新页面会继续读取任务状态，不自动重发 POST。

下载仅允许当前仓库的 GitHub HTTPS 产物以及受限 GitHub CDN 重定向，二进制最大 150 MiB，校验/构建信息各最大 64 KiB。必须匹配 SHA-256、ELF 平台架构和构建信息中的目标版本、commit。使用原始二进制，不解压归档。

在可执行文件目录下的 `.orchids-updates` 创建私有目录与持久操作状态；复制并同步旧程序，先启动独立守护进程，再在同文件系统原子 rename 覆盖目标。旧程序副本保留，没有先挪走可执行路径再挪入新程序的空缺。仍不将全部文件和状态写入描述为跨文件崩溃事务。

守护进程执行 systemctl restart，最长 90 秒检查 systemd MainPID 的 `/proc/PID/exe` SHA-256，以及 `/health` 返回的版本、commit、status。全部匹配才将状态设为 complete。新版本不通过则恢复旧程序并再次重启、验证；成功标为 rolled_back，恢复也失败则 recovery_failed，明确要求人工检查。

失败恢复前清除 systemd failed/start-limit 状态。手动回退使用最近一次成功操作前的本地备份，校验后复用相同替换/重启/验证流程。备份留在私有操作目录，未自动删除。

程序回退不执行数据或配置回退。本项目现有 Redis 和凭据文件不会被升级删除；未来发行若含不兼容数据迁移，需要独立的数据备份与恢复方案。

## 人工恢复

先查看 `/api/system/operation` 和 `journalctl -u orchids-update-<operation-id>`。若为 recovery_failed，使用状态中的 backup 路径核对 previous_sha256，再停止应用服务，复制该备份到目标同目录的临时文件并 chmod 0755、rename 覆盖；执行 systemctl reset-failed 和 restart，最后核对 `/health` 的 build、MainPID 产物哈希及业务接口。保留操作目录和备份以供排查。

## 验证范围

包含 SemVer、缓存错误状态、下载来源/大小/校验、HTTP/跨站保护、Linux flock/幂等、替换故障、目标验证和自动回退测试；前端测试检查未知状态、能力限制、操作互斥和幂等请求。另用隔离 systemd 服务演练实际升级、手动回退和新程序启动崩溃后的自动恢复，演练不连接正式 Redis。

## 本次上线记录

2026-09-30 已部署到 47.79.238.254（https://us1.daige.tech/admin/）。运行版本 v1.0.2+local.updater.20260930，build_type=local，commit=0c2abdd-dirty，明确区分未提交工作区与正式 Release。比较基线为当前已有稳定发行版 v1.0.2，构建元数据标注本次本地改造；不宣称本地代码等同该标签。

运行文件 SHA-256：efa5ac8c4a25f89af7503894f01f566f8c916e4ee1f00ca5862ee6cce450f11a。备份：/opt/orchids-2api/backups/updater-20260930-123111。通过 root systemd drop-in 启用在线更新，配置文件与凭据文件保留。

126 项前端测试通过，Windows amd64 后端全量测试通过，Linux 升级专项测试通过；真实浏览器验证版本窗口及七页浅色/深色手机布局。隔离持久 systemd 服务完成真实升级、手动回退和新进程启动即退出后的自动恢复演练；临时服务回收问题已通过持久单元约束及 reset-failed 处理。演练服务随后停止并移除。

正式服务认证后验证版本、升级能力、GitHub 发现、操作状态、七个页面、22 个静态资源哈希及列表/运维/日志接口，本地和公网健康正常。GitHub 最新为 v1.0.2，has_update=false；没有下载安装该历史产物。未来新稳定标签必须先发布对应的新构建，才能在线升级。
