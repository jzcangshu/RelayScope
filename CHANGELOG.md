# Changelog

All notable changes to RelayScope are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- 管理台支持 All API Hub 批量导入登录态。站点页新增「批量导入」对话框，选择
  All API Hub 导出的账号备份文件（或粘贴其内容）后，后端按站点地址自动匹配
  已接入的站点，把 `account_info.access_token`/`account_info.id` 换算成
  RelayScope 会话格式批量加密写入；cookie 认证账号解析 `sessionCookie` 头。
  单站点的「导入登录态」对话框同样兼容：直接粘贴整份导出文件会自动挑出本站
  账号。导入结果逐项列出成功与跳过原因，未接入的站点不会被自动创建。
- 公开看板默认隐藏 24 小时内没有任何样本的模型入口，避免休眠站点淹没有效
  数据；在搜索框输入名称时这些入口仍会出现，全部被隐藏时的空态改用
  「暂无有效样本」文案并说明搜索可用，不再误报为「内容已被屏蔽」。
- Admin membership management by LinuxDO username. Operators can set exact
  expiry dates, clear membership validity, review registered and pre-registered
  members, and grant a membership before the user first signs in.
- Membership system backed by LinuxDO OAuth login. Users sign in through
  LINUX DO Connect; sessions persist in SQLite (SHA-256-hashed tokens) so
  logins survive restarts. Membership validity is extended either by
  redeeming admin-generated codes (`RS-XXXX-XXXX-XXXX`, Crockford base32,
  60-bit entropy) or by paying LDC through the LINUX DO Credit EasyPay
  compatible gateway. Extension semantics stack from the later of "now" and
  the current expiry. Admins batch-generate codes (1-500 per batch), review
  redemptions, revoke unused codes, and set the per-day LDC recharge price.
- Cloud-synced customization. The customize dialog (hidden sites/providers/
  models, default healthy toggle, tags) now syncs to a per-user
  `user_preferences` row with debounced writes; first login merges with
  cloud-first precedence and uploads local settings when the cloud row is
  empty. Editing requires an active membership; saved settings keep applying
  after expiry.
- Site wish pool. Logged-in users submit wishes (site name, URL, invite-code
  checkbox) that deduplicate by normalized domain; non-invite sites default
  to a 30 LDC target (admin-adjustable) while invite-required sites stay
  "target undecided" until the admin prices them. Visitors pledge LDC via
  payment orders; only verified payments count toward progress, targets
  auto-flip to "reached", refunds (full, via platform API) drop progress and
  can reopen a wish.
- Pluggable payment layer (`internal/payment`) with an EasyPay-compatible
  implementation of the LINUX DO Credit protocol (MD5 signing, GET notify
  callback verified by signature plus amount, order-query fallback, full
  refunds). Payment endpoints answer 501 until credentials are configured.

### Changed
- Membership recharge is monthly-only (admin-configured price, default 15
  LDC per 30-day month). Active members receive a monthly free wish credit
  (default 10 LDC, granted lazily once per calendar month via a unique
  (user, period) row — no cron) that is consumed first when pledging; the
  remainder, if any, goes through LDC payment. Credit-funded pledges count
  toward wish progress immediately.
- Public dashboard header restores the account entry (login button /
  user menu with redeem, recharge, feedback and logout) and adds a hash-routed
  wish pool page. The feedback dialog returns (its four regression tests pass
  again); the customize dialog is membership-gated with login/redeem guidance.
- Maintenance loop also prunes expired user sessions and pending LDC orders
  older than 24 hours.

### Added
- Per-site model keyword blocking. A site's adapter config may now carry
  `"blockedKeywords": ["…"]`; during collection the collector drops models
  whose raw name contains any keyword (case-insensitive) before observations
  are built, and the store removes previously collected matches immediately
  (`RemoveRawModels`) instead of waiting out the three-run absence buffer, so
  blocked models disappear from every dashboard view at the next collection
  regardless of availability. Snapshots and buckets are kept, so clearing the
  keyword restores a model with its history intact. Site write paths now
  reject adapter configs that are not valid JSON (they would have failed
  every collection run). The admin UI exposes the keywords as a dedicated
  field in the site dialog and shows a badge on blocked sites.

