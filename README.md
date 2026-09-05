# RelayScope

**[English](README_EN.md)** | 中文

![RelayScope](docs/assets/relayscope-hero-rounded.jpg)

RelayScope 是一个面向 AI API 中转站的，轻量化模型健康度聚合监测系统。

支持采集：状态页、模型市场、各分组可用性、模型目录与价格信息。它**不会**发起付费的模型调用来做探测；只保留短期滚动历史，完整保留每个站点的原始模型名与分组名，并呈现统一的跨站点视图。

[![CI](https://github.com/jzcangshu/RelayScope/actions/workflows/ci.yml/badge.svg)](https://github.com/jzcangshu/RelayScope/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

## 为什么选择 RelayScope

现有的拨测工具（Uptime Kuma、Gatus）只能监控 HTTP 端点，不理解**模型**；LLM 网关（LiteLLM、Helicone）只能观察经过自己转发的流量，无法比较你没有接入的站点。RelayScope 填补了两者之间的空白：

| 问题 | RelayScope 的回答方式 |
|---|---|
| 「哪个站点现在能用 X 模型？」 | 读取各站点自己的状态/市场数据，按模型、按分组呈现 |
| 「站点 Y 现在能用吗？」 | 跨模型视图，服务状态与采集状态相互独立 |
| 「代价是多少？」 | 在采集健康数据的同时捕获价格，且不发起任何付费 API 调用 |

它把**服务健康**（healthy / degraded / failed / no_samples）与**采集健康**（fresh / stale / collecting / login_expired / challenge_pending）区分开，采集失败永远不会被伪装成服务故障。

![站点页面截图](docs/assets/dashboard-light.png)

## 功能特性

- **被动采集** —— 只读站点已公开的数据；没有合成探测，没有模型调用开销。
- **模型级粒度** —— 按规范模型家族、站点、原始模型名和站点分组监控。
- **适配器体系** —— NewAPI、Sub2API、Uptime Kuma、model-market 以及自定义探针协议。新增一种探针变体只需一次构造器调用。
- **加密凭据保险库** —— 已认证站点的令牌与 Cookie 以认证加密存储；Chrome 扩展可从已登录浏览器导入会话。
- **Cloudflare 人机验证恢复** —— 可选集成 FlareSolverr，应对托管式人机验证背后的站点。
- **极度轻量** —— 一个 Go 二进制，一个 SQLite 文件（纯 Go 驱动，无 CGO）。默认站点数下 1 核 / 768 MB VPS 即可运行；启用 FlareSolverr 或扩大站点规模前请先阅读[部署容量指南](docs/deployment-sizing.zh-CN.md)。
- **诚实的数据** —— 空采样窗口就是 `no_samples`，绝不冒充健康或故障。不合成槽位，不做综合评分。

## 快速开始

```bash
# 从源码构建（Go 1.26+）
make build
./bin/relayscope
# → 监听 http://127.0.0.1:8080

# 或者使用 Docker
docker run -d -p 8080:8080 -v relayscope-data:/app/data ghcr.io/jzcangshu/relayscope:latest
```

首次运行时，RelayScope 会生成一个强管理员密码并写入 `<data-dir>/admin-password.txt`（权限 0600）。公开面板无需登录；管理后台位于 `/admin/`。

## 配置

所有配置都通过环境变量完成。常用的有：

| 变量 | 默认值 | 用途 |
|---|---|---|
| `RELAYSCOPE_LISTEN_ADDR` | `127.0.0.1:8080` | 监听地址 |
| `RELAYSCOPE_DATA_DIR` | `data` | SQLite 数据库与管理员密码所在目录 |
| `RELAYSCOPE_LOG_LEVEL` | `info` | 日志级别（debug/info/warn/error） |
| `RELAYSCOPE_SHUTDOWN_TIMEOUT` | `10s` | 优雅停机时限 |
| `RELAYSCOPE_PUBLIC_URL` | _空_ | 规范公网地址（OAuth 回调用） |
| `RELAYSCOPE_OAUTH_CLIENT_ID` | _空_ | OAuth 登录 Client ID（与 SECRET 成对配置） |
| `RELAYSCOPE_OAUTH_CLIENT_SECRET` | _空_ | OAuth 登录 Client Secret（只保存在服务器上） |
| `RELAYSCOPE_SESSION_ENCRYPTION_KEY` | _空_ | 加密导入站点会话的密钥 |
| `RELAYSCOPE_FLARESOLVERR_ENDPOINT` | _空_ | 可选的 FlareSolverr 端点（限环回） |
| `RELAYSCOPE_HTTP_CONCURRENCY` | `3` | 站点 HTTP 操作最大并发数 |
| `RELAYSCOPE_COLLECTION_TIMEOUT` | `3m` | 单站点计划采集超时 |
| `RELAYSCOPE_HTTP_TIMEOUT` | `20s` | 出站 HTTP 客户端超时 |
| `RELAYSCOPE_MAINTENANCE_INTERVAL` | `30m` | 历史清理间隔 |

完整列表见 `docs/development.md`（[中文版](docs/development.zh-CN.md)），模板见 `deploy/relayscope.env.example`。

## 文档

| 中文 | English |
| --- | --- |
| [产品规格](docs/product-spec.zh-CN.md) | [Product Spec](docs/product-spec.md) |
| [架构](docs/architecture.zh-CN.md) | [Architecture](docs/architecture.md) |
| [数据契约](docs/data-contract.zh-CN.md) | [Data Contract](docs/data-contract.md) |
| [适配器编写](docs/adapter-authoring.zh-CN.md) | [Adapter Authoring](docs/adapter-authoring.md) |
| [运维清单](docs/operations.zh-CN.md) | [Operations](docs/operations.md) |
| [本地开发](docs/development.zh-CN.md) | [Development](docs/development.md) |
| [部署容量指南](docs/deployment-sizing.zh-CN.md) | [Deployment Sizing](docs/deployment-sizing.md) |

## 添加监控站点

RelayScope 以空数据库启动。通过管理后台（`/admin/`）添加站点：

1. 选择适配器并填写其配置（后台会根据适配器的 JSON Schema 渲染表单）。
2. 如站点需要登录，可选导入登录会话。
3. 调度器随即按配置的间隔开始采集。

模型匹配规则同样在管理后台维护（必需词/任选词/排除词/正则，带优先级）；后台提供规则预览和未匹配模型列表，方便逐步收敛。

仓库根目录提供了 `sites.example.json` 作为配置格式参考。需要批量建站或迁移服务器时，可用导入脚本回放站点与规则目录：

```bash
RELAYSCOPE_ADMIN_PASSWORD=... scripts/import-sites.sh sites.production.json http://127.0.0.1:8080
RELAYSCOPE_ADMIN_PASSWORD=... scripts/import-rules.sh rules.production.json http://127.0.0.1:8080
```

`sites.production.json` 与 `rules.production.json` 是本仓库实际生产部署的站点与规则目录，作为灾备与迁移的单一事实来源（只含公开 URL、模型名和匹配模式，凭据永不入库）。两个脚本的幂等语义不同：站点脚本不去重，只对全新数据库执行一次；规则脚本按 canonicalName 跳过已存在项，可重复执行。详见[运维清单](docs/operations.zh-CN.md)。

## 推荐服务器配置

| 部署形态 | CPU | 内存 | 磁盘 | Swap |
| --- | ---: | ---: | ---: | ---: |
| 仅 RelayScope | 1-2 vCPU | 1 GB | 10 GB 可用 | 1-2 GB |
| 含 FlareSolverr | 2-4 vCPU | 4 GB | 20 GB 可用 | 4 GB |

在默认站点数量下，768 MB 档位适合只跑 RelayScope 的场景，不适合 Chromium/FlareSolverr。容量假设与扩容信号见[部署容量实测指南](docs/deployment-sizing.zh-CN.md)。

## 许可证

[MIT](LICENSE) — RelayScope contributors。

## LinuxDo 社区

<div align="center">
  <a href="https://linux.do" target="_blank">
    <img src="https://cdn3.ldstatic.com/original/4X/c/c/d/ccd8c210609d498cbeb3d5201d4c259348447562.png" alt="LinuxDo" height="60">
  </a>
  <p>
    <a href="https://linux.do" target="_blank"><strong>LinuxDo 社区</strong></a><br>
  </p>
    <p>@蕉灼の仓鼠</p>
    <p>本人长期活跃于L站;</p>
    <p>这里的人很好说话又好听;</p>
    <p>欢迎都来加入L站大家庭。 </p>

</div>
