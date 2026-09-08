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
let unmatchedModels = [];
let feedbackItems = [];
let sitesLoaded = false;
let unmatchedLoaded = false;
let refreshing = false;
let runRequestId = 0;
let rulePreviewRequestId = 0;
let pairingPending = false;
const pendingCollections = new Set();
const loadedSections = new Set();
const failedSections = new Set();
const collectionViews = {
  sites: ['#site-list', '站点'], rules: ['#rule-list', '模型规则'],
  runs: ['#run-list', '采集记录'], unmatched: ['#unmatched-list', '未匹配模型'],
  feedback: ['#feedback-list', '用户反馈'], conflicts: ['#conflict-list', '匹配冲突'],
  meta: ['#system-content', '系统信息']
};

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
const TAB_TITLES = { overview: '运行概览', sites: '站点管理', rules: '模型规则', runs: '采集记录', unmatched: '未匹配模型', feedback: '用户反馈', system: '系统信息' };
const TAB_DESCRIPTIONS = { overview: '查看采集概况，优先处理需要关注的站点。', sites: '接入数据来源，管理采集计划与登录状态。', rules: '把不同上游命名归入标准模型，预览命中并处理冲突。', runs: '按站点查看执行结果与失败原因，最新异常优先展示。', unmatched: '检查尚未归类的模型，从这里直接建立匹配规则。', feedback: '完整查看用户报告的问题与建议。', system: '查看当前服务的版本、构建信息与服务器时间。' };
const THEME_ICONS = { auto: 'i-monitor', light: 'i-sun', dark: 'i-moon' };
const THEME_LABELS = { auto: '主题：跟随系统', light: '主题：浅色', dark: '主题：深色' };
const needsSession = (site) => site.enabled && site.sessionRequired && !site.sessionConfigured;
const needsAttention = (site) => site.enabled && (ACQ_ATTENTION.has(site.acquisitionState) || needsSession(site));
const icon = (name) => `<svg class="icon" aria-hidden="true"><use href="#i-${name}"/></svg>`;

function attentionReason(site) {
  if (site.customFailureReason) return site.customFailureReason;
  if (needsSession(site)) return '尚未导入登录态，请先完成浏览器同步。';
  return { login_expired: '登录已失效，请重新同步浏览器登录态。', challenge_pending: '站点需要验证，请检查登录与验证状态。', challenge_failed: '站点验证未通过，请查看采集记录。', collection_failed: '采集未完成，查看记录以定位具体原因。' }[site.acquisitionState] || '等待下一次采集。';
}

function filterSites(items, query = '', state = '') {
  const search = query.trim().toLowerCase();
  return items.filter((site) => {
    if (search && !`${site.name} ${site.sourceUrl} ${site.baseUrl}`.toLowerCase().includes(search)) return false;
    if (state === 'enabled') return site.enabled;
    if (state === 'disabled') return !site.enabled;
    if (state === 'attention') return needsAttention(site);
    if (state === 'sessions') return needsSession(site);
    if (state === 'healthy') return site.enabled && site.acquisitionState === 'fresh' && !needsSession(site);
    if (state === 'stale') return site.enabled && site.acquisitionState === 'stale';
    return true;
  });
}

async function readJSON(path) {
  const section = path.split('?')[0].split('/').pop();
  try {
    const response = await fetch(path, { cache: 'no-store', signal: AbortSignal.timeout(20000) });
    if (!response.ok) throw new Error(response.status === 401 ? '登录已失效，请重新登录' : '读取失败');
    const payload = await response.json();
    loadedSections.add(section);
    failedSections.delete(section);
    return payload;
  } catch (error) {
    failedSections.add(section);
    throw error;
  }
}

function renderCollectionState(section) {
  if (loadedSections.has(section)) return false;
  const [selector, label] = collectionViews[section];
  const failed = failedSections.has(section);
  $(selector).innerHTML = `<div class="empty-state"><h2>${label}${failed ? '暂时无法读取' : '正在读取'}</h2><p>${failed ? '请检查连接后重试，暂时无法确认是否有数据。' : '稍候即可查看最新内容。'}</p>${failed ? '<button type="button" class="btn" data-refresh-all>重新读取</button>' : ''}</div>`;
  return true;
}

function formError(form, message = '') {
  const region = form.querySelector('[data-form-error]');
  region.textContent = message;
  region.hidden = !message;
  if (message) region.focus();
}

async function submitForm(form, label, action) {
  const button = form.querySelector('[type="submit"]');
  if (button.disabled) return;
  const original = button.textContent;
  button.disabled = true;
  button.textContent = label;
  form.setAttribute('aria-busy', 'true');
  form.querySelectorAll('[data-close]').forEach((control) => { control.disabled = true; });
  formError(form);
  try { await action(); }
  catch (error) { formError(form, error.message || '操作失败，填写内容已保留，请重试。'); }
  finally {
    button.disabled = false;
    button.textContent = original;
    form.removeAttribute('aria-busy');
    form.querySelectorAll('[data-close]').forEach((control) => { control.disabled = false; });
  }
}

async function saveRequest(path, method, payload) {
  let response;
  try { response = await fetch(path, { method, headers: jsonHeaders(), body: payload === undefined ? undefined : JSON.stringify(payload), signal: AbortSignal.timeout(30000) }); }
  catch { throw new Error('连接中断，尚未确认保存结果。请刷新列表确认后再重试，填写内容已保留。'); }
  if (!response.ok) throw new Error(await errorMessage(response, '操作失败，请重试。'));
  return response;
}

async function runAction(button, action) {
  if (button?.disabled) return;
  if (button) { button.disabled = true; button.setAttribute('aria-busy', 'true'); }
  try { await action(); }
  catch (error) { toast(error.message || '连接失败，请重试。', 'error'); }
  finally { if (button) { button.disabled = false; button.removeAttribute('aria-busy'); } }
}