### Changed
- The admin console was redesigned on a dedicated stylesheet (`web/admin/
  admin.css`): sidebar navigation with hash deep links, action-oriented
  overview with attention shortcuts, site search and state filters, rule
  search with count badges, a site dropdown for run filtering (replacing the
  numeric site ID input), a session-import dialog (replacing `window.prompt`),
  toast feedback, inline SVG icons, light/dark themes that follow the public
  dashboard's `relayscope-theme` preference, and a responsive layout for
  narrow screens. Still dependency-free vanilla JS/CSS with no build step;
  the public dashboard is untouched.

### Fixed
- 修复 NewAPI v1.0 站点人民币符号显示成 `¤` 的问题。new-api 的
  `quota_display_type` 是货币预设（USD/CNY/CUSTOM），`custom_currency_symbol`
  只对 CUSTOM 类型有意义，但预设为 CNY 的站点该字段残留着未填写的占位符
  `¤`，价格解码器无条件采用了它，YiMingTalk、Ad公益站、南梁 API 等 5 个站
  的价格全部显示成「¤0.042」。现在非 USD/CUSTOM 货币遇到占位符、空值或
  解码默认的 `$` 时，按货币代码推导真实符号（CNY→¥），站点显式设置的自定义
  符号（含表情符号）原样保留。
- 修复计划采集默认超时回落到 3 分钟的问题：主程序此前把配置默认值覆盖掉
  调度器为挑战型 NewAPI 站点准备的 7 分钟上限，导致已完成的采集在落库前
  变成 `store_failed: context canceled`。同时让手动采集跟随服务生命周期，
  浏览器或反向代理断开不再取消落库，并同步延长管理台等待与服务器写超时。
- 推送标题带上站点名。`ApplyAnnouncements` 返回的新公告从未填充 `SiteName`，
  `renderMessage` 渲染出的标题是孤零零的「📢 」，用户无法分辨消息来自哪个站点。
  现在 store 层在返回前查一次站点名统一填入，标题形如
  「📢 咕咕嘎嘎」或「📢 HXI AI | 标题」。
- 站点首次采集公告按历史回填处理，不再轰炸订阅者。给 4 个适配器接上 NewAPI
  公告采集后，CoeeApi（48 条）、HXI AI（41 条）等站点的全部历史公告在首个采集
  周期被当成「新公告」逐条入队，一夜之间把订阅者的手机通知灌满。现在首个批次的
  公告照常入库但全部视为旧闻不推送，之后采集到的增量才触发通知；公告内容变更
  仍然推送（有测试固定该语义）。
- 通知订阅页的配置状态现在一目了然。之前推送目标（Bark key / Chat ID /
  Webhook）只是一个草稿输入框：没有保存按钮、没有已保存回填、刷新即丢，
  站点勾选时悄悄把「当时输入框里有什么」当作目标，用户无从确认配置到底存没存好。
  现在渠道是显式的单一事实来源：打开页面回填已保存的平台与目标，状态行直接写明
  「✓ 已保存 · Bark …5678 · 1 个站点订阅使用此渠道」，输入即显示
  「● 有未保存的修改」并点亮「保存渠道」按钮；换 key/换渠道一次应用到全部已订阅站点
  （待发送队列同步改道），历史不一致的目标在保存时统一。站点订阅改用已保存渠道而非
  输入框草稿，每行右侧显示 `🔔 …5678` 渠道徽标，未保存渠道时勾选站点会被拦下并提示
  先保存渠道。
- 修复 `notification_subscriptions` / `notification_outbox` 时间戳列的扫描错误：
  `created_at` / `updated_at` 是 INTEGER 毫秒列，`ListUserSubscriptions`、
  `ListActiveSubscriptionsForSite` 和 `ListPendingNotifications` 却直接 Scan 进
  `time.Time`。`ListPendingNotifications` 尤其致命——生产 outbox 之前恒为空，
  第一条真实待推送就会让 dispatcher 的扫描报错并静默跳过整批投递。现在统一先扫 int64
  再 `time.UnixMilli` 转换（由新测试覆盖）。
