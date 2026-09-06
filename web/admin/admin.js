const $ = (selector) => document.querySelector(selector);
const csrf = () => document.cookie.split('; ').find((item) => item.startsWith('relayscope_csrf='))?.split('=')[1] || '';
const jsonHeaders = () => ({ 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() });
const escapeHTML = (value) => String(value ?? '').replace(/[&<>"']/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]);
const words = (value) => String(value || '').split(/[,，\n]+/).map((item) => item.trim()).filter(Boolean);
let sites = [];
let adapters = [];
let rules = [];
let meta = {};
let unmatchedCount = 0;
let sessionDialogSiteId = 0;

const ACQ_STATES = {
  fresh: ['正常', 'chip-fresh'],
  collecting: ['采集中', 'chip-collecting'],
  stale: ['待采集', 'chip-stale'],
  collection_failed: ['采集失败', 'chip-danger'],
  login_expired: ['登录失效', 'chip-warn'],
  challenge_pending: ['等待验证', 'chip-warn'],
  challenge_failed: ['验证失败', 'chip-danger'],
};
const ACQ_ATTENTION = new Set(['collection_failed', 'login_expired', 'challenge_pending', 'challenge_failed']);
const TAB_TITLES = { overview: '概览', sites: '站点管理', rules: '模型规则', runs: '采集记录', unmatched: '未匹配模型', feedback: '用户反馈', system: '系统' };
const THEME_ICONS = { auto: 'i-monitor', light: 'i-sun', dark: 'i-moon' };
const THEME_LABELS = { auto: '主题：跟随系统', light: '主题：浅色', dark: '主题：深色' };

function toast(message, kind = 'info') {
  const node = document.createElement('div');
  node.className = `toast toast-${kind}`;
  node.textContent = message;
  $('#toast-region').append(node);
  setTimeout(() => { node.classList.add('leaving'); setTimeout(() => node.remove(), 220); }, 3600);
}

async function errorMessage(response, fallback) {
  try {
    const payload = await response.json();
    return payload.error || payload.message || fallback;
  } catch { return fallback; }
}

function adapterFor(key) { return adapters.find((item) => item.key === key); }
function configValue(value, property) {
  if (value !== undefined && value !== null) return value;
  return property.default ?? (property.type === 'boolean' ? false : property.type === 'number' || property.type === 'integer' ? 0 : '');
}
function renderConfigFields(rawConfig = '{}') {
  const adapter = adapterFor($('#site-adapter').value);
  const properties = adapter?.configSchema?.properties || {};
  let values = {};
  try { values = JSON.parse(rawConfig) || {}; } catch { values = {}; }
  const fields = Object.entries(properties).filter(([, property]) => ['string', 'number', 'integer', 'boolean'].includes(property.type));
  $('#site-config-fields').innerHTML = fields.length ? fields.map(([key, property]) => {
    const value = configValue(values[key], property);
    const escaped = escapeHTML(value);
    if (property.enum?.length) return `<label class="field">${escapeHTML(property.title || key)}<select data-config-key="${escapeHTML(key)}">${property.enum.map((item) => `<option value="${escapeHTML(item)}" ${String(item) === String(value) ? 'selected' : ''}>${escapeHTML(item)}</option>`).join('')}</select></label>`;
    if (property.type === 'boolean') return `<div class="field field-inline span-2"><label class="check"><input type="checkbox" data-config-key="${escapeHTML(key)}" ${value ? 'checked' : ''}> ${escapeHTML(property.title || key)}</label></div>`;
    return `<label class="field">${escapeHTML(property.title || key)}<input data-config-key="${escapeHTML(key)}" type="${property.type === 'string' ? 'text' : 'number'}" data-config-type="${escapeHTML(property.type)}" value="${escaped}"${property.minimum !== undefined ? ` min="${property.minimum}"` : ''}></label>`;
  }).join('') : '<p class="muted" style="margin:0; grid-column: span 2">此适配器没有可视化配置字段，可展开下方高级 JSON 编辑。</p>';
}
function configFromFields() {
  let values = {};
  try { values = JSON.parse($('#site-config').value) || {}; } catch { values = {}; }
  document.querySelectorAll('[data-config-key]').forEach((field) => {
    const key = field.dataset.configKey;
    if (field.type === 'checkbox') values[key] = field.checked;
    else if (field.dataset.configType === 'integer') values[key] = Number.parseInt(field.value, 10);
    else if (field.dataset.configType === 'number') values[key] = Number(field.value);
    else values[key] = field.value;
  });
  return JSON.stringify(values);
}
function blockedKeywordsOf(site) {
  try { return JSON.parse(site.adapterConfig || '{}')?.blockedKeywords || []; } catch { return []; }
}

/* ---------- 导航与主题 ---------- */

function switchTab(tab) {
  if (!TAB_TITLES[tab]) tab = 'overview';
  document.querySelectorAll('[data-tab]').forEach((item) => {
    if (item.dataset.tab === tab) item.setAttribute('aria-current', 'page');
    else item.removeAttribute('aria-current');
  });
  document.querySelectorAll('.panels > .panel').forEach((panel) => { panel.hidden = panel.id !== `${tab}-panel`; });
  $('#page-title').textContent = TAB_TITLES[tab];
}
window.addEventListener('hashchange', () => switchTab(location.hash.slice(1)));

const systemTheme = matchMedia('(prefers-color-scheme: dark)');
let themePreference = 'auto';
try { themePreference = localStorage.getItem('relayscope-theme') || 'auto'; } catch {}
function applyTheme() {
  const mode = themePreference === 'auto' ? (systemTheme.matches ? 'dark' : 'light') : themePreference;
  document.documentElement.dataset.theme = mode;
  $('#theme-icon-use')?.setAttribute('href', `#${THEME_ICONS[themePreference]}`);
  $('#theme-toggle')?.setAttribute('aria-label', THEME_LABELS[themePreference]);
  $('#theme-label').textContent = THEME_LABELS[themePreference];
  $('#theme-toggle')?.setAttribute('title', THEME_LABELS[themePreference]);
}
systemTheme.addEventListener('change', () => { if (themePreference === 'auto') applyTheme(); });

/* ---------- 数据加载 ---------- */

async function loadAll() {
  await Promise.all([loadMeta(), loadAdapters(), loadSites(), loadRules(), loadRuns(), loadConflicts(), loadFeedback(), loadUnmatched()]);
  renderOverview();
  const stamp = new Date().toLocaleTimeString('zh-CN', { hour12: false });
  $('#last-refresh').textContent = `更新于 ${stamp}`;
}
async function loadMeta() { const response = await fetch('/api/v1/meta'); meta = response.ok ? await response.json() : {}; renderSystem(); }
async function loadAdapters() { const response = await fetch('/api/v1/admin/adapters'); adapters = response.ok ? (await response.json()).adapters || [] : []; $('#site-adapter').innerHTML = adapters.map((item) => `<option value="${escapeHTML(item.key)}">${escapeHTML(item.displayName)}</option>`).join(''); renderConfigFields($('#site-config').value); }
async function loadSites() {
  const response = await fetch('/api/v1/admin/sites'); if (!response.ok) return;
  sites = (await response.json()).sites || [];
  $('#dashboard-message').textContent = `已登记 ${sites.length} 个站点`;
  const siteSelect = $('#run-site-filter');
  const current = siteSelect.value;
  siteSelect.innerHTML = '<option value="">全部站点</option>' + sites.map((site) => `<option value="${site.id}">${escapeHTML(site.name)}（#${site.id}）</option>`).join('');
  siteSelect.value = current;
  renderSites();
}
function renderSites() {
  const search = $('#site-search').value.trim().toLowerCase();
  const stateFilter = $('#site-state-filter').value;
  const visible = sites.filter((site) => {
    if (search && !`${site.name} ${site.sourceUrl} ${site.baseUrl}`.toLowerCase().includes(search)) return false;
    if (stateFilter === 'disabled') return !site.enabled;
    if (stateFilter === 'attention') return site.enabled && (ACQ_ATTENTION.has(site.acquisitionState) || (site.sessionRequired && !site.sessionConfigured));
    if (stateFilter === 'healthy') return site.enabled && !ACQ_ATTENTION.has(site.acquisitionState) && !(site.sessionRequired && !site.sessionConfigured);
    return true;
  });
  $('#site-list').innerHTML = visible.length ? visible.map((site) => {
    const [stateLabel, stateClass] = ACQ_STATES[site.acquisitionState] || [site.acquisitionState, 'chip-muted'];
    const blocked = blockedKeywordsOf(site);
    const nextRun = site.nextRunAt ? new Date(site.nextRunAt).toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' }) : '—';
    const attention = site.enabled && (ACQ_ATTENTION.has(site.acquisitionState) || (site.sessionRequired && !site.sessionConfigured));
    return `<article class="site-card${site.enabled ? '' : ' site-disabled'}" data-site-card="${site.id}">
      <div class="site-main">
        <div class="site-title-line"><strong>${escapeHTML(site.name)}</strong><span class="chip ${site.enabled ? stateClass : 'chip-muted'}"><span class="dot"></span>${site.enabled ? escapeHTML(stateLabel) : '已停用'}</span>${blocked.length ? `<span class="chip chip-blocked" title="屏蔽关键词：${escapeHTML(blocked.join('，'))}"><svg class="icon" aria-hidden="true"><use href="#i-ban"/></svg>${blocked.length}</span>` : ''}</div>
        <span class="site-url mono">${escapeHTML(site.sourceUrl || site.baseUrl)}</span>
        <div class="site-facts"><span>${escapeHTML(site.adapterKey)}</span><span class="sep">·</span><span>每 ${Math.round(site.intervalSeconds / 60)} 分钟</span><span class="sep">·</span><span>下次 ${nextRun}</span>${site.sessionRequired ? `<span class="sep">·</span><span>${site.sessionConfigured ? '已有登录态' : '待同步登录态'}</span>` : ''}${site.enabled && attention && site.customFailureReason ? `<span class="sep">·</span><span>公告：${escapeHTML(site.customFailureReason)}</span>` : ''}</div>
      </div>
      <div class="site-actions">
        <button class="btn btn-primary" data-collect="${site.id}" type="button"><svg class="icon" aria-hidden="true"><use href="#i-play"/></svg>采集</button>
        <button class="btn" data-edit-site="${site.id}" type="button"><svg class="icon" aria-hidden="true"><use href="#i-pencil"/></svg>编辑</button>
        <button class="btn" data-session="${site.id}" type="button"><svg class="icon" aria-hidden="true"><use href="#i-key"/></svg>登录态</button>
        <button class="btn" data-toggle="${site.id}" type="button">${site.enabled ? '停用' : '启用'}</button>
        <button class="btn btn-danger" data-delete-site="${site.id}" type="button" aria-label="删除站点 ${escapeHTML(site.name)}"><svg class="icon" aria-hidden="true"><use href="#i-trash"/></svg></button>
      </div>
    </article>`;
  }).join('') : `<div class="empty-state"><svg class="icon" aria-hidden="true"><use href="#i-inbox"/></svg><p>没有符合条件的站点。</p></div>`;
  document.querySelectorAll('[data-edit-site]').forEach((button) => button.onclick = () => openSite(Number(button.dataset.editSite)));
  document.querySelectorAll('[data-toggle]').forEach((button) => button.onclick = () => toggleSite(Number(button.dataset.toggle)));
  document.querySelectorAll('[data-collect]').forEach((button) => button.onclick = () => collectSite(Number(button.dataset.collect)));
  document.querySelectorAll('[data-session]').forEach((button) => button.onclick = () => openSessionDialog(Number(button.dataset.session)));
  document.querySelectorAll('[data-delete-site]').forEach((button) => button.onclick = () => deleteSite(Number(button.dataset.deleteSite)));
}
async function loadRules() { const response = await fetch('/api/v1/admin/rules'); if (!response.ok) return; rules = (await response.json()).rules || []; $('#nav-rules-count').textContent = rules.length; $('#nav-rules-count').hidden = false; renderRules(); }
function renderRules() {
  const search = $('#rule-search').value.trim().toLowerCase();
  const visible = rules.filter((rule) => !search || `${rule.canonicalName} ${rule.provider}`.toLowerCase().includes(search));
  $('#rule-summary').textContent = `${visible.length} / ${rules.length} 条`;
  $('#rule-list').innerHTML = visible.length ? visible.map((rule) => `<div class="row-card"><div class="row-main"><div class="row-title"><strong class="mono">${escapeHTML(rule.canonicalName)}</strong>${rule.enabled ? '' : '<span class="chip chip-muted">已停用</span>'}${rule.generated ? '<span class="chip chip-muted">内置</span>' : ''}</div><div class="row-sub">${escapeHTML(rule.provider)}</div></div><div class="row-side"><span>优先级 ${escapeHTML(rule.priority)}</span><button class="btn" data-edit-rule="${rule.id}" type="button">编辑</button>${rule.generated ? '' : `<button class="btn btn-danger" data-delete-rule="${rule.id}" type="button">删除</button>`}</div></div>`).join('') : '<div class="empty-state"><p>没有符合条件的规则。</p></div>';
  document.querySelectorAll('[data-edit-rule]').forEach((button) => button.onclick = () => openRule(Number(button.dataset.editRule)));
  document.querySelectorAll('[data-delete-rule]').forEach((button) => button.onclick = () => deleteRule(Number(button.dataset.deleteRule)));
}
const runStatus = (status) => ({ success: ['成功', 'chip-fresh'], partial: ['部分完成', 'chip-warn'], running: ['采集中', 'chip-collecting'], failed: ['失败', 'chip-danger'] }[status] || [status || '未知', 'chip-muted']);
const formatRunTime = (value) => new Date(value).toLocaleString('zh-CN', { hour12: false });
function formatDuration(run) {
  if (!run.finishedAt) return '尚未完成';
  const milliseconds = Math.max(0, new Date(run.finishedAt) - new Date(run.startedAt));
  if (milliseconds < 1000) return `${milliseconds} 毫秒`;
  if (milliseconds < 60000) return `${(milliseconds / 1000).toFixed(milliseconds < 10000 ? 1 : 0)} 秒`;
  const minutes = Math.floor(milliseconds / 60000);
  return `${minutes} 分 ${Math.round((milliseconds % 60000) / 1000)} 秒`;
}
function renderRun(run) {
  const [label, stateClass] = runStatus(run.status);
  const error = run.errorCode || run.errorMessage;
  return `<div class="run-entry">
    <div class="run-entry-head"><span class="chip ${stateClass}">${escapeHTML(label)}</span><strong>${escapeHTML(formatRunTime(run.startedAt))}</strong><span class="muted">耗时 ${escapeHTML(formatDuration(run))}</span></div>
    <div class="run-facts"><span><b>${run.modelsSeen}</b> 模型</span><span><b>${run.groupsSeen}</b> 分组</span><span>目录 <b>${run.catalogComplete ? '完整' : '不完整'}</b></span><span>适配器 <b>${escapeHTML(run.adapterKey)}</b></span></div>
    ${error ? `<div class="run-error"><strong>${escapeHTML(run.errorCode || '采集错误')}</strong>${run.errorMessage ? `<pre>${escapeHTML(run.errorMessage)}</pre>` : ''}</div>` : ''}
  </div>`;
}
async function loadRuns() {
  const query = new URLSearchParams(); if ($('#run-status-filter').value) query.set('status', $('#run-status-filter').value); if ($('#run-site-filter').value) query.set('site', $('#run-site-filter').value); query.set('limit', '100');
  const response = await fetch(`/api/v1/admin/runs?${query}`); if (!response.ok) return;
  const runs = (await response.json()).runs || [];
  const grouped = new Map();
  runs.forEach((run) => { if (!grouped.has(run.siteId)) grouped.set(run.siteId, []); grouped.get(run.siteId).push(run); });
  const archives = [...grouped.values()].map((items) => items.sort((left, right) => new Date(right.startedAt) - new Date(left.startedAt)));
  archives.sort((left, right) => Number(right[0].status !== 'success') - Number(left[0].status !== 'success') || left[0].siteName.localeCompare(right[0].siteName, 'zh-CN'));
  $('#run-summary').textContent = `${archives.length} 个站点 · 共 ${runs.length} 条近期记录`;
  $('#run-list').innerHTML = archives.length ? archives.map((items) => {
    const latest = items[0];
    const [latestLabel, latestClass] = runStatus(latest.status);
    const successCount = items.filter((run) => run.status === 'success').length;
    const partialCount = items.filter((run) => run.status === 'partial').length;
    const failureCount = items.filter((run) => run.status === 'failed').length;
    let consecutiveFailures = 0;
    for (const run of items) { if (run.status !== 'failed') break; consecutiveFailures++; }
    return `<details class="run-site" ${latest.status === 'success' ? '' : 'open'}>
      <summary>
        <span class="run-disclosure" aria-hidden="true">›</span>
        <span class="run-site-title"><strong>${escapeHTML(latest.siteName)}</strong><span class="muted">最近 ${items.length} 次</span></span>
        <span class="run-site-counts"><b class="healthy">成功 ${successCount}</b>${partialCount ? `<b class="degraded">部分 ${partialCount}</b>` : ''}<b class="${failureCount ? 'failed' : 'muted'}">失败 ${failureCount}</b>${consecutiveFailures ? `<b class="failed">连续 ${consecutiveFailures} 次</b>` : ''}</span>
        <span class="run-site-latest"><span class="chip ${latestClass}">${escapeHTML(latestLabel)}</span><time>${escapeHTML(formatRunTime(latest.startedAt))}</time></span>
      </summary>
      <div class="run-history">${items.map(renderRun).join('')}</div>
    </details>`;
  }).join('') : '<div class="empty-state"><p>暂无采集记录。</p></div>';
}
async function loadUnmatched() { const response = await fetch('/api/v1/admin/unmatched?limit=200'); if (!response.ok) return; const models = (await response.json()).models || []; unmatchedCount = models.length; $('#nav-unmatched-count').textContent = models.length || ''; renderUnmatched(models); }
function renderUnmatched(models) {
  $('#unmatched-list').innerHTML = models.length ? models.map((item) => `<div class="row-card"><div class="row-main"><div class="row-title"><strong class="mono">${escapeHTML(item.rawModelName)}</strong></div><div class="row-sub">${escapeHTML(item.siteName)}</div></div><div class="row-side"><span>${escapeHTML(item.providerHint || '未提供供应商')}</span><span>最近 ${escapeHTML(formatRunTime(item.lastSeenAt))}</span></div></div>`).join('') : '<div class="empty-state"><p>没有未匹配模型，所有模型都已命中规则。</p></div>';
}
function renderOverview() {
  const enabled = sites.filter((site) => site.enabled);
  const attention = enabled.filter((site) => ACQ_ATTENTION.has(site.acquisitionState) || (site.sessionRequired && !site.sessionConfigured));
  const pendingSessions = enabled.filter((site) => site.sessionRequired && !site.sessionConfigured);
  $('#overview-content').innerHTML = `
    <button class="stat-card tone-ok" data-goto="sites" type="button"><div class="stat-value">${enabled.length}<span class="muted" style="font-size:14px"> / ${sites.length}</span></div><div class="stat-label">启用中站点</div></button>
    <button class="stat-card ${attention.length ? 'tone-danger' : 'tone-ok'}" data-goto="sites" data-filter="attention" type="button"><div class="stat-value">${attention.length}</div><div class="stat-label">异常 / 待处理站点</div></button>
    <button class="stat-card ${pendingSessions.length ? 'tone-warn' : ''}" data-goto="sites" data-filter="attention" type="button"><div class="stat-value">${pendingSessions.length}</div><div class="stat-label">待同步登录态</div></button>
    <button class="stat-card" data-goto="unmatched" type="button"><div class="stat-value">${unmatchedCount}</div><div class="stat-label">未匹配模型</div></button>`;
  document.querySelectorAll('[data-goto]').forEach((card) => card.onclick = () => {
    location.hash = `#${card.dataset.goto}`;
    if (card.dataset.filter) $('#site-state-filter').value = card.dataset.filter;
    renderSites();
  });
  const attentionList = $('#attention-list');
  if (attention.length) {
    $('#attention-card').hidden = false;
    attentionList.innerHTML = attention.map((site) => {
      const [stateLabel] = ACQ_STATES[site.acquisitionState] || [site.acquisitionState];
      const reason = site.customFailureReason || (site.sessionRequired && !site.sessionConfigured ? '缺少登录态，等待浏览器同步' : '');
      return `<button class="attention-item" data-attention-site="${site.name}" type="button"><span class="chip ${ACQ_ATTENTION.has(site.acquisitionState) ? 'chip-danger' : 'chip-warn'}">${escapeHTML(stateLabel || '待处理')}</span><strong>${escapeHTML(site.name)}</strong><span class="muted">${escapeHTML(reason || '需要检查')}</span></button>`;
    }).join('');
    document.querySelectorAll('[data-attention-site]').forEach((item) => item.onclick = () => {
      location.hash = '#sites';
      $('#site-search').value = item.dataset.attentionSite;
      renderSites();
    });
  } else {
    $('#attention-card').hidden = true;
    attentionList.innerHTML = '';
  }
}
function renderSystem() {
  $('#system-content').innerHTML = `<div class="stat-card"><div class="stat-value mono">${escapeHTML(meta.version || 'dev')}</div><div class="stat-label">版本</div></div><div class="stat-card"><div class="stat-value mono" style="font-size:16px">${escapeHTML(meta.commit || 'none')}</div><div class="stat-label">提交</div></div><div class="stat-card"><div class="stat-value" style="font-size:16px">${escapeHTML(meta.buildDate || 'unknown')}</div><div class="stat-label">构建时间</div></div><div class="stat-card"><div class="stat-value" style="font-size:16px">${escapeHTML(meta.serverTime || '')}</div><div class="stat-label">服务器时间</div></div>`;
}
async function loadConflicts() { const response = await fetch('/api/v1/admin/conflicts'); if (!response.ok) return; const conflicts = (await response.json()).conflicts || []; $('#conflict-list').innerHTML = conflicts.length ? conflicts.map((item) => `<div class="row-card"><div class="row-main"><div class="row-title"><strong class="mono">${escapeHTML(item.rawModelName)}</strong></div><div class="row-sub">${escapeHTML(item.siteName)}</div></div><div class="row-side">${(item.candidateRules || []).map(escapeHTML).join(' / ')}</div></div>`).join('') : '<div class="empty-state"><p>当前没有多规则命中冲突。</p></div>'; }
async function loadFeedback() { const response = await fetch('/api/v1/admin/feedback'); if (!response.ok) return; const items = (await response.json()).feedback || []; $('#feedback-list').innerHTML = items.length ? items.map((item) => `<div class="row-card"><div class="row-main"><div class="row-title"><strong>${escapeHTML(item.user.name || item.user.username)}</strong><span class="muted">@${escapeHTML(item.user.username)}</span></div><div class="row-sub">${escapeHTML(item.content)}</div></div><div class="row-side">${escapeHTML(formatRunTime(item.createdAt))}</div></div>`).join('') : '<div class="empty-state"><p>暂无用户反馈。</p></div>'; }

/* ---------- 站点编辑 ---------- */

function openSite(id = 0) {
  const site = sites.find((item) => item.id === id);
  let config = {};
  try { config = JSON.parse(site?.adapterConfig || '{}') || {}; } catch { config = {}; }
  $('#site-form-title').textContent = site ? '编辑站点' : '新增站点';
  $('#site-id').value = site?.id || '';
  $('#site-name').value = site?.name || '';
  $('#site-base-url').value = site?.baseUrl || '';
  $('#site-source-url').value = site?.sourceUrl || '';
  $('#site-adapter').value = site?.adapterKey || adapters[0]?.key || '';
  $('#site-interval').value = Math.round((site?.intervalSeconds || 900) / 60);
  $('#site-jitter').value = site?.jitterSeconds || 120;
  $('#site-config').value = site?.adapterConfig || '{}';
  $('#site-blocked').value = (config.blockedKeywords || []).join(', ');
  renderConfigFields($('#site-config').value);
  $('#site-failure-reason').value = site?.customFailureReason || '';
  $('#site-enabled').checked = site?.enabled ?? true;
  $('#site-session-required').checked = site?.sessionRequired ?? false;
  $('#site-dialog').showModal();
  requestAnimationFrame(() => $('#site-name').focus());
}
$('#site-adapter').addEventListener('change', () => renderConfigFields($('#site-config').value));
$('#site-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  let config;
  try { config = JSON.parse(configFromFields()); } catch { toast('适配器配置不是有效 JSON', 'error'); return; }
  const blocked = words($('#site-blocked').value);
  if (blocked.length) config.blockedKeywords = blocked;
  else delete config.blockedKeywords;
  const id = Number($('#site-id').value);
  const payload = { name: $('#site-name').value.trim(), baseUrl: $('#site-base-url').value.trim(), sourceUrl: $('#site-source-url').value.trim(), adapterKey: $('#site-adapter').value, adapterConfig: JSON.stringify(config), customFailureReason: $('#site-failure-reason').value.trim(), enabled: $('#site-enabled').checked, sessionRequired: $('#site-session-required').checked, intervalSeconds: Number($('#site-interval').value) * 60, jitterSeconds: Number($('#site-jitter').value) };
  const response = await fetch(id ? `/api/v1/admin/sites/${id}` : '/api/v1/admin/sites', { method: id ? 'PATCH' : 'POST', headers: jsonHeaders(), body: JSON.stringify(payload) });
  if (response.ok) {
    $('#site-dialog').close();
    await loadSites();
    renderOverview();
    toast(id ? `站点“${payload.name}”已更新` : `已新增站点“${payload.name}”`, 'success');
  } else toast(await errorMessage(response, '站点保存失败'), 'error');
});
async function toggleSite(id) {
  const site = sites.find((item) => item.id === id); if (!site) return;
  const response = await fetch(`/api/v1/admin/sites/${id}`, { method: 'PATCH', headers: jsonHeaders(), body: JSON.stringify({ name: site.name, adapterKey: site.adapterKey, adapterConfig: site.adapterConfig, enabled: !site.enabled, sessionRequired: site.sessionRequired, intervalSeconds: site.intervalSeconds, jitterSeconds: site.jitterSeconds }) });
  if (response.ok) { await loadSites(); toast(site.enabled ? `站点“${site.name}”已停用` : `站点“${site.name}”已启用`, 'success'); }
  else toast(await errorMessage(response, '操作失败'), 'error');
}
async function deleteSite(id) {
  const site = sites.find((item) => item.id === id); if (!site) return;
  if (!window.confirm(`删除站点“${site.name}”？历史数据会保留，可通过 API 恢复。`)) return;
  const response = await fetch(`/api/v1/admin/sites/${id}`, { method: 'DELETE', headers: { 'X-CSRF-Token': csrf() } });
  if (response.ok) { toast('站点已删除', 'success'); await Promise.all([loadSites(), loadRuns(), loadUnmatched()]); renderOverview(); }
  else toast(await errorMessage(response, '站点删除失败'), 'error');
}
async function collectSite(id) {
  const site = sites.find((item) => item.id === id);
  toast(`正在采集“${site?.name || id}”…`);
  const response = await fetch(`/api/v1/admin/sites/${id}/collect`, { method: 'POST', headers: { 'X-CSRF-Token': csrf() } });
  if (response.ok) toast('采集完成', 'success');
  else toast(await errorMessage(response, '采集失败，已保留上次成功数据'), 'error');
  await Promise.all([loadSites(), loadRuns()]);
}
function openSessionDialog(id) { sessionDialogSiteId = id; $('#session-payload').value = ''; $('#session-form-title').textContent = `导入登录态 — ${sites.find((item) => item.id === id)?.name || id}`; $('#session-dialog').showModal(); }
$('#session-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const raw = $('#session-payload').value.trim();
  if (!raw) { $('#session-dialog').close(); return; }
  try { JSON.parse(raw); } catch { toast('会话 JSON 格式错误', 'error'); return; }
  const response = await fetch(`/api/v1/admin/sites/${sessionDialogSiteId}/session`, { method: 'POST', headers: jsonHeaders(), body: raw });
  if (response.ok) { $('#session-dialog').close(); toast('会话已加密保存', 'success'); await loadSites(); }
  else toast(await errorMessage(response, '会话保存失败'), 'error');
});
$('#session-sync').onclick = async () => {
  const response = await fetch('/api/v1/admin/session-sync/pair', { method: 'POST', headers: jsonHeaders() });
  if (!response.ok) { toast('无法创建浏览器同步配对码', 'error'); return; }
  const pairing = await response.json();
  window.prompt('将此配对码输入浏览器同步扩展：', pairing.code);
};