function reportLoadError(error) {
  $('#dashboard-message').hidden = false;
  $('#dashboard-message').textContent = `${error.message || '部分数据读取失败'}，已保留上次读取的内容。请使用右上角刷新重试。`;
}

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
const CONFIG_LABELS = {
  statusPath: '状态接口路径', period: '统计周期', board: '模型榜单类型', modelProbePath: '模型探针接口路径',
  pricingAdapter: '价格解析方式', pricingPath: '价格接口路径', pricingStatusPath: '计费信息接口路径',
  pulsePath: '模型动态接口路径', summaryPath: '汇总接口路径', detailPath: '详情接口路径',
  windowHours: '统计时长（小时）', catalogPageSize: '目录每页数量', skipDetails: '跳过分时详情采集',
  availabilityMode: '可用状态判断方式', pricingRequiresSession: '采集价格需要登录', statusBaseUrl: '状态服务地址',
  catalogPath: '模型目录路径', detailPathTemplate: '详情地址模板', groupsPath: '分组接口路径',
  batchPath: '批量查询接口路径', pageSize: '每页采集数量', pricingBaseUrl: '价格服务地址',
  pricingOptional: '价格缺失时继续采集', monitorPath: '监测接口路径', slug: '状态页面标识',
  heartbeatPath: '心跳接口路径', retryAttempts: '重试次数', monitorNameMode: '模型名称识别方式', heatmapPath: '请求热力图接口路径'
};
const CONFIG_OPTIONS = { metrics: '按监测数据判断', presence: '按目录收录判断', 'suffix-model': '从名称后缀识别模型' };
function configValue(value, property) {
  if (value !== undefined && value !== null) return value;
  return property.default ?? (property.type === 'boolean' ? false : '');
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
    const label = escapeHTML(property.title || CONFIG_LABELS[key] || key);
    const attributes = `data-config-key="${escapeHTML(key)}" data-config-type="${escapeHTML(property.type)}" data-config-default="${property.default !== undefined}"`;
    if (property.enum?.length) return `<label class="field">${label}<select ${attributes} data-config-enum="true"><option value="">使用来源默认设置</option>${property.enum.map((item) => `<option value="${escapeHTML(item)}" ${String(item) === String(value) ? 'selected' : ''}>${escapeHTML(CONFIG_OPTIONS[item] || item)}</option>`).join('')}</select></label>`;
    if (property.type === 'boolean') return `<div class="field field-inline span-2"><label class="check"><input type="checkbox" ${attributes} ${value ? 'checked' : ''}> ${label}</label></div>`;
    const bounds = `${property.minimum !== undefined ? ` min="${property.minimum}"` : ''}${property.maximum !== undefined ? ` max="${property.maximum}"` : ''}`;
    return `<label class="field">${label}<input ${attributes} type="${property.type === 'string' ? 'text' : 'number'}" value="${escaped}" placeholder="使用来源默认设置"${bounds}${property.type === 'number' ? ' step="any"' : ''}></label>`;
  }).join('') : '<p class="schema-hint">此来源类型无需额外配置；有特殊设置时可展开原始配置。</p>';
}
function configFromFields() {
  const values = JSON.parse($('#site-config').value);
  if (!values || Array.isArray(values) || typeof values !== 'object') throw new Error('原始配置必须是有效的 JSON（配置数据）对象。');
  document.querySelectorAll('[data-config-key]').forEach((field) => {
    const key = field.dataset.configKey;
    if (field.type === 'checkbox') {
      if (field.checked || Object.hasOwn(values, key) || field.dataset.configDefault === 'true') values[key] = field.checked;
      return;
    }
    if (field.value === '' && (field.type === 'number' || field.dataset.configEnum || !Object.hasOwn(values, key))) { delete values[key]; return; }
    if (field.dataset.configType === 'boolean') values[key] = field.value === 'true';
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
  $('#page-description').textContent = TAB_DESCRIPTIONS[tab];
  document.title = `${TAB_TITLES[tab]} · RelayScope`;
  document.body.classList.remove('nav-open');
  $('#mobile-nav-toggle').setAttribute('aria-expanded', 'false');
}
window.addEventListener('hashchange', () => { switchTab(location.hash.slice(1)); $('#page-title').focus({ preventScroll: true }); });
document.querySelector('.skip-link').addEventListener('click', (event) => {
  event.preventDefault();
  $('#admin-content').focus();
  $('#admin-content').scrollIntoView({ block: 'start' });
});
document.querySelectorAll('[data-tab]').forEach((item) => item.addEventListener('click', () => {
  if (location.hash === item.getAttribute('href')) {
    switchTab(item.dataset.tab);
    $('#page-title').focus({ preventScroll: true });
  }
}));

const systemTheme = matchMedia('(prefers-color-scheme: dark)');
let themePreference = 'auto';
try { const saved = localStorage.getItem('relayscope-theme'); if (['auto', 'light', 'dark'].includes(saved)) themePreference = saved; } catch {}
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
  if (refreshing) return;
  refreshing = true;
  $('#refresh').disabled = true;
  $('#refresh').setAttribute('aria-busy', 'true');
  $('#last-refresh').textContent = '正在更新…';
  Object.keys(collectionViews).forEach(renderCollectionState);
  const sections = [['系统信息', loadMeta], ['来源类型', loadAdapters], ['站点', loadSites], ['模型规则', loadRules], ['采集记录', loadRuns], ['匹配冲突', loadConflicts], ['反馈', loadFeedback], ['未匹配模型', loadUnmatched]];
  const results = await Promise.allSettled(sections.map(([, load]) => load()));
  const failed = results.flatMap((result, index) => result.status === 'rejected' ? [sections[index][0]] : []);
  $('#dashboard-message').hidden = !failed.length;
  $('#dashboard-message').textContent = failed.length ? `${failed.join('、')}读取失败，已保留可用内容。请刷新重试；登录过期时请重新登录。` : '';
  Object.keys(collectionViews).forEach(renderCollectionState);
  renderOverview();
  const stamp = new Date().toLocaleTimeString('zh-CN', { hour12: false });
  $('#last-refresh').textContent = `${failed.length ? '部分更新' : '更新于'} ${stamp}`;
  refreshing = false;
  $('#refresh').disabled = false;
  $('#refresh').removeAttribute('aria-busy');
}
async function loadMeta() { meta = await readJSON('/api/v1/meta'); renderSystem(); }
async function loadAdapters() {
  adapters = (await readJSON('/api/v1/admin/adapters')).adapters || [];
  if ($('#site-dialog').open) return;
  $('#site-adapter').innerHTML = adapters.map((item) => `<option value="${escapeHTML(item.key)}">${escapeHTML(item.displayName)}</option>`).join('');
  renderConfigFields($('#site-config').value);
  if (sitesLoaded) renderSites();
}
async function loadSites() {
  sites = (await readJSON('/api/v1/admin/sites')).sites || [];
  sitesLoaded = true;
  const siteSelect = $('#run-site-filter');
  const current = siteSelect.value;
  siteSelect.innerHTML = '<option value="">全部站点</option>' + sites.map((site) => `<option value="${site.id}">${escapeHTML(site.name)}（#${site.id}）</option>`).join('');
  siteSelect.value = current;
  renderSites();
  renderOverview();
}
function renderSites() {
  if (renderCollectionState('sites')) return;
  const visible = filterSites(sites, $('#site-search').value, $('#site-state-filter').value);
  $('#site-summary').textContent = `${visible.length} / ${sites.length} 个站点`;
  $('#reset-site-filters').hidden = !($('#site-search').value || $('#site-state-filter').value);
  $('#site-list').innerHTML = visible.length ? visible.map((site) => {
    const [stateLabel, stateClass] = ACQ_STATES[site.acquisitionState] || [site.acquisitionState, 'chip-muted'];
    const blocked = blockedKeywordsOf(site);
    const nextRun = site.nextRunAt ? new Date(site.nextRunAt).toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' }) : '—';
    const attention = needsAttention(site);
    const collecting = pendingCollections.has(site.id) || site.acquisitionState === 'collecting';
    return `<article class="site-card${site.enabled ? '' : ' site-disabled'}" data-site-card="${site.id}">
      <div class="site-main">
        <div class="site-title-line"><strong>${escapeHTML(site.name)}</strong>${blocked.length ? `<span class="chip chip-blocked" title="屏蔽关键词：${escapeHTML(blocked.join('，'))}">${icon('ban')}<span>${blocked.length} 项屏蔽</span></span>` : ''}</div>
        <span class="site-url mono">${escapeHTML(site.sourceUrl || site.baseUrl)}</span>
        <div class="site-facts"><span>${escapeHTML(adapterFor(site.adapterKey)?.displayName || site.adapterKey)}</span></div>
      </div>
      <div class="site-schedule"><span class="site-fact-label">采集计划</span><strong>每 ${Math.round(site.intervalSeconds / 60)} 分钟</strong><span>下次 ${site.enabled ? nextRun : '已暂停'}</span></div>
      <div class="site-health"><span class="chip ${site.enabled ? stateClass : 'chip-muted'}"><span class="dot"></span>${site.enabled ? escapeHTML(stateLabel || '未知') : '已停用'}</span><span class="session-state${needsSession(site) ? ' needs-session' : ''}">${site.sessionRequired ? (site.sessionConfigured ? '已配置登录态' : '待同步登录态') : '公开数据源'}</span></div>
      <div class="site-actions">
        <button class="btn btn-ghost" data-edit-site="${site.id}" type="button" aria-label="编辑 ${escapeHTML(site.name)}">编辑</button>
        <button class="btn" data-collect="${site.id}" type="button"${collecting ? ' disabled' : ''} aria-label="采集 ${escapeHTML(site.name)}">${icon('play')}${collecting ? '采集中' : '采集'}</button>
        <details class="action-menu"><summary class="btn btn-icon" aria-label="${escapeHTML(site.name)} 的更多操作">${icon('more')}</summary><div class="menu-content"><button data-site-runs="${site.id}" type="button">${icon('activity')}查看采集记录</button><button data-session="${site.id}" type="button">${icon('key')}导入登录态</button><button data-toggle="${site.id}" type="button">${icon(site.enabled ? 'ban' : 'play')}${site.enabled ? '停用站点' : '启用站点'}</button><button class="danger-text" data-delete-site="${site.id}" type="button">${icon('trash')}删除站点</button></div></details>
      </div>
      ${attention ? `<div class="site-issue">${icon('activity')}<span>${escapeHTML(attentionReason(site))}</span><button type="button" class="text-button" ${needsSession(site) || site.acquisitionState === 'login_expired' ? `data-session="${site.id}"` : `data-site-runs="${site.id}"`}>${needsSession(site) || site.acquisitionState === 'login_expired' ? '导入登录态' : '查看记录'} →</button></div>` : ''}
    </article>`;
  }).join('') : `<div class="empty-state">${icon('globe')}<h2>${sites.length ? '没有符合条件的站点' : '接入第一个监测站点'}</h2><p>${sites.length ? '修改关键词或清空筛选后重试。' : '填写来源页面后，系统会按计划自动采集模型状态。'}</p><button type="button" class="btn btn-primary" ${sites.length ? 'data-reset-sites' : 'data-action="add-site"'}>${sites.length ? '清空筛选' : '新增站点'}</button></div>`;
  document.querySelectorAll('[data-edit-site]').forEach((button) => button.onclick = () => openSite(Number(button.dataset.editSite)));
  document.querySelectorAll('[data-toggle]').forEach((button) => button.onclick = () => runAction(button, () => toggleSite(Number(button.dataset.toggle))));
  document.querySelectorAll('[data-collect]').forEach((button) => button.onclick = () => runAction(button, () => collectSite(Number(button.dataset.collect))));
  document.querySelectorAll('[data-session]').forEach((button) => button.onclick = () => openSessionDialog(Number(button.dataset.session)));
  document.querySelectorAll('[data-delete-site]').forEach((button) => button.onclick = () => runAction(button, () => deleteSite(Number(button.dataset.deleteSite))));
}
async function loadRules() { rules = (await readJSON('/api/v1/admin/rules')).rules || []; $('#nav-rules-count').textContent = rules.length; $('#nav-rules-count').hidden = false; renderRules(); }
function renderRules() {
  if (renderCollectionState('rules')) return;
  const search = $('#rule-search').value.trim().toLowerCase();
  const visible = rules.filter((rule) => !search || `${rule.canonicalName} ${rule.provider}`.toLowerCase().includes(search));
  $('#rule-summary').textContent = `${visible.length} / ${rules.length} 条`;
  $('#rule-list').innerHTML = visible.length ? visible.map((rule) => `<div class="row-card"><div class="row-main"><div class="row-title"><strong class="mono">${escapeHTML(rule.canonicalName)}</strong>${rule.enabled ? '' : '<span class="chip chip-muted">已停用</span>'}${rule.generated ? '<span class="chip chip-muted">内置</span>' : ''}</div><div class="row-sub">${escapeHTML(rule.provider)}</div></div><div class="row-side"><span>优先级 ${escapeHTML(rule.priority)}</span><button class="btn" data-edit-rule="${rule.id}" type="button">编辑</button>${rule.generated ? '' : `<button class="btn btn-danger" data-delete-rule="${rule.id}" type="button">删除</button>`}</div></div>`).join('') : '<div class="empty-state"><p>没有符合条件的规则。</p></div>';
  document.querySelectorAll('[data-edit-rule]').forEach((button) => button.onclick = () => openRule(Number(button.dataset.editRule)));
  document.querySelectorAll('[data-delete-rule]').forEach((button) => button.onclick = () => runAction(button, () => deleteRule(Number(button.dataset.deleteRule))));
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
  const requestId = ++runRequestId;
  const query = new URLSearchParams(); if ($('#run-status-filter').value) query.set('status', $('#run-status-filter').value); if ($('#run-site-filter').value) query.set('site', $('#run-site-filter').value); query.set('limit', '100');
  $('#run-list').setAttribute('aria-busy', 'true');
  let runs;
  try { runs = (await readJSON(`/api/v1/admin/runs?${query}`)).runs || []; }
  catch (error) { if (requestId === runRequestId) { $('#run-list').setAttribute('aria-busy', 'false'); throw error; } return; }
  if (requestId !== runRequestId) return;
  const disclosureStates = new Map([...$('#run-list').querySelectorAll('[data-run-site]')].map((element) => [element.dataset.runSite, element.open]));
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
    const open = disclosureStates.get(String(latest.siteId)) ?? latest.status !== 'success';
    return `<details class="run-site" data-run-site="${latest.siteId}" ${open ? 'open' : ''}>
      <summary>
        <span class="run-disclosure" aria-hidden="true">›</span>
        <span class="run-site-title"><strong>${escapeHTML(latest.siteName)}</strong><span class="muted">最近 ${items.length} 次</span></span>
        <span class="run-site-counts"><b class="healthy">成功 ${successCount}</b>${partialCount ? `<b class="degraded">部分 ${partialCount}</b>` : ''}<b class="${failureCount ? 'failed' : 'muted'}">失败 ${failureCount}</b>${consecutiveFailures ? `<b class="failed">连续 ${consecutiveFailures} 次</b>` : ''}</span>
        <span class="run-site-latest"><span class="chip ${latestClass}">${escapeHTML(latestLabel)}</span><time>${escapeHTML(formatRunTime(latest.startedAt))}</time></span>
      </summary>
      <div class="run-history">${items.map(renderRun).join('')}</div>
    </details>`;
  }).join('') : '<div class="empty-state"><p>当前条件下没有采集记录，试试切换站点或状态。</p></div>';
  $('#run-list').setAttribute('aria-busy', 'false');
}
async function loadUnmatched() {
  unmatchedModels = (await readJSON('/api/v1/admin/unmatched?limit=200')).models || [];
  unmatchedCount = unmatchedModels.length;
  unmatchedLoaded = true;
  $('#nav-unmatched-count').textContent = unmatchedCount === 200 ? '200+' : unmatchedCount || '';
  renderUnmatched();
}
function renderUnmatched() {
  if (renderCollectionState('unmatched')) return;
  const query = $('#unmatched-search').value.trim().toLowerCase();
  const models = unmatchedModels.filter((item) => `${item.rawModelName} ${item.siteName} ${item.providerHint}`.toLowerCase().includes(query));
  $('#unmatched-summary').textContent = `${models.length} 条${unmatchedCount === 200 ? ' · 仅展示最近 200 条' : ''}`;
  $('#unmatched-list').innerHTML = models.length ? models.map((item) => `<div class="row-card"><div class="row-main"><div class="row-title"><strong class="mono">${escapeHTML(item.rawModelName)}</strong></div><div class="row-sub">${escapeHTML(item.siteName)} · ${escapeHTML(item.providerHint || '未提供供应商')}</div></div><div class="row-side"><span>最近 ${escapeHTML(formatRunTime(item.lastSeenAt))}</span><button class="btn" type="button" data-create-rule="${unmatchedModels.indexOf(item)}">建立规则</button></div></div>`).join('') : `<div class="empty-state">${icon('check')}<p>${query ? '没有符合搜索条件的模型。' : '当前没有待归类的模型。'}</p></div>`;
}
function renderOverview() {
  const enabled = sites.filter((site) => site.enabled);
  const attention = enabled.filter(needsAttention);
  const pendingSessions = enabled.filter(needsSession);
  const waiting = enabled.filter((site) => !needsAttention(site) && site.acquisitionState !== 'fresh');
  $('#overview-health').classList.toggle('has-attention', attention.length > 0);
  const headline = !sitesLoaded ? (failedSections.has('sites') ? '站点状态暂时无法读取' : '正在读取站点状态') : !sites.length ? '从接入一个站点开始' : attention.length ? `${attention.length} 个站点需要处理` : waiting.length ? `${waiting.length} 个站点等待采集确认` : enabled.length ? '已启用站点采集运行正常' : '当前没有启用的站点';
  $('#overview-health').innerHTML = `${icon(attention.length ? 'activity' : 'gauge')}<div><strong>${headline}</strong><p>${sites.length ? '采集异常会保留上次成功的数据；具体模型是否可用，请查看公开看板。' : '添加来源页面后，系统会自动采集模型目录、状态与价格。'}</p></div><a class="btn" href="${sites.length ? '/' : '#sites'}">${sites.length ? '查看公开看板' : '管理站点'}${icon('arrow')}</a>`;
  const stats = [
    [sitesLoaded ? enabled.length : '—', '启用中站点', `${sites.length} 个站点已登记`, 'sites', 'enabled', 'globe', ''],
    [sitesLoaded ? attention.length : '—', '异常 / 待处理', '检查采集失败与登录问题', 'sites', 'attention', 'activity', attention.length ? 'tone-danger' : ''],
    [sitesLoaded ? pendingSessions.length : '—', '待同步登录态', '连接浏览器后恢复采集', 'sites', 'sessions', 'key', pendingSessions.length ? 'tone-warn' : ''],
    [unmatchedLoaded ? `${unmatchedCount}${unmatchedCount === 200 ? '+' : ''}` : '—', '未匹配模型', '检查上游命名并建立规则', 'unmatched', '', 'shield', '']
  ];
  $('#overview-content').innerHTML = stats.map(([count, label, hint, target, filter, glyph, tone]) => `<button class="stat-card ${tone}" data-goto="${target}" data-filter="${filter}" type="button"><span class="stat-label">${label}${icon(glyph)}</span><span class="stat-value">${count}</span><span class="stat-hint">${hint}${icon('arrow')}</span></button>`).join('');
  const attentionList = $('#attention-list');
  if (attention.length) {
    $('#attention-summary').textContent = `${attention.length} 项 · 点击处理`;
    attentionList.innerHTML = attention.map((site) => {
      const [stateLabel] = ACQ_STATES[site.acquisitionState] || [site.acquisitionState];
      return `<button class="attention-item" data-attention-site="${site.id}" type="button"><span class="attention-copy"><strong>${escapeHTML(site.name)}<span class="chip ${ACQ_ATTENTION.has(site.acquisitionState) ? 'chip-danger' : 'chip-warn'}">${escapeHTML(needsSession(site) ? '待同步' : stateLabel || '待处理')}</span></strong><span>${escapeHTML(attentionReason(site))}</span></span>${icon('arrow')}</button>`;
    }).join('');
  } else {
    $('#attention-summary').textContent = sitesLoaded ? '暂无待处理事项' : '正在读取';
    attentionList.innerHTML = `<div class="empty-state">${icon(sites.length ? 'check' : 'globe')}<h2>${sitesLoaded ? (sites.length ? '当前没有采集异常' : '还没有监测站点') : '正在读取站点状态'}</h2><p>${sites.length ? '出现采集失败或登录问题时，会在这里集中显示。' : '从右侧接入站点，开始建立你的监测目录。'}</p></div>`;
  }
}
function renderSystem() {
  $('#system-content').innerHTML = [['运行版本', meta.version || '开发版本'], ['构建提交', meta.commit || '未提供'], ['构建时间', meta.buildDate || '未提供'], ['服务器时间', meta.serverTime ? formatRunTime(meta.serverTime) : '未提供']].map(([label, value]) => `<div class="system-fact"><span>${label}</span><strong>${escapeHTML(value)}</strong></div>`).join('');
}
async function loadConflicts() {
  const conflicts = (await readJSON('/api/v1/admin/conflicts')).conflicts || [];
  $('#conflict-summary').textContent = conflicts.length ? `${conflicts.length} 项待检查` : '当前无冲突';
  if (!$('#conflict-card').dataset.loaded) $('#conflict-card').open = conflicts.length > 0;
  $('#conflict-card').dataset.loaded = 'true';
  $('#conflict-list').innerHTML = conflicts.length ? conflicts.map((item) => `<div class="row-card"><div class="row-main"><div class="row-title"><strong class="mono">${escapeHTML(item.rawModelName)}</strong></div><div class="row-sub">${escapeHTML(item.siteName)}</div></div><div class="row-side conflict-candidates">${(item.candidateRules || []).map((name) => `<button type="button" class="text-button" data-find-rule="${escapeHTML(name)}">${escapeHTML(name)}</button>`).join('')}</div></div>`).join('') : '<div class="empty-state"><p>当前没有多规则命中冲突。</p></div>';
}
async function loadFeedback() { feedbackItems = (await readJSON('/api/v1/admin/feedback')).feedback || []; renderFeedback(); }
function renderFeedback() {
  if (renderCollectionState('feedback')) return;
  const query = $('#feedback-search').value.trim().toLowerCase();
  const items = feedbackItems.filter((item) => `${item.content} ${item.user?.name} ${item.user?.username}`.toLowerCase().includes(query));
  $('#feedback-summary').textContent = `${items.length} / ${feedbackItems.length} 条反馈`;
  $('#feedback-list').innerHTML = items.length ? items.map((item) => `<article class="feedback-entry"><div class="feedback-heading"><strong>${escapeHTML(item.user?.name || item.user?.username || '用户')}</strong><span class="muted">@${escapeHTML(item.user?.username || '')}</span><time>${escapeHTML(formatRunTime(item.createdAt))}</time></div><p class="feedback-content">${escapeHTML(item.content)}</p></article>`).join('') : '<div class="empty-state"><p>当前没有符合条件的反馈。</p></div>';
}

