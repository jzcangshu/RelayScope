# 本地开发

[English](development.md) | **中文**

项目的大型工具链与缓存都保存在仓库内部，并被 Git 忽略。

## Go 工具链

经过验证的本地工具链是 Windows amd64 版 Go 1.26.5。如果已下载，可解压到 `.tools/go`；辅助脚本会优先使用该副本，否则回退到 `PATH` 中的 Go 安装，因此干净的克隆无需提交工具链即可使用。

预期 SHA-256：

```text
97e6b2a833b6d89f9ff17d25419ac0a7e3b482a044e9ab18cdef834bd834fd38
```

## 命令

```powershell
./scripts/test.ps1
./scripts/dev.ps1
```

开发服务器默认监听 `http://127.0.0.1:8080`。运行时状态默认存于 `data/relaypulse.db`，已被 Git 排除。

目前支持的环境变量：

| 变量 | 默认值 | 含义 |
| --- | --- | --- |
| `RELAYPULSE_LISTEN_ADDR` | `127.0.0.1:8080` | HTTP 监听地址 |
| `RELAYPULSE_DATA_DIR` | `data` | 运行时数据目录 |
| `RELAYPULSE_LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error` |
| `RELAYPULSE_SHUTDOWN_TIMEOUT` | `10s` | 优雅停机超时 |
| `RELAYPULSE_FLARESOLVERR_ENDPOINT` | 空 | 可选的环回 FlareSolverr 端点，例如 `http://127.0.0.1:8191`；只有在收到非 2xx 响应且响应体明显像 Cloudflare 验证页时才会联系它 |
| `RELAYPULSE_SESSION_ENCRYPTION_KEY` | 空 | 可选的 base64url 密钥（编码前至少 32 字节），用于加密站点会话包 |
| `RELAYPULSE_PUBLIC_URL` | 空 | 可选的公网 HTTP/HTTPS 源（origin），在生产域名就绪后配置 |
| `RELAYPULSE_OAUTH_CLIENT_ID` | 空 | OAuth 提供方客户端 ID；与客户端密钥一起配置后启用公开登录（当前为 LinuxDo） |
| `RELAYPULSE_OAUTH_CLIENT_SECRET` | 空 | OAuth 提供方客户端密钥；只保存在受保护的部署配置中 |
| `RELAYPULSE_HTTP_CONCURRENCY` | `3` | 站点 HTTP 操作的最大并发数（1-32） |
| `RELAYPULSE_COLLECTION_TIMEOUT` | `3m` | 单站点计划采集的超时时间 |
| `RELAYPULSE_HTTP_TIMEOUT` | `20s` | 出站 HTTP 客户端超时 |
| `RELAYPULSE_MAINTENANCE_INTERVAL` | `30m` | 历史清理间隔（最小 1m） |

## 会话导入

管理后台接受已登记站点的最小 JSON 会话包：

```json
{"userAgent":"Mozilla/5.0 ...","cookies":[{"name":"session","value":"..."}]}
```

该包先用 AES-GCM 加密，然后才写入 SQLite。不要把它粘贴到源码管理、issue 跟踪或日志里。浏览器配置文件永远不会被复制到服务器。

## 在 Chrome 中确认的适配器契约

NewAPI 定价家族目前暴露 `/api/pricing`，包含 `model_name` 与 `enable_groups[]`。其性能页面暴露 `/api/perf-metrics/summary?hours=24` 与 `/api/perf-metrics?model=...&hours=24`；后者返回逐分组的聚合指标以及按小时的 `series[]`。适配器同时保留精确的来源模型名与每一个来源分组名。大型目录只抓取一次，之后仅对匹配已配置热门模型规则的名称发起详情请求。

## 价格功能开发

当前价格由 `internal/pricing` 规范化，并存入现有的模型/分组来源扩展 JSON，因此新增解码器不需要数据库迁移。公开面板随每个当前分组行收到规范化报价，并选择报价可解析且当前处于 `healthy` 或 `degraded` 的最低价分组。处于 `failed`、`no_samples` 或 `unknown` 的分组不参与卡片价格选择；若没有可用的计价分组，卡片显示价格不可用。固定计费按每次请求展示；用量计费按每百万 token 展示，包含输入、输出、缓存读、缓存写价格。缓存字段缺失时保持明显不可用，而不是用其他价格推断。

开发规则时可运行聚焦的解码器测试：

```powershell
$env:GOCACHE = Join-Path $PWD '.cache\go-build'
$env:GOMODCACHE = Join-Path $PWD '.cache\go-mod'
& '.\.tools\go\bin\go.exe' test ./internal/pricing ./internal/adapter
```

对兼容 NewAPI 的探针，把 `pricingAdapter` 设为 `newapi` 并配置 `pricingPath` 与可选的 `pricingStatusPath`。对任何其他载荷，应实现并注册一个解码器，而不是把解析逻辑嵌进探针适配器或面板。

## 本地验收证据

首次本地运行登记了全部 13 个初始站点。需要认证会话的站点报告为 `login_expired`；检出 Cloudflare 页面的站点报告为 `challenge_pending`；失败的请求保留最近一次成功快照。一次代表性运行在 6 个拥有可用快照的站点上产生了 162 行公开的模型/分组数据。

在开发机上实测：SQLite 数据库约 1.1 MB（含 WAL 约 1.3 MB），公开行查询平均约 4 ms，Go 进程私有内存约 64 MB。这些数字不是生产容量保证，但证实所选的单进程设计在当前数据集下完全满足既定的低资源目标。

## Debian 13 服务概要

以专用的非特权用户运行二进制，并把数据目录放在发布目录之外。最小 systemd 单元如下：

```ini
[Unit]
Description=RelayPulse uptime monitor
After=network-online.target
Wants=network-online.target

[Service]
User=relaypulse
Group=relaypulse
WorkingDirectory=/opt/relaypulse
ExecStart=/opt/relaypulse/relaypulse
Environment=RELAYPULSE_LISTEN_ADDR=127.0.0.1:8080
Environment=RELAYPULSE_DATA_DIR=/var/lib/relaypulse
EnvironmentFile=-/etc/relaypulse/relaypulse.env
Restart=on-failure
RestartSec=10
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/relaypulse

[Install]
WantedBy=multi-user.target
```

FlareSolverr 是可选组件，必须单独管理，只绑定 `127.0.0.1`。保持单并发、短超时与失败冷却；它并不保证每个 Cloudflare 验证都能通过。不要在没有 swap 和内存监控的小内存主机上同时运行 Chromium 与 FlareSolverr。

OAuth 登录的人机验证不由 RelayPulse 自动处理。管理员在本地 Chrome 完成授权后，将最小化的 User-Agent（浏览器标识）与 Cookie（会话 Cookie）包导入后台；主服务不会读取 Chrome 配置目录，也不会把浏览器凭据写入 Git，导入内容通过上述密钥加密后保存。

## Chrome 会话同步

将 `extension/session-sync` 作为未打包的 Manifest V3 扩展加载。在管理后台选择 **浏览器同步**，复制十分钟有效的配对码，然后在扩展中连同 RelayPulse 源（origin）一并输入。它永远不会拿到管理员密码或管理员 Cookie。

同步令牌绑定到扩展源，三十分钟后过期，并在一次成功批量导入后作废。当操作者打开待接入站点的页面时，扩展只为列出的待接入站点源申请宿主权限。服务器通过现有的加密会话保险库保存导入的包。
