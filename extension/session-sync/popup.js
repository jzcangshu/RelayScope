const $ = (selector) => document.querySelector(selector);
let pendingSites = [];
let accountCredentials = {};

const send = (message) => new Promise((resolve, reject) => {
  chrome.runtime.sendMessage(message, (response) => {
    if (chrome.runtime.lastError) { reject(new Error(chrome.runtime.lastError.message)); return; }
    if (!response?.ok) { reject(new Error(response?.error || '操作失败')); return; }
    resolve(response.value);
  });
});

async function requestOrigins(origins) {
  for (const value of origins) new URL(value);
}

function show(message, error = false) {
  $('#message').textContent = message;
  $('#message').classList.toggle('error', error);
}

function renderSites(sites) {
  const container = $('#sites');
  container.replaceChildren();
  if (!sites.length) {
    const empty = document.createElement('div');
    empty.className = 'empty';
    empty.textContent = '当前无需更新';
    container.append(empty);
    return;
  }
  for (const site of sites) {
    const item = document.createElement('article');
    const name = document.createElement('strong');
    const reason = document.createElement('span');
    name.textContent = site.name;
    reason.textContent = site.reason === 'verification_failed'
      ? '上次验证失败'
      : site.adapterKey === 'model-market' ? '浏览器登录' : '备份令牌';
    item.append(name, reason);
    container.append(item);
  }
}

async function loadPending() {
  try {
    const result = await send({ type: 'pending' });
    pendingSites = result.sites || [];
    $('#count').textContent = `${pendingSites.length} 个站点待更新`;
    renderSites(pendingSites);
    $('#open').disabled = !pendingSites.length;
    $('#sync').disabled = !pendingSites.length;
    show('请选择账号备份；Sub2API 站点需保持登录页面打开。');
    return pendingSites;
  } catch (error) {
    show(error.message, true);
    throw error;
  }
}

async function openPendingSites() {
  if (!pendingSites.length) return;
  await requestOrigins(pendingSites.flatMap((site) => [site.origin, site.loginUrl]));
  const result = await send({ type: 'open', sites: pendingSites });
  show(result.opened ? `已打开 ${result.opened} 个需浏览器登录的页面。` : '当前站点均可从备份导入。');
}

async function connect() {
  try {
    $('#reconnect').hidden = true;
    await loadPending();
    await openPendingSites();
  } catch (error) {
    show(error.message, true);
    $('#reconnect').hidden = false;
  }
}
$('#reconnect').onclick = connect;
$('#refresh').onclick = loadPending;
$('#open').onclick = async () => {
  try { await openPendingSites(); }
  catch (error) { show(error.message, true); }
};
$('#backup').onchange = async (event) => {
  accountCredentials = {};
  const file = event.target.files?.[0];
  if (!file) return;
  try {
    const backup = JSON.parse(await file.text());
    const accounts = Object.values(backup?.accounts?.accounts || {});
    for (const account of accounts) {
      let origin;
      try { origin = new URL(account.site_url).origin; }
      catch { continue; }
      const token = account?.account_info?.access_token;
      const userId = account?.account_info?.id;
      if (token && userId !== undefined && userId !== null) {
        accountCredentials[origin] = { authType: 'newapi_token', accessToken: String(token), userId: String(userId) };
      }
    }
    const matched = pendingSites.filter((site) => site.adapterKey === 'model-market' || accountCredentials[site.origin]).length;
    show(`备份已读取，可处理 ${matched}/${pendingSites.length} 个待更新站点。`);
  } catch {
    show('账号备份格式无效。', true);
  }
};
$('#sync').onclick = async () => {
  $('#sync').disabled = true;
  try {
    await requestOrigins(pendingSites.map((site) => site.origin));
    const result = await send({ type: 'capture', sites: pendingSites, accountCredentials });
    const failures = (result.results || []).filter((item) => !item.ok);
    const skipped = result.skipped || [];
    await loadPending();
    const details = [`已导入 ${result.imported} 个站点`];
    if (failures.length) details.push(`${failures.length} 个采集验证失败`);
    if (skipped.length) details.push(`${skipped.length} 个因无匹配令牌或登录态而跳过`);
    show(`${details.join('，')}。`, failures.length > 0);
  } catch (error) {
    $('#sync').disabled = false;
    show(error.message, true);
  }
};

(async () => {
  await connect();
})();
