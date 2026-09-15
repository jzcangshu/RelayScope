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

test('public dashboard uses a compact bell control and single announcement title', () => {
  const html = readFileSync(join(__dirname, 'index.html'), 'utf8');
  const css = readFileSync(join(__dirname, 'dashboard.css'), 'utf8');
  assert.match(html, /id="announcement-action" class="theme-toggle announcement-action"/);
  assert.match(html, /运行状态通知/);
  assert.doesNotMatch(html, /service-status|announcement-count|运行公告/);
  assert.equal((html.match(/class="icon-button"/g) || []).length, 8);
  assert.equal((html.match(/class="icon-button"[^>]*>[\s\S]*?<svg/g) || []).length, 8);
  assert.match(css, /detail-head\.announcement-head h2/);
  assert.match(css, /font-size: 23px/);
  assert.match(css, /color: #000/);
  assert.match(css, /data-theme="dark"[^\n]+color: #fff/);
  assert.match(css, /\.icon-button[\s\S]*?width: 34px[\s\S]*?height: 34px/);
  assert.match(css, /\.icon-button svg[\s\S]*?width: 19px[\s\S]*?height: 19px/);
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
  assert.match(source, /announcementDialog\.showModal\(\)/);
  assert.match(source, /failureCode/);
  assert.match(source, /setInterval\(loadRows, 60000\)/);
  assert.doesNotMatch(source, /数据未变化/);
  assert.doesNotMatch(source, /announcementCount/);
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
  return Function(`${source.slice(start, end)}; return { membershipState, membershipBadge, preferencesIsEmpty, mergePreferences, wishProgress, wishStatusBadge };`)();
}

const { membershipState, membershipBadge, preferencesIsEmpty, mergePreferences, wishProgress, wishStatusBadge } = loadMembershipHelpers();

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

test('public page wires account, redeem, recharge, wish pool and payment return', () => {
  const html = readFileSync(join(__dirname, 'index.html'), 'utf8');
  assert.match(html, /href="#wishes" data-nav-wishes/);
  assert.match(html, /id="user-action"/);
  assert.match(html, /id="user-menu"/);
  assert.match(html, /id="wish-page"/);
  assert.match(html, /id="redeem-dialog"/);
  assert.match(html, /id="recharge-dialog"/);
  assert.match(html, /id="wish-form-dialog"/);
  assert.match(html, /注册此站点需要邀请码/);
  assert.match(html, /id="pledge-dialog"/);
  const source = readFileSync(join(__dirname, 'dashboard.js'), 'utf8');
  assert.match(source, /userAction\.addEventListener\('click'/);
  assert.match(source, /window\.addEventListener\('hashchange', applyRoute\)/);
  assert.match(source, /scheduleCloudSave/);
  assert.match(source, /\/api\/v1\/me\/preferences/);
  assert.match(source, /\/api\/v1\/redeem/);
  assert.match(source, /\/api\/v1\/membership\/recharge/);
  assert.match(source, /\/api\/v1\/wishes/);
  assert.match(source, /\/api\/v1\/payment\/orders\//);
  assert.match(source, /membershipIs\(\) !== 'active'/);
  assert.match(source, /renderCustomizeGate/);
});