- NewAPI pricing sites now collect announcements. `NewAPIAdapter` never
  implemented the announcement provider interface, so sites using the
  `newapi-pricing` adapter (e.g. 小鸡毛的公益API站) never fed the notification
  outbox — subscribers' Bark/Feishu/Telegram channels only ever received manual
  test pushes, never automatic ones. The adapter now reads the same `/api/status`
  timeline (plus `/api/notice` diff and `disabled`) modes the probe adapter uses,
  through shared collection logic.
- Four adapters that sit on top of NewAPI now collect its announcements too:
  `model-pulse`, `model-probe`, `aiapi-probe` and `uptime-kuma` (e.g. 咕咕嘎嘎、
  简直了、HXI AI all publish real announcements nobody was receiving). Each gains
  an `announcementMode` config (`auto` default) that shares the same timeline /
  notice-diff collection as the built-in NewAPI adapters. `auto` silently skips
  sites whose `/api/status` is not a NewAPI payload (a genuine Uptime Kuma or
  AIAPI status page, or an HTML status page like CoeeApi), so nothing breaks when
  the endpoint belongs to the adapter's own format.
- Auto-mode announcement collection now falls back to the source URL's host when
  the base URL does not answer with a NewAPI payload. Sites like CoeeApi register
  a fronting status page as the base URL (`status.coee.ccwu.cc`, HTML) while the
  monitored NewAPI deployment lives at the source URL's host
  (`api.coee.ccwu.cc`, 48 published announcements) — those announcements were
  unreachable. Once the primary host answers with a valid NewAPI payload it is
  treated as authoritative, so a disabled timeline never leaks into probing an
  unrelated host.
- NewAPI sites that publish an empty pricing catalog (2xx body with a
  genuinely empty model list, e.g. Ad公益站, which returns vendors but zero
  price rows) no longer fail the collection run. The adapter now reports a
  complete empty catalog, so the run is recorded as success and previously
  seen models are marked unavailable via the missing-catalog path instead of
  surfacing a misleading "returned no models" error. Catalog entries that
  exist but carry no usable model name still fail, and an explicit API-level
  failure on a 2xx body (`{"success": false, "message": …}`) still surfaces
  as a collection error rather than being mistaken for an empty site.
- Collection-time HTTP 403 now classifies the site as login expired, matching
  how the session-refresh path already treated it. Sites that newly require
  login for their pricing endpoint and reject a stale stored credential with
  403 (instead of 401) previously showed an opaque "collection failed" with no
  path to recovery; they now show "登录已失效，请重新同步浏览器登录态" with the
  import-login-state action, same as a 401.
- NewAPI probe sites now report every key group and honest 24h timelines.
  The collector ignored the plugin's `token-groups` endpoint, so groups came
  only from pricing `enable_groups` (3 of 8 groups on one site) and models
  without pricing landed under a fabricated `default` group; the probe's
  key-group list is now authoritative and its models extend the catalog.
  Zero-traffic probe slots were stored as a fabricated 100% success ratio
  (the plugin stamps them `success_rate=100` while the source UI renders them
  grey "no request"), and the plugin's fetch-anchored rolling slot windows
  shifted every collection, so hourly buckets never hit the store's upsert
  key and accumulated ~200 shifted copies per dashboard slot — the public
  timeline's best-state-per-slot fold then let adjacent healthy hours
  outvote real failures and rendered every bar green. Zero-traffic slots now
  map to no-samples, hourly slots are aligned to clock-hour boundaries so
  successive runs upsert in place, and the affected drifted buckets are
  removed by the same predicate (`resolution_seconds=3600 AND
  bucket_start % 3600000 <> 0`).
- NewAPI probe detail collection no longer fails with HTTP 404 for
  slash-containing model names (e.g. `openai/gpt-oss-120b`,
  `moonshotai/kimi-k3`). The model-status plugin resolves
  `/api/model-status/embed/status/{model}` behind a router that decodes `%2F`
  back to `/` before path matching, so a single-escaped segment could never
  match and poisoned every run as `details_partial`. The primary URL now
  double-escapes the `{model}` segment, and a 404 on the primary form retries
  once with the alternate encoding before the model is recorded as a
  partial-detail issue.

### Added
- Fetchers may implement the optional `JSONPoster` interface. The probe
  adapter uses it to fetch all model timelines in one `POST
  /status/batch?window=24h` call per collection (falling back to per-model
  GETs when the fetcher or plugin does not support it), and reads
  `token-groups` for key-group membership.
