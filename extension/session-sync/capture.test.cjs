const test = require('node:test');
const assert = require('node:assert/strict');
const { prepareBundles } = require('./capture.js');

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