/* ---------- 站点编辑 ---------- */

function openSite(id = 0) {
  if (!loadedSections.has('adapters')) { toast('来源类型尚未读取完成，请刷新后再编辑站点。', 'error'); return; }
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
  $('#site-jitter').value = site?.jitterSeconds ?? 120;
  $('#site-config').value = site?.adapterConfig || '{}';
  $('#site-blocked').value = (config.blockedKeywords || []).join(', ');
  renderConfigFields($('#site-config').value);
  $('#site-failure-reason').value = site?.customFailureReason || '';
  $('#site-enabled').checked = site?.enabled ?? true;
  $('#site-session-required').checked = site?.sessionRequired ?? false;
  formError($('#site-form'));
  $('#site-schedule-section').open = Boolean(site && (site.intervalSeconds !== 900 || site.jitterSeconds !== 120));
  $('#site-schedule-section summary span').textContent = `每 ${$('#site-interval').value} 分钟 · 随机延后 ${$('#site-jitter').value} 秒`;
  $('#site-blocked-section').open = Boolean(config.blockedKeywords?.length);
  $('#site-announcement-section').open = Boolean(site?.customFailureReason);
  $('#site-config-section').open = false;
  $('#site-source-settings').open = false;
  $('#site-dialog').showModal();
  requestAnimationFrame(() => { $('#site-dialog .dialog-body').scrollTop = 0; $('#site-name').focus({ preventScroll: true }); });
}
$('#site-adapter').addEventListener('change', () => renderConfigFields($('#site-config').value));
$('#site-config').addEventListener('input', () => {
  try {
    const config = JSON.parse($('#site-config').value);
    if (!config || Array.isArray(config) || typeof config !== 'object') return;
    renderConfigFields($('#site-config').value);
    $('#site-blocked').value = Array.isArray(config.blockedKeywords) ? config.blockedKeywords.join(', ') : '';
  } catch { /* 保留未完成的输入，在提交时显示错误。 */ }
});
$('#site-config-fields').addEventListener('input', () => {
  try { $('#site-config').value = configFromFields(); } catch { /* 不覆盖尚未写完的原始配置。 */ }
});
$('#site-blocked').addEventListener('input', () => {
  try {
    const config = JSON.parse(configFromFields());
    const blocked = words($('#site-blocked').value);
    if (blocked.length) config.blockedKeywords = blocked;
    else delete config.blockedKeywords;
    $('#site-config').value = JSON.stringify(config);
  } catch { /* 原始配置未完成时保留输入，保存前再次校验。 */ }
});
$('#site-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  await submitForm(event.currentTarget, '正在保存…', async () => {
  let config;
  try { config = JSON.parse(configFromFields()); } catch { $('#site-config-section').open = true; throw new Error('原始配置格式不正确，请修正后再保存。'); }
  const blocked = words($('#site-blocked').value);
  if (blocked.length) config.blockedKeywords = blocked;
  else delete config.blockedKeywords;
  const id = Number($('#site-id').value);
  const payload = { name: $('#site-name').value.trim(), baseUrl: $('#site-base-url').value.trim(), sourceUrl: $('#site-source-url').value.trim(), adapterKey: $('#site-adapter').value, adapterConfig: JSON.stringify(config), customFailureReason: $('#site-failure-reason').value.trim(), enabled: $('#site-enabled').checked, sessionRequired: $('#site-session-required').checked, intervalSeconds: Number($('#site-interval').value) * 60, jitterSeconds: Number($('#site-jitter').value) };
  await saveRequest(id ? `/api/v1/admin/sites/${id}` : '/api/v1/admin/sites', id ? 'PATCH' : 'POST', payload);
  $('#site-dialog').close();
  toast(id ? `站点“${payload.name}”已更新` : `已新增站点“${payload.name}”`, 'success');
  await loadSites().catch(reportLoadError);
  });
});
async function toggleSite(id) {
  const site = sites.find((item) => item.id === id); if (!site) return;
  await saveRequest(`/api/v1/admin/sites/${id}`, 'PATCH', { name: site.name, adapterKey: site.adapterKey, adapterConfig: site.adapterConfig, enabled: !site.enabled, sessionRequired: site.sessionRequired, intervalSeconds: site.intervalSeconds, jitterSeconds: site.jitterSeconds });
  toast(site.enabled ? `站点“${site.name}”已停用` : `站点“${site.name}”已启用`, 'success');
  await loadSites().catch(reportLoadError);
}
async function deleteSite(id) {
  const site = sites.find((item) => item.id === id); if (!site) return;
  if (!window.confirm(`删除站点“${site.name}”？站点将停止采集并从看板移除，历史数据会保留。`)) return;
  await saveRequest(`/api/v1/admin/sites/${id}`, 'DELETE');
  toast('站点已删除', 'success');
  await Promise.all([loadSites(), loadRuns(), loadUnmatched()]).catch(reportLoadError);
  renderOverview();
}
async function collectSite(id) {
  if (pendingCollections.has(id)) return;
  pendingCollections.add(id);
  const site = sites.find((item) => item.id === id);
  toast(`正在采集“${site?.name || id}”…`);
  renderSites();
  try {
    const response = await fetch(`/api/v1/admin/sites/${id}/collect`, { method: 'POST', headers: { 'X-CSRF-Token': csrf() }, signal: AbortSignal.timeout(240000) });
    if (response.ok) toast('采集完成', 'success');
    else toast(await errorMessage(response, '采集失败，已保留上次成功数据'), 'error');
  } catch { toast('连接中断，采集可能仍在执行。请刷新采集记录确认。', 'error'); }
  finally { pendingCollections.delete(id); await Promise.all([loadSites(), loadRuns()]).catch(reportLoadError); renderSites(); }
}
function openSessionDialog(id) { sessionDialogSiteId = id; $('#session-payload').value = ''; formError($('#session-form')); $('#session-form-title').textContent = `导入登录态 · ${sites.find((item) => item.id === id)?.name || id}`; $('#session-dialog').showModal(); }
$('#session-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  await submitForm(event.currentTarget, '正在保存…', async () => {
  const raw = $('#session-payload').value.trim();
  if (!raw) throw new Error('请粘贴需要导入的登录态，或取消关闭。');
  let payload;
  try { payload = JSON.parse(raw); } catch { throw new Error('登录态内容格式错误，请检查后重试。'); }
  await saveRequest(`/api/v1/admin/sites/${sessionDialogSiteId}/session`, 'POST', payload);
  $('#session-dialog').close();
  $('#session-payload').value = '';
  toast('登录态已加密保存', 'success');
  await loadSites().catch(reportLoadError);
  });
});
async function createPairing() {
  if (pairingPending) return;
  pairingPending = true;
  try {
  const response = await saveRequest('/api/v1/admin/session-sync/pair', 'POST');
  const pairing = await response.json();
  $('#pair-code').value = pairing.code;
  $('#pair-expiry').textContent = pairing.expiresAt ? `有效期至 ${formatRunTime(pairing.expiresAt)}，过期后重新打开此窗口生成。` : '配对码仅用于连接当前扩展。';
  $('#pair-message').textContent = '';
  $('#pair-dialog').showModal();
  } finally { pairingPending = false; }
}
$('#session-sync').onclick = (event) => runAction(event.currentTarget, createPairing);