/* ---------- 规则编辑 ---------- */

function rulePayload() { return { provider: $('#rule-provider').value.trim(), canonicalName: $('#rule-name').value.trim(), requiredTerms: words($('#rule-required').value), anyTerms: words($('#rule-any').value), excludedTerms: words($('#rule-excluded').value), aliases: words($('#rule-aliases').value), pattern: $('#rule-pattern').value.trim(), priority: Number($('#rule-priority').value), enabled: $('#rule-enabled').checked, generated: rules.find((item) => item.id === Number($('#rule-id').value))?.generated || false }; }
function openRule(id = 0) { const rule = rules.find((item) => item.id === id); $('#rule-id').value = rule?.id || ''; $('#rule-provider').value = rule?.provider || ''; $('#rule-name').value = rule?.canonicalName || ''; $('#rule-required').value = (rule?.requiredTerms || []).join(', '); $('#rule-any').value = (rule?.anyTerms || []).join(', '); $('#rule-excluded').value = (rule?.excludedTerms || []).join(', '); $('#rule-aliases').value = (rule?.aliases || []).join(', '); $('#rule-pattern').value = rule?.pattern || ''; $('#rule-priority').value = rule?.priority ?? 100; $('#rule-enabled').checked = rule?.enabled ?? true; $('#rule-preview').textContent = ''; $('#rule-dialog').showModal(); }
$('#rule-form').addEventListener('submit', async (event) => { event.preventDefault(); const id = Number($('#rule-id').value); const response = await fetch(id ? `/api/v1/admin/rules/${id}` : '/api/v1/admin/rules', { method: id ? 'PUT' : 'POST', headers: jsonHeaders(), body: JSON.stringify(rulePayload()) }); if (response.ok) { $('#rule-dialog').close(); toast('规则已保存', 'success'); await Promise.all([loadRules(), loadConflicts()]); } else toast(await errorMessage(response, '规则保存失败，请检查正则表达式和模型名'), 'error'); });
$('#preview-rule').onclick = async () => { const response = await fetch('/api/v1/admin/rules/preview', { method: 'POST', headers: jsonHeaders(), body: JSON.stringify(rulePayload()) }); if (!response.ok) { $('#rule-preview').textContent = '预览失败。'; return; } const matches = (await response.json()).matches || []; $('#rule-preview').innerHTML = matches.length ? matches.map((item) => `<div>${escapeHTML(item.siteName)} · <span class="mono">${escapeHTML(item.rawModelName)}</span></div>`).join('') : '当前已发现模型中没有命中项。'; };
async function deleteRule(id) { if (!window.confirm('删除这条自定义规则？')) return; const response = await fetch(`/api/v1/admin/rules/${id}`, { method: 'DELETE', headers: { 'X-CSRF-Token': csrf() } }); if (response.ok) { toast('规则已删除', 'success'); await Promise.all([loadRules(), loadConflicts()]); } }

