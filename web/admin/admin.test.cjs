const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const html = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
const script = fs.readFileSync(path.join(__dirname, 'admin.js'), 'utf8');
const styles = fs.readFileSync(path.join(__dirname, 'admin.css'), 'utf8');

test('admin surface exposes every operational view and logout', () => {
  for (const view of ['overview', 'sites', 'rules', 'runs', 'unmatched', 'feedback', 'system']) {
    assert.match(html, new RegExp(`data-tab="${view}"`));
  }
  assert.match(html, /id="logout"/);
  assert.match(script, /\/api\/v1\/admin\/logout/);
});

test('admin client uses escaped rendering and schema-driven config fields', () => {
  assert.match(script, /function renderConfigFields/);
  assert.match(script, /function configFromFields/);
  assert.match(script, /escapeHTML/);
  assert.match(script, /\/api\/v1\/admin\/unmatched/);
  assert.match(script, /data-config-key/);
});

test('admin client wires destructive and filtered workflows to protected API calls', () => {
  assert.match(script, /method: 'DELETE'/);
  assert.match(script, /run-status-filter/);
  assert.match(script, /X-CSRF-Token/);
  assert.match(script, /session-sync\/pair/);
});

test('blocked keyword feature is wired through dialog, config, and row badges', () => {
  assert.match(html, /id="site-blocked"/);
  assert.match(script, /blockedKeywords/);
  assert.match(script, /function blockedKeywordsOf/);
  assert.match(script, /chip-blocked/);
  assert.match(styles, /\.chip-blocked/);
});

test('admin UI ships its own stylesheet with theme tokens and reduced motion', () => {
  assert.match(html, /\/admin\/admin\.css/);
  assert.match(styles, /\[data-theme="dark"\]/);
  assert.match(styles, /prefers-reduced-motion/);
  assert.match(styles, /:focus-visible/);
  assert.doesNotMatch(styles, /@import url\(/);
});

test('site and rule lists support client-side search without extra requests', () => {
  assert.match(html, /id="site-search"/);
  assert.match(html, /id="rule-search"/);
  assert.match(script, /function renderSites/);
  assert.match(script, /function renderRules/);
  assert.match(script, /addEventListener\('input', renderSites\)/);
  assert.match(script, /addEventListener\('input', renderRules\)/);
});

test('session import uses a dialog instead of window.prompt', () => {
  assert.match(html, /id="session-dialog"/);
  assert.match(script, /function openSessionDialog/);
  assert.match(script, /\/api\/v1\/admin\/sites\/\$\{sessionDialogSiteId\}\/session/);
});

test('navigation deep links hash-based tabs and sidebar has aria-current', () => {
  assert.match(script, /addEventListener\('hashchange'/);
  assert.match(script, /function switchTab/);
  assert.match(script, /aria-current/);
  assert.match(html, /class="sidebar-nav"/);
});
