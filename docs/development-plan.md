# RelayPulse 开发落地计划（开源化改造）

> 本文件是改造期间的执行契约：每个阶段独立提交、测试全绿后才进入下一步；
> 只做该阶段定义的事，旁支问题记录不顺手改；保持轻量化，删除代码优于新增代码。

## 总原则与执行纪律

1. **每个阶段独立提交、测试全绿后才进下一步。** 每步必须通过 `go vet ./...`、`go test ./...`、`gofmt -l .`、node 测试。
2. **只做该阶段定义的事。** 发现的旁支问题记录到本文件末尾的「待办备忘」，留到对应阶段处理。
3. **保持轻量化。** 不引入依赖、不增加抽象层、不加兜底分支，除非有明确收益且写明理由。

**硬约束**（详见 architecture.md）：
- 一个 Go 进程 + 一个 SQLite 数据库；无 Redis/MQ/独立 TSDB。
- 无 CGO；唯一直接依赖 `modernc.org/sqlite`。
- 无运行时 Node.js；前端为 embed 静态资源。
- 目标部署：1 核 768MB 服务器。
- 网络操作永不持有 SQLite 事务。

---

## Phase 0 — 去特定化与安全卫生 ✅（已完成）

删除个人横幅/链接；12 个迁移合并为干净基线；移除 36 站点种子与 50 条社区模型规则
（改为示例种子）；生产域名占位符化；前端文案中性化；OAuth 提供者解耦
（users.provider+external_id、RELAYPULSE_OAUTH_*）；删除死代码。

## Phase 1 — 架构偿债

### 1.1 RefreshMatches 增量化（最高优先级）

现状：每次站点采集成功后全量重算所有站点所有 raw_models 的匹配（单事务逐行
DELETE+INSERT），N 站点并发时产生 N 次全表重写，SQLite 单写者排队。

改动：
- `ApplyCollection` 返回本次采集涉及的 raw_model ID 列表。
- 新增 `RefreshMatchesForRawModels(ctx, engine, rawModelIDs, now)`：只为给定模型重算匹配。
- `RefreshAllMatches`（原全量路径）保留给规则变更时的 `ReloadMatcher` 使用。
- matcher 的词表归一化移到 `New()` 构建期预计算（compiledRule 缓存 normalized 词表）。

边界：不改匹配语义；不改 model_matches 表结构。

### 1.2 调度持久化与启动风暴消除

现状：调度状态在内存 map 中，重启后所有站点立即 due 形成风暴；`GetSite(id)` 缺失导致
每 worker 全表 ListAllSites 找一行。

改动：
- 迁移 002：`sites.next_run_at INTEGER` + `(enabled, next_run_at)` 索引（这次真正接线）。
- store 新增 `ListDueSites(ctx, now, limit)` / `GetSite(ctx, id)` / `SetSiteNextRun(ctx, id, at)`。
- scheduler 改为从 DB 读 due 站点、完成后把下次时间写入 DB；内存仅保留 in-flight 去重集合。
- NULL next_run_at 视为立即可采集（新站点首采）。

边界：不改间隔默认值（15min 正常 / 30min 失败）、jitter 语义、3 分钟采集超时。

### 1.3 适配器共享工具包收敛

现状：阈值 0.85/0.50 硬编码三处（newapi.go 与 query.go SQL×2）；六份适配器各自的状态
映射函数；四份"大于 1e11 当毫秒"时间戳猜测；findArray/firstString/stringSlice/
numberPointer 在 adapter/newapi.go 与 pricing/newapi.go 逐字重复。

改动：新建 `internal/adapter/adapterutil` 包：
- `health.go`：`HealthyRatio/DegradedRatio` 常量 + `RatioToServiceState(ratio)` +
  `NormalizeRatio(value)`；query.go 的 SQL CASE 表达式由同一常量构建。
- `timeparse.go`：`ParseFlexibleTime(value int64) time.Time` 统一秒/毫秒猜测。
- ~~`jsonwalk.go`：`FindArray/FirstString/StringSlice/NumberPointer` 通用 walker。~~

边界：不改任何适配器的采集逻辑和解码行为，只做提取+替换；每替换一批跑一次测试。

> **JSON walker 合并决策**：审计发现 findArray（depth 上限 4 vs 5、adapter 有
> 内层 key 探测）、firstString（adapter 有 model/model_info 嵌套回退）、
> stringSlice（adapter 首个数组即返回含空切片 vs pricing 继续尝试并返回 nil）、
> numberPointer（adapter 用 strconv 且支持 `%` 除 100 vs pricing 用 fmt.Sscanf
> 无 `%` 处理）四函数均有语义差异。合并会改变解码行为，违反本阶段边界，
> 故保留各自实现。如需统一须另立阶段，逐函数验证所有调用点行为。

### 1.4 配置双真相统一

现状：`ConfigSchema()` JSON Schema 字面量与运行时手写 struct 各自维护默认值，无校验。

改动：
- `internal/adapter` 新增 `ApplyConfigDefaults(schema, raw)`：按 schema 为缺失字段注入
  default，并对已知属性做 type/enum/minimum 校验；保留未知键。轻量实现，不引入
  jsonschema 依赖。
- 各适配器 Collect 开头的 config 解码改用此函数，消除手写默认值填充。

边界：不做 schema 由 struct 反射生成（侵入大收益不明，留待需求驱动）。

### Phase 1 验收

全测试绿；新增增量刷新行为测试；调度重启不风暴测试；adapterutil 单元测试；
各适配器测试确认替换后行为不变。

## Phase 2 — 代码重构

store.go（1275 行）拆为 migrate/sites/runs/sessions/rules/matches/collection_apply/meta；
server.go 拆为 respond/middleware/public/auth/sessionsync/admin handlers（闭包改方法，
新增 writeError 统一错误 envelope，CSRF token 独立随机值）；newapi.go 按
Collect/decode/merge 三段拆分（mergeDetailBuckets 162 行拆子函数）。
边界：不改任何外部行为；最大单文件 <400 行。

## Phase 3 — 管理后台重做

后端先行：DELETE site（软删）、POST admin/logout、URL 可编辑、runs 过滤参数、
GET admin/unmatched（spec 承诺未实现）、session 元信息端点、错误 envelope。
前端：vendored Preact+htm 零构建 ESM（满足 CSP 'self' 无 Node），五视图
（概览/站点/规则/运行/系统），schema 驱动适配器配置表单（~100 行渲染器 +
JSON 高级回退）。前端测试升级为行为测试。

## Phase 4 — 发布基础设施

运维参数进 config（HTTP 并发/采集超时/HTTP 超时/维护周期/健康阈值）；信号量感知 ctx；
版本 ldflags 注入 + 首 tag v0.1.0；GHCR 镜像 workflow；文档定稿（development.md 去
个人化、adapter-authoring 更新、operations 补 Docker）。

---

## 明确排除（防目标偏移）

i18n 国际化；合成探测（打真实模型请求）；跨站综合评分；长期时序分析；WebSocket/SSE；
第二个 OAuth 提供者实现；schema 反射生成；通知渠道（飞书/邮件/Webhook，独立功能模块）。

## 待办备忘（执行中发现的旁支问题）

- ReloadMatcher 在释放写锁后才调 RefreshAllMatches，与并发采集的增量刷新可能重复工作
  （自愈性质，暂不改；若 Phase 2 拆分时顺路可加 in-flight 合并）。
- collector worker goroutine 无 panic recovery（未列入任何阶段，保持现状）。
- 登录限流 maxFailures map 无过期清理（轻微内存泄漏；Phase 2 server 拆分时处理）。