/* ---------- 规则编辑 ---------- */

function rulePayload() { return { provider: $('#rule-provider').value.trim(), canonicalName: $('#rule-name').value.trim(), requiredTerms: words($('#rule-required').value), anyTerms: words($('#rule-any').value), excludedTerms: words($('#rule-excluded').value), aliases: words($('#rule-aliases').value), pattern: $('#rule-pattern').value.trim(), priority: Number($('#rule-priority').value), enabled: $('#rule-enabled').checked, generated: rules.find((item) => item.id === Number($('#rule-id').value))?.generated || false }; }
function openRule(id = 0, draft = null) {
  const rule = rules.find((item) => item.id === id) || draft;
  rulePreviewRequestId++;
  $('#rule-form-title').textContent = id ? '编辑匹配规则' : '建立匹配规则';
  $('#rule-id').value = id || '';
  $('#rule-provider').value = rule?.provider || '';
  $('#rule-name').value = rule?.canonicalName || '';
  $('#rule-required').value = (rule?.requiredTerms || []).join(', ');
  $('#rule-any').value = (rule?.anyTerms || []).join(', ');
  $('#rule-excluded').value = (rule?.excludedTerms || []).join(', ');
  $('#rule-aliases').value = (rule?.aliases || []).join(', ');
  $('#rule-pattern').value = rule?.pattern || '';
  $('#rule-priority').value = rule?.priority ?? 100;
  $('#rule-enabled').checked = rule?.enabled ?? true;
  $('#rule-preview').textContent = '';
  formError($('#rule-form'));
  $('#rule-dialog').showModal();
}
$('#rule-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  await submitForm(event.currentTarget, '正在保存…', async () => {
    const id = Number($('#rule-id').value);
    await saveRequest(id ? `/api/v1/admin/rules/${id}` : '/api/v1/admin/rules', id ? 'PUT' : 'POST', rulePayload());
    $('#rule-dialog').close();
    toast('规则已保存', 'success');
    await Promise.all([loadRules(), loadConflicts(), loadUnmatched()]).catch(reportLoadError);
    renderOverview();
  });
});
$('#preview-rule').onclick = async () => {
  const requestId = ++rulePreviewRequestId;
  const button = $('#preview-rule');
  button.disabled = true;
  $('#rule-preview').innerHTML = '<p>正在读取匹配结果…</p>';
  try {
    const response = await saveRequest('/api/v1/admin/rules/preview', 'POST', rulePayload());
    const matches = (await response.json()).matches || [];
    if (requestId !== rulePreviewRequestId || !$('#rule-dialog').open) return;
    $('#rule-preview').innerHTML = matches.length ? `<p>命中 ${matches.length} 项</p>` + matches.map((item) => `<div>${escapeHTML(item.siteName)} · <span class="mono">${escapeHTML(item.rawModelName)}</span></div>`).join('') : '<p>当前已发现模型中没有命中项，请调整匹配条件。</p>';
  } catch (error) {
    if (requestId === rulePreviewRequestId) $('#rule-preview').innerHTML = `<p>${escapeHTML(error.message || '预览失败，请重试。')}</p>`;
  } finally { button.disabled = false; }
};
$('#rule-form').addEventListener('input', () => { rulePreviewRequestId++; $('#rule-preview').textContent = ''; });
async function deleteRule(id) {
  if (!window.confirm('删除这条自定义规则？')) return;
  await saveRequest(`/api/v1/admin/rules/${id}`, 'DELETE');
  toast('规则已删除', 'success');
  await Promise.all([loadRules(), loadConflicts(), loadUnmatched()]).catch(reportLoadError);
  renderOverview();
}

