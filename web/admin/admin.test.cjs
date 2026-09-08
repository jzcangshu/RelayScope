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
  assert.match(script, /saveRequest\([^\n]+, 'DELETE'/);
  assert.match(script, /run-status-filter/);
  assert.match(script, /X-CSRF-Token/);
  assert.match(script, /session-sync\/pair/);
});

const normalizedScript = script.replace(/\r\n/g, '\n');
function declaration(name) {
  const start = normalizedScript.search(new RegExp(`(?:async )?function ${name}\\(`));
  assert.notEqual(start, -1, `${name} is missing`);
  return normalizedScript.slice(start, normalizedScript.indexOf('\n}', start) + 2);
}

test('site filters distinguish fresh collection, stale data and missing sessions', () => {
  const helpers = normalizedScript.slice(normalizedScript.indexOf('const needsSession ='), normalizedScript.indexOf('const icon ='));
  const filterSites = Function('ACQ_ATTENTION', `${helpers}\n${declaration('filterSites')}; return filterSites;`)(new Set(['login_expired', 'collection_failed', 'challenge_pending', 'challenge_failed']));
  const samples = [
    { name: 'Ready', enabled: true, acquisitionState: 'fresh' },
    { name: 'Waiting', enabled: true, acquisitionState: 'stale' },
    { name: 'Failed', enabled: true, acquisitionState: 'collection_failed' },
    { name: 'Session', enabled: true, acquisitionState: 'fresh', sessionRequired: true, sessionConfigured: false },
    { name: 'Disabled', enabled: false, acquisitionState: 'fresh' }
  ];
  assert.deepEqual(filterSites(samples, '', 'healthy').map((s) => s.name), ['Ready']);
  assert.deepEqual(filterSites(samples, '', 'attention').map((s) => s.name), ['Failed', 'Session']);
  assert.deepEqual(filterSites(samples, '', 'sessions').map((s) => s.name), ['Session']);
  assert.deepEqual(filterSites(samples, '', 'stale').map((s) => s.name), ['Waiting']);
  assert.equal(filterSites(samples, '', 'enabled').length, 4);
});

test('overview navigation clears incompatible site filters before rendering', () => {
  const fields = { '#site-search': { value: 'Old site' }, '#site-state-filter': { value: 'disabled' } };
  let rendered;
  const location = { hash: '#overview' };
  const showSites = Function('$', 'renderSites', 'location', 'switchTab', `${declaration('showSites')}; return showSites;`)((id) => fields[id], () => { rendered = { query: fields['#site-search'].value, filter: fields['#site-state-filter'].value }; }, location, () => {});
  showSites('sessions');
  assert.deepEqual(rendered, { query: '', filter: 'sessions' });
  showSites('', 'Target site');
  assert.deepEqual(rendered, { query: 'Target site', filter: '' });
  assert.equal(location.hash, '#sites');
});

test('configuration editing preserves unknown properties and zero values', () => {
  const raw = { value: '{"hiddenSetting":"keep","timeoutSeconds":20}' };
  const fields = [{ dataset: { configKey: 'timeoutSeconds', configType: 'integer' }, value: '0', type: 'number' }];
  const read = Function('$', 'document', `${declaration('configFromFields')}; return configFromFields;`)(() => raw, { querySelectorAll: () => fields });
  assert.deepEqual(JSON.parse(read()), { hiddenSetting: 'keep', timeoutSeconds: 0 });
  raw.value = '{ unfinished';
  assert.throws(read);
  raw.value = '[]';
  assert.throws(read, /对象/);
});

test('optional source settings stay unset instead of overwriting adapter defaults', () => {
  const configValue = Function(`${declaration('configValue')}; return configValue;`)();
  assert.equal(configValue(undefined, { type: 'integer', minimum: 1 }), '');
  assert.equal(configValue(undefined, { type: 'integer', default: 24 }), 24);
  assert.equal(configValue(0, { type: 'integer', default: 24 }), 0);
  const raw = { value: '{"unknownOption":"keep","explicitEmpty":""}' };
  const fields = [
    { dataset: { configKey: 'pageSize', configType: 'integer' }, value: '', type: 'number' },
    { dataset: { configKey: 'catalogPath', configType: 'string' }, value: '', type: 'text' },
    { dataset: { configKey: 'explicitEmpty', configType: 'string' }, value: '', type: 'text' },
    { dataset: { configKey: 'availabilityMode', configType: 'string', configEnum: 'true' }, value: '', type: 'select-one' },
    { dataset: { configKey: 'pricingOptional', configType: 'boolean' }, checked: false, type: 'checkbox' }
  ];
  const read = Function('$', 'document', `${declaration('configFromFields')}; return configFromFields;`)(() => raw, { querySelectorAll: () => fields });
  assert.deepEqual(JSON.parse(read()), { unknownOption: 'keep', explicitEmpty: '' });
  fields[0].value = '100';
  fields[4].checked = true;
  raw.value = read();
  fields[4].checked = false;
  assert.deepEqual(JSON.parse(read()), { unknownOption: 'keep', explicitEmpty: '', pageSize: 100, pricingOptional: false });
});

