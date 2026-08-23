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
  assert.match(theme, /localStorage\.getItem\('relaypulse-theme'\)/);
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
