const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const html = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
const script = fs.readFileSync(path.join(__dirname, 'admin.js'), 'utf8');

test('admin surface exposes the five operational views and logout', () => {
  for (const view of ['overview', 'sites', 'rules', 'runs', 'unmatched', 'system']) {
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
