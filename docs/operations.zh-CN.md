# 运维清单

[English](operations.md) | **中文**

## 首次部署前

- 创建 `/var/lib/relaypulse`，权限 `0700`，属主 `relaypulse`。
- 在 Git 之外生成强随机的 `RELAYPULSE_SESSION_ENCRYPTION_KEY`。
- 只复制发布二进制与内嵌资源。
- 把生成的管理员密码文件放进受保护的数据目录；不要留在 shell 历史或代码仓库中。
- 配置反向代理转发到 `127.0.0.1:8080` 并终结 HTTPS。
- 待域名解析到服务器且反向代理证书生效后，再配置 `RELAYPULSE_PUBLIC_URL=https://relaypulse.example.com`。
- 按[部署容量指南](deployment-sizing.zh-CN.md)给主机选型。768 MB 档位只适用于不带 FlareSolverr 的 RelayPulse；浏览器人机验证恢复需要更大的主机和 swap。

## 发布文件

- 用 `scripts/build-linux.ps1` 构建 Linux amd64 版本。
- 把二进制安装为 `/opt/relaypulse/relaypulse`，并安装 `deploy/relaypulse.service` 中的单元文件。
- 将 `deploy/relaypulse.env.example` 复制为 `/etc/relaypulse/relaypulse.env`，随后只在服务器上填写密钥，文件权限设为 `0600`。
- 替换占位域名后再使用 `deploy/Caddyfile.example`。

带版本号的发布（`vMAJOR.MINOR.PATCH`）会通过 `.github/workflows/release.yml` 把无 CGO 的镜像发布到 GHCR。部署前核对 tag 与镜像摘要，保留上一个二进制和数据库备份；重启后检查两个健康端点和管理后台的运行视图。

## FlareSolverr

- 固定使用独立的 Linux x64 发行包，而不是引入容器运行时。
- 安装在 `/opt/flaresolverr` 下，以非特权用户 `flaresolverr` 运行，并使用 `deploy/flaresolverr.service`。
- 保持 `HOST=127.0.0.1`；8191 端口绝不能对外暴露。
- 在该辅助程序通过启动与请求检查之前，把 RelayPulse 的端点留空；之后再设置为 `http://127.0.0.1:8191`。
- 浏览器操作由 RelayPulse 串行化。systemd 单元还施加了 1 GB 软内存限制和 1.5 GB 硬限制。
- FlareSolverr 能通过一些浏览器检查与非交互式验证，但其上游验证码求解服务目前的文档标注为不可用。应把 `challenge_failed` 视为预期的外部限制，并保留最近一次成功快照。

## 例行检查

- 升级后访问 `/health/live` 与 `/health/ready`。`live` 是进程探针；`ready` 还会做一次轻量的 SQLite 版本号检查，存储不可用时应返回 `503`。
- 在管理后台的采集运行视图中检查 `login_expired`、`challenge_pending`、`challenge_failed` 与过期数据。
- 通过 Chrome 访问令牌侧边栏刷新私密站点的凭据。
- 至少保留一份较新的 SQLite 备份；在服务停止时复制数据库，或使用 SQLite 的在线备份工具。
- 关注磁盘占用。服务以小批量删除超过 72 小时的历史数据，不会每个周期都执行全量 vacuum。
- 访问令牌扩展只能指向配置好的 RelayPulse 源。它上传待接入列表中来源完全匹配的条目，绝不上传账号备份的其余内容。
- `deploy/nginx-monitor.conf` 中 80 端口的虚拟主机会转发整个应用。在不可信网络上使用管理后台或会话同步路径之前，应优先使用 HTTPS。

## 恢复语义

采集失败绝不会覆盖最近一次成功的服务快照。公开页面单独标注采集新鲜度，因此用户能分辨是模型本身不健康，还是监测程序暂时读不到来源站点。
