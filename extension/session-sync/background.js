importScripts('config.js', 'capture.js');

const DEFAULT_SERVER = self.RELAYPULSE_SERVER || 'http://127.0.0.1:8080';

const normalizeServer = (value) => String(value || '').trim().replace(/\/$/, '');

chrome.sidePanel.setPanelBehavior({ openPanelOnActionClick: true }).catch(() => {});

async function api(path, options = {}) {
  const headers = { 'Content-Type': 'application/json', ...(options.headers || {}) };
  const response = await fetch(`${DEFAULT_SERVER}${path}`, { ...options, headers, cache: 'no-store' });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(body.error || `服务器返回 ${response.status}`);
    error.status = response.status;
    throw error;
  }
  return body;
}

async function pending() {
  const token = await getToken();
  if (!token) throw new Error('请先输入后台生成的配对码。');
  const origin = `chrome-extension://${chrome.runtime.id}`;
  return api('/api/v1/session-sync/pending', { method: 'POST', headers: { Origin: origin, Authorization: `Bearer ${token}` } });
}

async function getToken() {
  const stored = await chrome.storage.local.get(['relaypulseSyncToken', 'relaypulseSyncServer']);
  return stored.relaypulseSyncServer === DEFAULT_SERVER ? String(stored.relaypulseSyncToken || '') : '';
}

async function pair(code) {
  const origin = `chrome-extension://${chrome.runtime.id}`;
  const result = await api('/api/v1/session-sync/exchange', { method: 'POST', body: JSON.stringify({ code }), headers: { Origin: origin } });
  await chrome.storage.local.set({ relaypulseSyncToken: result.token, relaypulseSyncServer: DEFAULT_SERVER });
  return result;
}

async function openSites(sites) {
  const privatePages = sites.filter((site) => site.adapterKey === 'model-market');
  const tabs = await chrome.tabs.query({});
  const openOrigins = new Set(tabs.flatMap((item) => {
    try { return [new URL(item.url).origin]; }
    catch { return []; }
  }));
  let opened = 0;
  for (const site of privatePages) {
    if (openOrigins.has(site.origin)) continue;
    await chrome.tabs.create({ url: site.loginUrl, active: false });
    opened += 1;
  }
  return { opened };
}

async function readSub2APITokens(site) {
  const tabs = await chrome.tabs.query({});
  const tab = tabs.find((item) => {
    try { return new URL(item.url).origin === site.origin; }
    catch { return false; }
  });
  if (!tab?.id) throw new Error(`${site.name} 没有已打开的登录页面`);
  const [{ result }] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    world: 'MAIN',
    func: () => ({
      accessToken: localStorage.getItem('auth_token') || '',
      refreshToken: localStorage.getItem('refresh_token') || '',
      tokenExpiresAt: Number(localStorage.getItem('token_expires_at')) || 0,
    }),
  });
  if (!result?.accessToken || !result?.refreshToken || !result?.tokenExpiresAt) {
    throw new Error(`${site.name} 未检测到有效登录，请在该页面完成登录`);
  }
  return { authType: 'sub2api_token', ...result };
}

async function capture(sites, accountCredentials) {
  const { bundles, skipped } = await self.RelayPulseCapture.prepareBundles(
    sites,
    accountCredentials,
    readSub2APITokens,
  );
  if (!bundles.length) {
    return { imported: 0, results: [], skipped };
  }
  const token = await getToken();
  if (!token) throw new Error('请先输入后台生成的配对码。');
  const result = await api('/api/v1/session-sync/batch', { method: 'POST', body: JSON.stringify({ bundles }), headers: { Authorization: `Bearer ${token}` } });
  await chrome.storage.local.remove('relaypulseSyncToken');
  return { ...result, skipped };
}

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  const run = async () => {
    switch (message.type) {
      case 'state': return { server: DEFAULT_SERVER };
      case 'pair': return pair(String(message.code || '').trim());
      case 'pending': return pending();
      case 'open': return openSites(message.sites || []);
      case 'capture': return capture(message.sites || [], message.accountCredentials || {});
      default: throw new Error('未知操作');
    }
  };
  run().then((value) => sendResponse({ ok: true, value })).catch((error) => sendResponse({ ok: false, error: error.message }));
  return true;
});