- Versioned deployment catalogs and admin-API import tooling for server
  migration: `sites.production.json` (40 monitored sites) and
  `rules.production.json` (57 model-matching rules, including gpt-oss-120b/20b,
  gpt-6-astra, claude-fable-5.1/5.2, glm-5.3-flash, and
  deepseek-v4-pro-0813), replayable with `scripts/import-sites.sh` and
  `scripts/import-rules.sh`. Catalogs hold public site URLs, model names, and
  matching patterns only — credentials never enter the repository.

## [v0.1.2] - 2026-09-05

### Fixed
- A transient session-refresh failure no longer permanently locks an
  authenticated site as `login_expired`. Session refreshes now classify their
  HTTP status the same way collection-time fetches do: a 401/403 rejection
  marks the site `login_expired`, while a 5xx or network error reports
  `adapter_collect_failed` so the next scheduled run can retry with the stored
  refresh cookie. Previously any refresh error was hardcoded to
  `login_expired`, which combined with the scheduler's 30-minute backoff could
  strand a site indefinitely after a single failed refresh.
- Raised the FlareSolverr challenge timeout to 180s and the scheduled and
  manual collection ceilings to 7 minutes, because heavier Turnstile
  challenges and the two-solve-per-collection pattern on NewAPI pricing
  sites were clipping healthy runs with `challenge_failed` or
  `context canceled`.
- Browser session sync no longer fails with 401 right after pairing: Chromium
  omits the `Origin` header on cross-origin GET fetches made by extensions, so
  the extension now reads pending sites via POST (which always carries Origin)
  and the server accepts both GET and POST on `/api/v1/session-sync/pending`.
- Extension error messages now surface the server's `error` field instead of a
  generic status code.
- A successful model-probe report that contains no models is now treated as an
  empty catalog: every previously known model of the site is marked
  unavailable instead of the collection failing with `adapter_collect_failed`.
  The collector accepts an empty catalog when the adapter explicitly declares
  how absent models should be marked; silent emptiness still fails the run.
- A missing-catalog pass re-admits soft-removed models before selecting
  groups, so the first empty catalog marks the full model list unavailable
  instead of resurrecting removed models with their pre-removal snapshots.

### Changed
- The project is renamed to `RelayScope` across the module path, docs, build
  scripts, and container image references.
- The administrator session-import prompt and the operations docs describe the
  session JSON contract generically (auth types, cookies, refresh rotation)
  instead of calling out one specific site.

## [v0.1.1] - 2026-08-24

### Added
- Bilingual documentation: Chinese translations of every guide with per-document
  language switching; the Chinese README is the default entry point and the
  English mirror lives in `README_EN.md`.
- Measured deployment sizing guide with the production baseline, recommended
  host profiles, a capacity model, and scaling signals.
- Dashboard screenshot in both READMEs.

### Changed
- README restructured: hero image, revised introduction, and the recommended
  server section placed before project status.
- CI badge links and the quick-start image path updated for the repository
  rename to `RelayScope` (image now published as `ghcr.io/jzcangshu/relayscope`).

### Removed
- Internal restructuring records (`docs/development-plan.md`, `docs/plans/`)
  and the personal agent working agreement (`AGENTS.md`) from the public
  repository.

## [v0.1.0] - 2026-08-24

### Added
- Management API lifecycle controls, filtered runs, unmatched-model inspection, redacted session metadata, and schema-driven adapter fields.
- Configurable HTTP concurrency, collection/HTTP timeouts, maintenance interval, and build metadata.
- Public open-source repository scaffold: CI workflow, Dockerfile, Makefile, CONTRIBUTING guide.
- Cross-platform build entry point (`make build/test/vet`) replacing PowerShell-only scripts.

### Changed
- Repository repositioned as a generic relay health aggregator (decoupled from LinuxDo community specifics).

### Removed
- Hardcoded community site seeds and per-site operational migrations.

[v0.1.2]: https://github.com/jzcangshu/RelayScope/compare/v0.1.1...v0.1.2
[v0.1.1]: https://github.com/jzcangshu/RelayScope/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/jzcangshu/RelayScope/releases/tag/v0.1.0