/* ---------- 全局事件 ---------- */

$('#login-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const button = event.currentTarget.querySelector('button');
  if (button.disabled) return;
  button.disabled = true;
  $('#login-message').textContent = '正在登录…';
  try {
  const response = await fetch('/api/v1/admin/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password: $('#password').value }), signal: AbortSignal.timeout(20000) });
  if (!response.ok) { $('#login-message').textContent = '登录失败，请检查密码。'; return; }
  $('#password').value = '';
  $('#login-message').textContent = '';
  showDashboard();
  } catch { $('#login-message').textContent = '无法连接服务器，请稍后重试。'; }
  finally { button.disabled = false; }
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
$('#unmatched-search').addEventListener('input', renderUnmatched);
$('#feedback-search').addEventListener('input', renderFeedback);
const refreshRuns = () => loadRuns().catch(reportLoadError);
$('#run-filter-apply').onclick = refreshRuns;
$('#run-status-filter').addEventListener('change', refreshRuns);
$('#run-site-filter').addEventListener('change', refreshRuns);
$('#logout').onclick = (event) => runAction(event.currentTarget, async () => { await saveRequest('/api/v1/admin/logout', 'POST'); window.location.reload(); });
document.querySelectorAll('[data-close]').forEach((button) => button.onclick = () => $(`#${button.dataset.close}`).close());
$('#add-site').onclick = () => openSite();
$('#add-rule').onclick = () => openRule();

function showSites(filter = '', name = '') {
  $('#site-search').value = name;
  $('#site-state-filter').value = filter;
  renderSites();
  location.hash = '#sites';
  switchTab('sites');
}
$('#reset-site-filters').onclick = () => { showSites(); $('#site-search').focus(); };
document.addEventListener('click', (event) => {
  const button = event.target.closest('button');
  if (button?.dataset.action === 'add-site') openSite();
  if (button?.dataset.action === 'session-sync') runAction(button, createPairing);
  if (button?.hasAttribute('data-reset-sites')) showSites();
  if (button?.hasAttribute('data-refresh-all')) loadAll();
  if (button?.dataset.goto) {
    if (button.dataset.goto === 'sites') showSites(button.dataset.filter || '');
    else { $('#unmatched-search').value = ''; renderUnmatched(); location.hash = `#${button.dataset.goto}`; }
  }
  if (button?.dataset.attentionSite) {
    const site = sites.find((item) => item.id === Number(button.dataset.attentionSite));
    if (site) showSites('', site.name);
  }
  if (button?.dataset.siteRuns) {
    $('#run-site-filter').value = button.dataset.siteRuns;
    $('#run-status-filter').value = '';
    location.hash = '#runs';
    refreshRuns();
  }
  if (button?.hasAttribute('data-create-rule')) {
    const item = unmatchedModels[Number(button.dataset.createRule)];
    if (item) openRule(0, { provider: item.providerHint, canonicalName: item.rawModelName, requiredTerms: [item.rawModelName] });
  }
  if (button?.dataset.findRule) {
    $('#rule-search').value = button.dataset.findRule;
    renderRules();
    $('#rule-search').focus();
    $('#rule-list').scrollIntoView({ block: 'nearest' });
  }
  document.querySelectorAll('.action-menu[open]').forEach((menu) => { if (!menu.contains(event.target) || button) menu.open = false; });
});
$('#mobile-nav-toggle').onclick = () => {
  const open = document.body.classList.toggle('nav-open');
  $('#mobile-nav-toggle').setAttribute('aria-expanded', String(open));
};
document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape') document.querySelectorAll('.action-menu[open]').forEach((menu) => { menu.open = false; menu.querySelector('summary').focus(); });
});
document.querySelectorAll('.admin-dialog').forEach((dialog) => {
  dialog.addEventListener('cancel', (event) => { if (dialog.querySelector('form[aria-busy="true"]')) event.preventDefault(); });
  dialog.addEventListener('click', (event) => {
    const bounds = dialog.getBoundingClientRect();
    if (event.target === dialog && !dialog.querySelector('form[aria-busy="true"]') && (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom)) dialog.close();
  });
  dialog.addEventListener('invalid', (event) => { let element = event.target.parentElement; while (element && element !== dialog) { if (element.tagName === 'DETAILS') element.open = true; element = element.parentElement; } }, true);
});
$('#copy-pair-code').onclick = async () => {
  try { await navigator.clipboard.writeText($('#pair-code').value); $('#pair-message').textContent = '配对码已复制。'; }
  catch { $('#pair-code').select(); $('#pair-message').textContent = '请复制已选中的配对码。'; }
};
$('#pair-dialog').addEventListener('close', () => { $('#pair-code').value = ''; });
$('#session-dialog').addEventListener('close', () => { $('#session-payload').value = ''; });

async function checkAdminSession() {
  applyTheme();
  $('#login-form').hidden = true;
  $('#login-retry').hidden = true;
  $('#login-message').textContent = '正在确认登录状态…';
  try {
    const probe = await fetch('/api/v1/admin/sites', { cache: 'no-store', signal: AbortSignal.timeout(20000) });
    if (probe.ok) { $('#login-message').textContent = ''; showDashboard(); return; }
    if (probe.status !== 401) throw new Error('服务暂时不可用');
    $('#login-form').hidden = false;
    $('#login-message').textContent = '';
  } catch {
    $('#login-message').textContent = '暂时无法连接管理服务，请检查连接后重试。';
    $('#login-retry').hidden = false;
  }
  switchTab('overview');
}
$('#login-retry').onclick = checkAdminSession;
checkAdminSession();