test('protected mutations retain JSON and CSRF headers through the shared request helper', async () => {
  let captured;
  const save = Function('fetch', 'jsonHeaders', 'errorMessage', `${declaration('saveRequest')}; return saveRequest;`)(async (url, options) => { captured = { url, ...options }; return { ok: true }; }, () => ({ 'Content-Type': 'application/json', 'X-CSRF-Token': 'test-only-token' }), async () => 'failed');
  await save('/api/v1/admin/sites/8', 'PATCH', { enabled: false });
  assert.equal(captured.method, 'PATCH');
  assert.equal(captured.headers['X-CSRF-Token'], 'test-only-token');
  assert.deepEqual(JSON.parse(captured.body), { enabled: false });
  await save('/api/v1/admin/sites/8', 'DELETE');
  assert.equal(captured.method, 'DELETE');
  assert.equal(captured.body, undefined);
});

test('failed initial reads show an error state while later failures retain loaded content', async () => {
  let responseOK = false;
  const loadedSections = new Set();
  const failedSections = new Set();
  const region = { innerHTML: '' };
  const bindings = {
    loadedSections, failedSections,
    fetch: async () => ({ ok: responseOK, status: 503, json: async () => ({ rules: [] }) }),
    collectionViews: { rules: ['#rule-list', '模型规则'] }, $: () => region
  };
  const client = Function(...Object.keys(bindings), `${declaration('readJSON')}\n${declaration('renderCollectionState')}; return { readJSON, renderCollectionState };`)(...Object.values(bindings));
  await assert.rejects(client.readJSON('/api/v1/admin/rules'));
  assert.equal(client.renderCollectionState('rules'), true);
  assert.match(region.innerHTML, /暂时无法读取/);
  assert.doesNotMatch(region.innerHTML, /没有符合/);
  responseOK = true;
  await client.readJSON('/api/v1/admin/rules');
  region.innerHTML = 'last successful result';
  responseOK = false;
  await assert.rejects(client.readJSON('/api/v1/admin/rules'));
  assert.equal(client.renderCollectionState('rules'), false);
  assert.equal(region.innerHTML, 'last successful result');
});

test('failed form submissions show a persistent error and block duplicate saves', async () => {
  const button = { disabled: false, textContent: '保存站点' };
  const cancel = { disabled: false };
  const region = { textContent: '', hidden: true, focus() {} };
  const form = { value: 'unsaved input', querySelector: (selector) => selector === '[type="submit"]' ? button : region, querySelectorAll: () => [cancel], setAttribute() {}, removeAttribute() {} };
  const submit = Function(`${declaration('formError')}\n${declaration('submitForm')}; return submitForm;`)();
  let reject;
  let calls = 0;
  const action = () => { calls++; return new Promise((_, rejectPromise) => { reject = rejectPromise; }); };
  const pending = submit(form, '正在保存…', action);
  assert.equal(button.disabled, true);
  assert.equal(cancel.disabled, true);
  await submit(form, '正在保存…', action);
  assert.equal(calls, 1);
  reject(new Error('validation failed'));
  await pending;
  assert.equal(region.hidden, false);
  assert.equal(region.textContent, 'validation failed');
  assert.equal(form.value, 'unsaved input');
  assert.equal(button.disabled, false);
  assert.equal(cancel.disabled, false);
  assert.equal(button.textContent, '保存站点');
});

test('administrator theme initialization respects the existing content security policy', () => {
  assert.match(html, /<script src="\/assets\/theme\.js"><\/script>/);
  assert.doesNotMatch(html, /<script>[^<]/);
  assert.doesNotMatch(html, /\sstyle="/);
  assert.doesNotMatch(script, /window\.prompt/);
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