/* ---------- 全局事件 ---------- */

$('#login-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const response = await fetch('/api/v1/admin/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password: $('#password').value }) });
  if (!response.ok) { $('#login-message').textContent = '登录失败，请检查密码。'; return; }
  showDashboard();
});
function showDashboard() { $('#login-panel').hidden = true; $('#dashboard').hidden = false; switchTab(location.hash.slice(1) || 'overview'); applyTheme(); loadAll(); }
$('#theme-toggle').onclick = () => {
  const modes = ['auto', 'light', 'dark'];
  themePreference = modes[(modes.indexOf(themePreference) + 1) % modes.length];
  try { localStorage.setItem('relayscope-theme', themePreference); } catch {}
  applyTheme();
};
$('#refresh').onclick = loadAll;
$('#site-search').addEventListener('input', renderSites);
$('#site-state-filter').addEventListener('change', renderSites);
$('#rule-search').addEventListener('input', renderRules);
$('#run-filter-apply').onclick = loadRuns;
$('#run-status-filter').addEventListener('change', loadRuns);
$('#run-site-filter').addEventListener('change', loadRuns);
$('#logout').onclick = async () => { const response = await fetch('/api/v1/admin/logout', { method: 'POST', headers: { 'X-CSRF-Token': csrf() } }); if (response.ok) window.location.reload(); };
document.querySelectorAll('[data-close]').forEach((button) => button.onclick = () => $(`#${button.dataset.close}`).close());
$('#add-site').onclick = () => openSite();
$('#add-rule').onclick = () => openRule();

/* 未匹配面板保留手动刷新入口；初始化 */

(async function init() {
  applyTheme();
  try {
    const probe = await fetch('/api/v1/admin/sites');
    if (probe.ok) { showDashboard(); return; }
  } catch {}
  switchTab('overview');
})();
