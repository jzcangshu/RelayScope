const test = require('node:test');
const assert = require('node:assert/strict');
const { prepareBundles } = require('./capture.js');
const fs = require('node:fs');
const path = require('node:path');

test('keeps exact matches and skips missing backup credentials', async () => {
  const sites = [
    { id: 1, name: 'matched', origin: 'https://matched.example', adapterKey: 'newapi' },
    { id: 2, name: 'missing', origin: 'https://missing.example', adapterKey: 'newapi' },
  ];
  const credentials = {
    'https://matched.example': { authType: 'newapi_token', accessToken: 'token', userId: '7' },
  };

  const result = await prepareBundles(sites, credentials, async () => null);

  assert.deepEqual(result.bundles, [{
    siteId: 1,
    origin: 'https://matched.example',
    authType: 'newapi_token',
    accessToken: 'token',
    userId: '7',
  }]);
  assert.deepEqual(result.skipped, [{
    siteId: 2,
    name: 'missing',
    reason: '在备份中没有精确匹配的访问令牌',
  }]);
});

test('isolates browser credential failures per site', async () => {
  const sites = [
    { id: 1, name: 'browser', origin: 'https://browser.example', adapterKey: 'model-market' },
    { id: 2, name: 'backup', origin: 'https://backup.example', adapterKey: 'newapi' },
  ];

  const result = await prepareBundles(
    sites,
    { 'https://backup.example': { authType: 'newapi_token', accessToken: 'token', userId: '9' } },
    async () => { throw new Error('未检测到有效登录'); },
  );

  assert.equal(result.bundles.length, 1);
  assert.equal(result.bundles[0].siteId, 2);
  assert.deepEqual(result.skipped, [{ siteId: 1, name: 'browser', reason: '未检测到有效登录' }]);
});

test('extension uses pairing tokens instead of embedding the administrator password', () => {
  const background = fs.readFileSync(path.join(__dirname, 'background.js'), 'utf8');
  const config = fs.readFileSync(path.join(__dirname, 'config.example.js'), 'utf8');
  const popup = fs.readFileSync(path.join(__dirname, 'popup.js'), 'utf8');
  const manifest = JSON.parse(fs.readFileSync(path.join(__dirname, 'manifest.json'), 'utf8'));
  assert.doesNotMatch(background, /RELAYPULSE_ADMIN_PASSWORD|VerifyPassword|password:\s*BUILTIN/);
  assert.doesNotMatch(config, /ADMIN_PASSWORD|admin credential/i);
  assert.match(popup, /requestOrigins\(\[state\.server\]\)/);
  assert.ok(manifest.optional_host_permissions);
  assert.equal(manifest.host_permissions, undefined);
});
