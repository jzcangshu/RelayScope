const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const test = require('node:test');

function loadPriceHelpers() {
  const source = readFileSync(join(__dirname, 'dashboard.js'), 'utf8');
  const start = source.indexOf('function priceValue(price)');
  const end = source.indexOf('function cardMatchesFilters');
  assert.notEqual(start, -1, 'priceValue helper is missing');
  assert.notEqual(end, -1, 'cardMatchesFilters marker is missing');
  return Function(`${source.slice(start, end)}; return { lowestPrice };`)();
}

const { lowestPrice } = loadPriceHelpers();

test('public dashboard uses a bell control that toggles the notification sidebar', () => {
  const html = readFileSync(join(__dirname, 'index.html'), 'utf8');
  const css = readFileSync(join(__dirname, 'dashboard.css'), 'utf8');
  const source = readFileSync(join(__dirname, 'dashboard.js'), 'utf8');
  assert.match(html, /id="announcement-action" class="theme-toggle announcement-action"/);
  assert.match(html, /id="nc-sidebar"/);
  assert.match(html, /id="announcement-content"/);
  assert.match(html, /id="notice-content"/);
  // The bell drives the sidebar, not a dialog, so it must not advertise a
  // popup it cannot open.
  assert.match(html, /aria-controls="nc-sidebar"/);
  assert.match(html, /aria-expanded="false"/);
  assert.doesNotMatch(html, /announcement-dialog|aria-haspopup="dialog"/);
  assert.doesNotMatch(html, /service-status|announcement-count|运行公告|运行状态通知/);
  assert.equal((html.match(/class="icon-button"/g) || []).length, 6);
  assert.equal((html.match(/class="icon-button"[^>]*>[\s\S]*?<svg/g) || []).length, 6);
  assert.match(css, /\.nc-sidebar-title \{ margin: 0; font-size: 17px/);
  assert.match(css, /\.nc-range-btn\.active \{ background: var\(--surface\); color: var\(--accent\)/);
  assert.match(css, /\.icon-button[\s\S]*?width: 34px[\s\S]*?height: 34px/);
  assert.match(css, /\.icon-button svg[\s\S]*?width: 19px[\s\S]*?height: 19px/);
  assert.match(source, /announcementAction\?\.setAttribute\('aria-expanded'/);
});

test('dashboard script binds the public login control', () => {
  const source = readFileSync(join(__dirname, 'dashboard.js'), 'utf8');
  assert.match(source, /user-action/);
  assert.match(source, /userAction\.addEventListener\('click'/);
});

test('public theme initialization is CSP-compatible', () => {
  const page = readFileSync(join(__dirname, 'index.html'), 'utf8');
  const theme = readFileSync(join(__dirname, 'theme.js'), 'utf8');
  assert.match(page, /<script src="\/assets\/theme\.js"><\/script>/);
  assert.doesNotMatch(page, /<script>\s*try\s*\{/);
  assert.match(theme, /localStorage\.getItem\('relayscope-theme'\)/);
});

test('dashboard script renders and polls failure announcements', () => {
  const source = readFileSync(join(__dirname, 'dashboard.js'), 'utf8');
  assert.match(source, /\/api\/v1\/public\/announcements/);
  assert.match(source, /toggleNCSidebar\(/);
  assert.match(source, /failureCode/);
  assert.match(source, /setInterval\(loadRows, 60000\)/);
  assert.match(source, /ncSiteAnnouncements = payload\.siteAnnouncements \|\| \[\]/);
  assert.doesNotMatch(source, /loadSiteAnnouncementsBatch/);
  assert.doesNotMatch(source, /数据未变化/);
  assert.doesNotMatch(source, /announcementCount/);
});

function loadNcRangeHelpers() {
  const source = readFileSync(join(__dirname, 'dashboard.js'), 'utf8');
  const start = source.indexOf('const NC_RANGES = [');
  const end = source.indexOf('function renderAnnouncements()');
  assert.notEqual(start, -1, 'NC_RANGES is missing');
  assert.notEqual(end, -1, 'renderAnnouncements marker is missing');
  const body = source.slice(start, end);
  return Function(`${body}\nreturn { NC_RANGES, ncRangeMs, resolveNcRange, setRange: (value) => { ncRange = value; } };`)();
}

const ncRangeHelpers = loadNcRangeHelpers();
const HOUR_MS = 3600000;
const DAY_MS = 86400000;
const postedAgo = (ms, now) => ({ time: new Date(now - ms) });

test('announcement range auto-fits the narrowest window holding the newest post', () => {
  const now = Date.now();
  ncRangeHelpers.setRange('auto');
  assert.equal(ncRangeHelpers.resolveNcRange([postedAgo(2 * HOUR_MS, now)], now), '24h');
  assert.equal(ncRangeHelpers.resolveNcRange([postedAgo(3 * DAY_MS, now)], now), '7d');
  assert.equal(ncRangeHelpers.resolveNcRange([postedAgo(10 * DAY_MS, now)], now), '30d');
  // 站点公告经常比 30 天还旧：回落到最宽范围，而不是把「站点公告」整节藏掉
  assert.equal(ncRangeHelpers.resolveNcRange([postedAgo(90 * DAY_MS, now)], now), '30d');
});

test('announcement range keeps an explicit user choice over auto-fit', () => {
  const now = Date.now();
  const items = [postedAgo(10 * DAY_MS, now)];
  ncRangeHelpers.setRange('24h');
  assert.equal(ncRangeHelpers.resolveNcRange(items, now), '24h');
  ncRangeHelpers.setRange('auto');
  assert.equal(ncRangeHelpers.resolveNcRange(items, now), '30d');
});

test('announcement range ignores undated entries when auto-fitting', () => {
  const now = Date.now();
  ncRangeHelpers.setRange('auto');
  assert.equal(ncRangeHelpers.resolveNcRange([], now), '30d');
  assert.equal(ncRangeHelpers.resolveNcRange([{ time: null }], now), '30d');
});

test('announcement range falls back to 24h for unknown keys', () => {
  assert.equal(ncRangeHelpers.ncRangeMs('24h'), DAY_MS);
  assert.equal(ncRangeHelpers.ncRangeMs('nonsense'), DAY_MS);
});

test('notification center keeps the range switcher when the range holds nothing', () => {
  const source = readFileSync(join(__dirname, 'dashboard.js'), 'utf8');
  const css = readFileSync(join(__dirname, 'dashboard.css'), 'utf8');
  // 分节的存在性取决于"有没有公告数据"，不是"当前范围内有没有"——否则胶囊会被一起藏掉
  assert.match(source, /const hasSiteFeed = filtered\.length > 0;/);
  assert.match(source, /if \(hasSiteFeed\) \{/);
  assert.match(source, /nc-range-empty/);
  assert.match(css, /\.nc-range-empty \{/);
});

test('lowestPrice selects the cheapest currently usable group', () => {
  const groups = [
    { serviceState: 'failed', price: { available: true, inputPerMillion: 0, groupMultiplier: 0 } },
    { serviceState: 'healthy', price: { available: true, inputPerMillion: 2, groupMultiplier: 2 } },
    { serviceState: 'degraded', price: { available: true, inputPerMillion: 0.8, groupMultiplier: 0.8 } }
  ];

  assert.equal(lowestPrice(groups)?.groupMultiplier, 0.8);
});

test('lowestPrice returns no price when every priced group is unusable', () => {
  const groups = [
    { serviceState: 'failed', price: { available: true, inputPerMillion: 0 } },
    { serviceState: 'no_samples', price: { available: true, inputPerMillion: 1 } },
    { serviceState: 'unknown', price: { available: true, inputPerMillion: 2 } }
  ];

  assert.equal(lowestPrice(groups), null);
});

function loadTagHelpers() {
  const source = readFileSync(join(__dirname, 'dashboard.js'), 'utf8');
  const start = source.indexOf('function tagPickColor(sourceMap, palette)');
  const end = source.indexOf('// ---- 标签数据操作结束 ----');
  assert.notEqual(start, -1, 'tagPickColor helper is missing');
  assert.notEqual(end, -1, 'tag helper end marker is missing');
  const TAG_COLORS = ['mint', 'blue', 'violet', 'amber', 'rose', 'slate'];
  return Function('TAG_COLORS', `${source.slice(start, end)}; return { tagPickColor, tagCreate, tagRename, tagSetColor, tagSetSite, tagSnapshot };`)(TAG_COLORS);
}

const { tagCreate, tagRename, tagSetColor, tagSetSite, tagPickColor, tagSnapshot } = loadTagHelpers();

test('tagCreate adds a tag with the least-used palette color and rejects duplicates', () => {
  const tags = new Map();
  assert.deepEqual(tagCreate(tags, '新品监控'), { ok: true });
  assert.equal(tags.has('新品监控'), true);
  assert.equal(tags.get('新品监控').color, 'mint');
  assert.deepEqual([...tags.get('新品监控').sites], []);
  assert.equal(tagCreate(tags, '新品监控').error, '已存在同名标签。');
  assert.equal(tagCreate(tags, '').error, '请输入标签名称。');
  assert.equal(tags.size, 1);
});

test('tagPickColor prefers the least-used palette color', () => {
  const tags = new Map();
  tagCreate(tags, 'a');
  tagCreate(tags, 'b');
  assert.equal(tags.get('a').color, 'mint');
  assert.equal(tags.get('b').color, 'blue');
  tagSetColor(tags, 'b', 'mint');
  assert.equal(tagPickColor(tags, ['mint', 'blue', 'violet', 'amber', 'rose', 'slate']), 'blue');
});

test('tagRename migrates the storage key and preserves color and sites', () => {
  const tags = new Map([['旧名', { color: 'rose', sites: new Set(['星云中转']) }]]);
  assert.deepEqual(tagRename(tags, '旧名', '主力'), { ok: true });
  assert.equal(tags.has('旧名'), false);
  assert.ok(tags.get('主力'));
  assert.equal(tags.get('主力').color, 'rose');
  assert.deepEqual([...tags.get('主力').sites], ['星云中转']);
  assert.equal(tagRename(tags, '主力', '主力').ok, true);
  assert.equal(tagRename(tags, '主力', '').error, '请输入标签名称。');
  assert.equal(tagRename(tags, '不存在', '新名').error, '标签不存在。');
  tagCreate(tags, '新名');
  assert.equal(tagRename(tags, '主力', '新名').error, '已存在同名标签。');
});

test('tagSetColor only accepts palette colors and updates in place', () => {
  const tags = new Map([['x', { color: 'mint', sites: new Set() }]]);
  assert.equal(tagSetColor(tags, 'x', 'amber'), true);
  assert.equal(tags.get('x').color, 'amber');
  assert.equal(tagSetColor(tags, 'x', 'neon'), false);
  assert.equal(tags.get('x').color, 'amber');
  assert.equal(tagSetColor(tags, 'ghost', 'blue'), false);
});

test('tagSetSite attaches and detaches a site without touching other state', () => {
  const tags = new Map([['x', { color: 'mint', sites: new Set(['A']) }]]);
  assert.equal(tagSetSite(tags, 'B', 'x', true), true);
  assert.deepEqual([...tags.get('x').sites].sort(), ['A', 'B']);
  assert.equal(tagSetSite(tags, 'A', 'x', false), true);
  assert.deepEqual([...tags.get('x').sites], ['B']);
  assert.equal(tagSetSite(tags, 'C', 'ghost', true), false);
});

test('tagSnapshot is a deep copy that later mutations cannot corrupt', () => {
  const tags = new Map([['x', { color: 'mint', sites: new Set(['A']) }]]);
  const snap = tagSnapshot(tags);
  tags.get('x').color = 'rose';
  tags.get('x').sites.add('B');
  tags.set('y', { color: 'blue', sites: new Set() });
  assert.equal(snap.get('x').color, 'mint');
  assert.deepEqual([...snap.get('x').sites], ['A']);
  assert.equal(snap.has('y'), false);
  const restored = tagSnapshot(snap);
  assert.deepEqual([...restored.get('x').sites], ['A']);
  assert.equal(snap.get('x') === restored.get('x'), false);
});

test('tag manager panel keeps its containers and exposes the new affordances', () => {
  const html = readFileSync(join(__dirname, 'index.html'), 'utf8');
  const css = readFileSync(join(__dirname, 'dashboard.css'), 'utf8');
  const source = readFileSync(join(__dirname, 'dashboard.js'), 'utf8');
  assert.match(html, /id="customize-tags"/);
  assert.match(html, /id="customize-tab-tags"/);
  assert.match(source, /data-tag-swatch/);
  assert.match(source, /data-tag-color-option/);
  assert.match(source, /data-tag-menu-item/);
  assert.match(source, /data-tag-undo/);
  assert.match(source, /role="menuitemradio"/);
  assert.match(source, /data-tag-form/);
  assert.match(source, /data-tag-status/);
  assert.doesNotMatch(source, /tagFormMode|tagArmed|openTagMenu/);
  assert.doesNotMatch(css, /\.tag-dot|\.tag-confirm-x|\.tag-tool|\.tag-manager|\.tag-create|\.tag-menu i/);
});

test('tag controls keep generous touch targets and self-sufficient button styles', () => {
  const css = readFileSync(join(__dirname, 'dashboard.css'), 'utf8');
  assert.match(css, /\.public-dashboard \.tag-swatch \{[\s\S]{0,500}?width: 28px/);
  assert.match(css, /\.public-dashboard \.tag-swatch \{[\s\S]{0,500}?min-height: 28px/);
  assert.match(css, /\.public-dashboard \.tag-swatch \{[\s\S]{0,500}?appearance: none/);
  assert.match(css, /\.public-dashboard \.tag-color-option \{[\s\S]{0,500}?min-height: 36px/);
  assert.match(css, /\.public-dashboard \.tag-menu-item \{[\s\S]{0,500}?min-height: 38px/);
});

test('tag rows count their own sites, and site rows count their own tags', () => {
  const source = readFileSync(join(__dirname, 'dashboard.js'), 'utf8');
  assert.match(source, /const tagSiteCount = \(name\) => tags\.get\(name\)\?\.sites\.size \|\| 0;/);
  const item = source.slice(source.indexOf('function tagItemHTML'), source.indexOf('function tagItemHTML') + 700);
  assert.match(item, /tagSiteCount\(name\)/);
  assert.doesNotMatch(item, /siteTagCount\(name\)/);
  const siteRow = source.slice(source.indexOf('function siteTagRowHTML'), source.indexOf('function siteTagRowHTML') + 700);
  assert.match(siteRow, /siteTagCount\(name\)/);
});

function feedbackHarness(fetch) {
  const source = readFileSync(join(__dirname, 'dashboard.js'), 'utf8');
  const start = source.indexOf("document.querySelector('#feedback-form').addEventListener('submit'");
  const end = source.indexOf('\ninitializeTheme();', start);
  assert.ok(start >= 0 && end > start, 'feedback submission binding is missing');
  let handler;
  const input = { value: '' };
  const button = { disabled: false, textContent: '提交反馈' };
  const message = { textContent: '', dataset: {} };
  const form = { addEventListener: (_, callback) => { handler = callback; }, querySelector: () => button };
  const document = { querySelector: (selector) => selector === '#feedback-content' ? input : form };
  const session = { closed: false, reloads: 0 };
  const dialog = { close: () => { session.closed = true; } };
  const loadUser = async () => { session.reloads++; };
  const getUser = Function('document', 'feedbackMessage', 'feedbackDialog', 'loadUser', 'fetch', 'AbortSignal',
    `let currentUser = { username: 'test-user' }; ${source.slice(start, end)}; return () => currentUser;`)(document, message, dialog, loadUser, fetch, AbortSignal);
  return { input, button, message, session, getUser, submit: () => handler({ preventDefault() {}, currentTarget: form }) };
}

test('feedback prevents duplicate requests and preserves content for a failed submission retry', async () => {
  const requests = [];
  const pending = [];
  const feedback = feedbackHarness((url, options) => {
    requests.push({ url, options });
    return new Promise((resolve) => pending.push(resolve));
  });
  feedback.input.value = '  模型状态需要核对  ';
  const first = feedback.submit();
  assert.equal(feedback.button.disabled, true);
  assert.equal(feedback.button.textContent, '正在提交…');
  await feedback.submit();
  assert.equal(requests.length, 1);
  assert.equal(requests[0].url, '/api/v1/feedback');
  assert.equal(requests[0].options.method, 'POST');
  assert.deepEqual(JSON.parse(requests[0].options.body), { content: '模型状态需要核对' });
  assert.ok(requests[0].options.signal instanceof AbortSignal);
  pending.shift()({ ok: false, status: 503 });
  await first;
  assert.equal(feedback.input.value, '  模型状态需要核对  ');
  assert.equal(feedback.button.disabled, false);
  assert.equal(feedback.button.textContent, '提交反馈');
  assert.match(feedback.message.textContent, /提交失败/);

  const retry = feedback.submit();
  pending.shift()({ ok: true, status: 200 });
  await retry;
  assert.equal(requests.length, 2);
  assert.equal(feedback.input.value, '');
  assert.equal(feedback.message.textContent, '反馈已提交。');
  assert.equal(feedback.message.dataset.state, 'success');
  assert.equal(feedback.button.disabled, false);
});

test('feedback rejects blank content and recovers from a connection failure', async () => {
  let calls = 0;
  const feedback = feedbackHarness(async () => { calls++; throw new TypeError('Failed to fetch'); });
  feedback.input.value = '   ';
  await feedback.submit();
  assert.equal(calls, 0);
  assert.match(feedback.message.textContent, /请填写/);
  feedback.input.value = '请核对模型价格';
  await feedback.submit();
  assert.equal(calls, 1);
  assert.equal(feedback.input.value, '请核对模型价格');
  assert.equal(feedback.button.disabled, false);
  assert.equal(feedback.message.dataset.state, 'error');
  assert.match(feedback.message.textContent, /内容已保留/);
});

test('feedback refreshes the login state when the session expires', async () => {
  const feedback = feedbackHarness(async () => ({ ok: false, status: 401 }));
  feedback.input.value = '待提交的反馈';
  await feedback.submit();
  assert.equal(feedback.session.closed, true);
  assert.equal(feedback.session.reloads, 1);
  assert.equal(feedback.getUser(), null);
  assert.equal(feedback.input.value, '待提交的反馈');
  assert.equal(feedback.button.disabled, false);
});

function loadMembershipHelpers() {
  const source = readFileSync(join(__dirname, 'dashboard.js'), 'utf8');
  const start = source.indexOf('// ---- 会员与同步纯函数');
  const end = source.indexOf('// ---- 会员与同步纯函数结束 ----');
  assert.notEqual(start, -1, 'membership helper block is missing');
  assert.notEqual(end, -1, 'membership helper end marker is missing');
  return Function(`${source.slice(start, end)}; return { membershipState, membershipBadge, preferencesIsEmpty, mergePreferences, wishProgress, wishStatusBadge, renderMarkdown };`)();
}

const { membershipState, membershipBadge, preferencesIsEmpty, mergePreferences, wishProgress, wishStatusBadge, renderMarkdown } = loadMembershipHelpers();

test('membershipState classifies none, active and expired', () => {
  const now = 1_800_000_000_000;
  assert.equal(membershipState(0, now), 'none');
  assert.equal(membershipState(null, now), 'none');
  assert.equal(membershipState(now + 1, now), 'active');
  assert.equal(membershipState(now, now), 'expired');
  assert.equal(membershipState(now - 1, now), 'expired');
  assert.deepEqual(membershipBadge('active'), { text: '会员生效中', tone: 'healthy' });
  assert.deepEqual(membershipBadge('none'), { text: '未开通会员', tone: 'muted' });
});

test('mergePreferences prefers cloud, uploads local on first sync', () => {
  const local = { hidden: { sites: ['a'], providers: [], models: [] }, defaultHealthy: false, tags: {} };
  const cloud = { hidden: { sites: [], providers: [], models: [] }, defaultHealthy: true, tags: { x: { color: 'mint', sites: [] } } };
  assert.deepEqual(mergePreferences(local, cloud), { source: 'cloud', upload: false });
  assert.deepEqual(mergePreferences(local, { hidden: { sites: [], providers: [], models: [] }, defaultHealthy: false, tags: {} }), { source: 'local', upload: true });
  assert.deepEqual(mergePreferences({ hidden: { sites: [], providers: [], models: [] }, defaultHealthy: false, tags: {} }, null), { source: 'default', upload: false });
  assert.equal(preferencesIsEmpty(null), true);
  assert.equal(preferencesIsEmpty({ hidden: { sites: [] }, defaultHealthy: true, tags: {} }), false);
});

test('wishProgress handles undecided targets and caps the ratio', () => {
  assert.deepEqual(wishProgress(12, null), { undecided: true, percent: 0, label: '许愿目标尚未确定' });
  assert.deepEqual(wishProgress(12, 30), { undecided: false, percent: 40, label: '已许愿 12 / 30 LDC' });
  assert.equal(wishProgress(45, 30).percent, 100);
  assert.equal(wishProgress(0, 30).percent, 0);
  assert.equal(wishStatusBadge('open'), null);
  assert.equal(wishStatusBadge('reached').tone, 'healthy');
  assert.equal(wishStatusBadge('connected').tone, 'accent');
});

test('renderMarkdown escapes HTML and renders the supported subset', () => {
  assert.equal(renderMarkdown('<script>alert(1)</script>'), '<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>');
  assert.equal(renderMarkdown('## 标题'), '<h4>标题</h4>');
  assert.equal(renderMarkdown('**加粗** 与 *斜体*'), '<p><strong>加粗</strong> 与 <em>斜体</em></p>');
  assert.equal(renderMarkdown('`code`'), '<p><code>code</code></p>');
  assert.equal(renderMarkdown('[官网](https://example.com)'), '<p><a href="https://example.com" target="_blank" rel="noopener noreferrer">官网</a></p>');
  assert.equal(renderMarkdown('[坏](javascript:alert(1))'), '<p>[坏](javascript:alert(1))</p>');
  assert.equal(renderMarkdown('- 一\n- 二'), '<ul><li>一</li><li>二</li></ul>');
  assert.equal(renderMarkdown('1. 甲\n2. 乙'), '<ol><li>甲</li><li>乙</li></ol>');
  assert.equal(renderMarkdown('> 引用'), '<blockquote>引用</blockquote>');
  assert.equal(renderMarkdown('---'), '<hr>');
  assert.equal(renderMarkdown('第一行\n第二行'), '<p>第一行<br>第二行</p>');
});

function loadSortHelpers() {
  const source = readFileSync(join(__dirname, 'dashboard.js'), 'utf8');
  // Extract priceValue
  const pvStart = source.indexOf('function priceValue(price)');
  const pvEnd = source.indexOf('\nfunction lowestPrice');
  assert.notEqual(pvStart, -1, 'priceValue is missing');
  const { priceValue } = Function(`${source.slice(pvStart, pvEnd)}; return { priceValue };`)();
  // Extract compareCards (depends on compareRows)
  const crStart = source.indexOf('function compareRows(a, b)');
  const ccEnd = source.indexOf('\nfunction buildTimeline');
  assert.notEqual(crStart, -1, 'compareRows is missing');
  const { compareCards } = Function(`${source.slice(crStart, ccEnd)}; return { compareCards };`)();
  // Extract sort block
  const sbStart = source.indexOf('// ---- 排序纯函数（供面板与测试共用');
  const sbEnd = source.indexOf('// ---- 排序纯函数结束 ----');
  assert.notEqual(sbStart, -1, 'sort block start marker is missing');
  assert.notEqual(sbEnd, -1, 'sort block end marker is missing');
  return Function('priceValue', 'compareCards',
    `${source.slice(sbStart, sbEnd)}\n; return { sortMedian, timelineStability, priceValueForSort, smartBordaScores, sortGroupCards, smartSiteScore, SORT_OPTIONS, SMART_MODEL_WEIGHTS, SITE_Q_WEIGHTS, FX_RATES };`
  )(priceValue, compareCards);
}

const { sortMedian, timelineStability, priceValueForSort, smartBordaScores, sortGroupCards, smartSiteScore, SORT_OPTIONS, SMART_MODEL_WEIGHTS, SITE_Q_WEIGHTS } = loadSortHelpers();

test('sortMedian computes correct medians for odd, even, and empty arrays', () => {
  assert.equal(sortMedian([3, 1, 2]), 2);
  assert.equal(sortMedian([4, 1, 3, 2]), 2.5);
  assert.equal(sortMedian([42]), 42);
  assert.equal(sortMedian([]), null);
});

test('timelineStability returns null for sparse coverage and penalizes switching', () => {
  const fullHealthy = Array.from({ length: 48 }, () => ({ state: 'healthy', start: 0, end: 0 }));
  assert.equal(timelineStability(fullHealthy), 1.0);
  // Sparse: only 2 covered slots → null
  const sparse = Array.from({ length: 48 }, (_, i) => ({ state: i < 2 ? 'healthy' : 'no_samples', start: 0, end: 0 }));
  assert.equal(timelineStability(sparse), null);
  // Flapping: healthy ↔ failed alternating every 4 slots
  const flapping = Array.from({ length: 48 }, (_, i) => ({
    state: Math.floor(i / 4) % 2 === 0 ? 'healthy' : 'failed', start: 0, end: 0,
  }));
  const stab = timelineStability(flapping);
  assert.ok(stab > 0 && stab < 1, `stability ${stab} should be between 0 and 1`);
  // All failed
  const allFailed = Array.from({ length: 48 }, () => ({ state: 'failed', start: 0, end: 0 }));
  assert.equal(timelineStability(allFailed), 0);
});

test('smartBordaScores ranks low-latency above high-latency when availability is equal', () => {
  const fast = { serviceState: 'healthy', successRatio: 0.98, averageLatencyMs: 200, requestCount: 100, lowestPrice: { available: true, inputPerMillion: 2 } };
  const slow = { serviceState: 'healthy', successRatio: 0.98, averageLatencyMs: 5000, requestCount: 100, lowestPrice: { available: true, inputPerMillion: 2 } };
  const scores = smartBordaScores([fast, slow]);
  assert.ok(scores.get(fast) > scores.get(slow), 'fast should outrank slow when availability is equal');
});

test('sortGroupCards smart mode keeps failed cards below healthy even when cheap', () => {
  const cards = [
    { serviceState: 'healthy', successRatio: 0.90, averageLatencyMs: 1000, requestCount: 50, siteName: 'A', rawModelName: 'm', lowestPrice: { available: true, inputPerMillion: 10 } },
    { serviceState: 'failed', successRatio: 0.05, averageLatencyMs: 300, requestCount: 50, siteName: 'B', rawModelName: 'm', lowestPrice: { available: true, inputPerMillion: 0.1 } },
  ];
  const sorted = sortGroupCards(cards, 'smart');
  assert.equal(sorted[0].siteName, 'A', 'healthy must outrank failed regardless of price');
});

test('smartBordaScores penalizes low requestCount', () => {
  const highCount = { serviceState: 'healthy', successRatio: 0.95, averageLatencyMs: 500, requestCount: 100, lowestPrice: { available: true, inputPerMillion: 2 } };
  const lowCount = { serviceState: 'healthy', successRatio: 0.95, averageLatencyMs: 500, requestCount: 5, lowestPrice: { available: true, inputPerMillion: 2 } };
  const scores = smartBordaScores([highCount, lowCount]);
  assert.ok(scores.get(highCount) > scores.get(lowCount), 'higher requestCount should score higher');
});

test('smartBordaScores returns empty map for all-unsampled cards', () => {
  const noSample = { serviceState: 'no_samples', successRatio: null };
  const scores = smartBordaScores([noSample]);
  assert.equal(scores.size, 0);
});

test('sortGroupCards respects all sort modes', () => {
  const cards = [
    { serviceState: 'healthy', successRatio: 0.95, averageLatencyMs: 300, requestCount: 100, siteName: 'A', rawModelName: 'm', lowestPrice: { available: true, inputPerMillion: 5 } },
    { serviceState: 'healthy', successRatio: 0.99, averageLatencyMs: 800, requestCount: 100, siteName: 'B', rawModelName: 'm', lowestPrice: { available: true, inputPerMillion: 1 } },
    { serviceState: 'healthy', successRatio: 0.90, averageLatencyMs: 100, requestCount: 100, siteName: 'C', rawModelName: 'm', lowestPrice: { available: true, inputPerMillion: 10 } },
  ];
  // By price: cheapest first
  const byPrice = sortGroupCards(cards, 'price');
  assert.equal(byPrice[0].siteName, 'B'); // 1 perM
  assert.equal(byPrice[2].siteName, 'C'); // 10 perM
  // By availability: highest first
  const byAvail = sortGroupCards(cards, 'availability');
  assert.equal(byAvail[0].siteName, 'B'); // 0.99
  assert.equal(byAvail[2].siteName, 'C'); // 0.90
  // By latency: lowest first
  const byLat = sortGroupCards(cards, 'latency');
  assert.equal(byLat[0].siteName, 'C'); // 100ms
  assert.equal(byLat[2].siteName, 'B'); // 800ms
  // Smart: should have __smartScore attached
  const bySmart = sortGroupCards(cards, 'smart');
  assert.ok(bySmart[0].__smartScore != null, 'smart sort should attach __smartScore');
  assert.ok(bySmart[0].__smartScore >= bySmart[1].__smartScore, 'scores should be descending');
});

test('sortGroupCards puts no-samples at the bottom in all modes', () => {
  const cards = [
    { serviceState: 'no_samples', successRatio: null, averageLatencyMs: null, siteName: 'X', rawModelName: 'm', lowestPrice: null },
    { serviceState: 'healthy', successRatio: 0.95, averageLatencyMs: 300, requestCount: 50, siteName: 'Y', rawModelName: 'm', lowestPrice: { available: true, inputPerMillion: 2 } },
  ];
  for (const mode of ['price', 'availability', 'latency', 'smart']) {
    const sorted = sortGroupCards(cards, mode);
    assert.equal(sorted[sorted.length - 1].siteName, 'X', `no-samples last in ${mode} mode`);
  }
});

test('smartSiteScore returns null for zero participating cards', () => {
  assert.equal(smartSiteScore([{ serviceState: 'no_samples', successRatio: null }]), null);
  assert.equal(smartSiteScore([]), null);
});

test('smartSiteScore: specialist site outranks mediocre generalist', () => {
  // Specialist: 3 excellent models
  const specialist = Array.from({ length: 3 }, (_, i) => ({
    serviceState: 'healthy', successRatio: 0.98, averageLatencyMs: 300, requestCount: 100,
    timeline: Array.from({ length: 48 }, () => ({ state: 'healthy' })),
    siteName: 'Specialist', rawModelName: `m${i}`,
    lowestPrice: { available: true, inputPerMillion: 2 },
  }));
  // Mediocre generalist: 15 mediocre models
  const generalist = Array.from({ length: 15 }, (_, i) => ({
    serviceState: 'healthy', successRatio: 0.70, averageLatencyMs: 1500, requestCount: 50,
    timeline: Array.from({ length: 48 }, (_, j) => ({ state: j < 30 ? 'healthy' : 'failed' })),
    siteName: 'Generalist', rawModelName: `m${i}`,
    lowestPrice: { available: true, inputPerMillion: 3 },
  }));
  const sScore = smartSiteScore(specialist);
  const gScore = smartSiteScore(generalist);
  assert.ok(sScore.score > gScore.score, `specialist (${sScore.score.toFixed(3)}) should beat generalist (${gScore.score.toFixed(3)})`);
});

test('smartSiteScore: strong generalist outranks specialist of equal quality', () => {
  // 3 excellent models
  const specialist = Array.from({ length: 3 }, (_, i) => ({
    serviceState: 'healthy', successRatio: 0.95, averageLatencyMs: 400, requestCount: 100,
    timeline: Array.from({ length: 48 }, () => ({ state: 'healthy' })),
    siteName: 'Spec', rawModelName: `m${i}`, lowestPrice: { available: true, inputPerMillion: 2 },
  }));
  // 12 equally excellent models
  const bigStrong = Array.from({ length: 12 }, (_, i) => ({
    serviceState: 'healthy', successRatio: 0.95, averageLatencyMs: 400, requestCount: 100,
    timeline: Array.from({ length: 48 }, () => ({ state: 'healthy' })),
    siteName: 'BigStrong', rawModelName: `m${i}`, lowestPrice: { available: true, inputPerMillion: 2 },
  }));
  const sScore = smartSiteScore(specialist);
  const bScore = smartSiteScore(bigStrong);
  assert.ok(bScore.score > sScore.score, `big-strong (${bScore.score.toFixed(3)}) should beat specialist (${sScore.score.toFixed(3)})`);
});

test('smartSiteScore: single perfect model ranks below three-good specialist', () => {
  const single = [{
    serviceState: 'healthy', successRatio: 1.0, averageLatencyMs: 100, requestCount: 200,
    timeline: Array.from({ length: 48 }, () => ({ state: 'healthy' })),
    siteName: 'Solo', rawModelName: 'm0', lowestPrice: { available: true, inputPerMillion: 1 },
  }];
  const triple = Array.from({ length: 3 }, (_, i) => ({
    serviceState: 'healthy', successRatio: 0.95, averageLatencyMs: 300, requestCount: 100,
    timeline: Array.from({ length: 48 }, () => ({ state: 'healthy' })),
    siteName: 'Triple', rawModelName: `m${i}`, lowestPrice: { available: true, inputPerMillion: 2 },
  }));
  const sScore = smartSiteScore(single);
  const tScore = smartSiteScore(triple);
  assert.ok(tScore.score > sScore.score, `triple (${tScore.score.toFixed(3)}) should beat single (${sScore.score.toFixed(3)})`);
});

test('mergePreferences handles sorting field in emptiness check', () => {
  const defaultSorting = { hidden: { sites: [], providers: [], models: [] }, defaultHealthy: false, tags: {}, sorting: { model: 'default', site: 'default' } };
  assert.equal(preferencesIsEmpty(defaultSorting), true);
  const smartSorting = { hidden: { sites: [], providers: [], models: [] }, defaultHealthy: false, tags: {}, sorting: { model: 'smart', site: 'smart' } };
  assert.equal(preferencesIsEmpty(smartSorting), false);
  assert.deepEqual(mergePreferences(defaultSorting, smartSorting), { source: 'cloud', upload: false });
  assert.deepEqual(mergePreferences(smartSorting, defaultSorting), { source: 'local', upload: true });
});

test('public page wires account, redeem, recharge, wish pool and payment return', () => {
  const html = readFileSync(join(__dirname, 'index.html'), 'utf8');
  assert.match(html, /href="#wishes" data-nav-wishes/);
  assert.match(html, /href="#customize" data-nav-customize/);
  assert.match(html, /id="customize-page"/);
  assert.match(html, /data-customize-tab="display"/);
  assert.match(html, /data-customize-tab="sites"/);
  assert.match(html, /data-customize-tab="providers"/);
  assert.match(html, /data-customize-tab="models"/);
  assert.match(html, /data-customize-tab="tags"/);
  assert.match(html, /data-nav-badge="sites"/);
  assert.match(html, /id="customize-display-settings"/);
  assert.match(html, /id="customize-sites"/);
  assert.match(html, /id="customize-providers"/);
  assert.match(html, /id="customize-models"/);
  assert.match(html, /class="public-footer"/);
  assert.doesNotMatch(html, /customize-dialog/);
  assert.match(html, /id="user-action"/);
  assert.match(html, /id="user-menu"/);
  assert.match(html, /id="wish-page"/);
  assert.match(html, /id="wish-banner"/);
  assert.match(html, /id="redeem-dialog"/);
  assert.match(html, /id="recharge-dialog"/);
  assert.match(html, /id="wish-form-dialog"/);
  assert.match(html, /注册此站点需要邀请码/);
  assert.match(html, /id="pledge-dialog"/);
  const source = readFileSync(join(__dirname, 'dashboard.js'), 'utf8');
  assert.match(source, /renderMarkdown/);
  assert.match(source, /renderChipGrid/);
  assert.match(source, /customize-gate/);
  assert.match(source, /gate-wish-link/);
  assert.match(source, /CUSTOMIZE_PANEL_BY_TAB/);
  assert.match(source, /updateNavBadges/);
  assert.match(source, /userAction\.addEventListener\('click'/);
  assert.match(source, /window\.addEventListener\('hashchange', applyRoute\)/);
  assert.match(source, /scheduleCloudSave/);
  assert.match(source, /\/api\/v1\/me\/preferences/);
  assert.match(source, /\/api\/v1\/redeem/);
  assert.match(source, /\/api\/v1\/membership\/recharge/);
  assert.match(source, /\/api\/v1\/me\/wish-credit/);
  assert.match(source, /membershipMonthlyPriceLdc/);
  assert.match(source, /pledgeCreditAvailable/);
  assert.match(source, /\/api\/v1\/wishes/);
  assert.match(source, /\/api\/v1\/payment\/orders\//);
  assert.match(source, /membershipIs\(\) !== 'active'/);
  assert.match(source, /renderCustomizeGate/);
  // 排序功能接线
  assert.match(html, /id="sort-mode"/);
  assert.match(source, /data-customize-sort/);
  assert.match(source, /智能排序为会员专属/);
  assert.match(source, /saveSorting/);
  assert.match(source, /relayscope-sorting/);
});
