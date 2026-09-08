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
  assert.equal((html.match(/class="icon-button"/g) || []).length, 3);
  assert.equal((html.match(/class="icon-button"[^>]*>[\s\S]*?<svg/g) || []).length, 3);
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
