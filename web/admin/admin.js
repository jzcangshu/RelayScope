const $ = (selector) => document.querySelector(selector);
const csrf = () => document.cookie.split('; ').find((item) => item.startsWith('relayscope_csrf='))?.split('=')[1] || '';
const jsonHeaders = () => ({ 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() });
const escapeHTML = (value) => String(value ?? '').replace(/[&<>"']/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]);
const words = (value) => String(value || '').split(/[,，\n]+/).map((item) => item.trim()).filter(Boolean);
let sites = [];
let adapters = [];
let rules = [];
let meta = {};

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
    if (property.enum?.length) return `<label>${escapeHTML(property.title || key)}<select data-config-key="${escapeHTML(key)}">${property.enum.map((item) => `<option value="${escapeHTML(item)}" ${String(item) === String(value) ? 'selected' : ''}>${escapeHTML(item)}</option>`).join('')}</select></label>`;
    if (property.type === 'boolean') return `<label class="check"><input type="checkbox" data-config-key="${escapeHTML(key)}" ${value ? 'checked' : ''}> ${escapeHTML(property.title || key)}</label>`;
    return `<label>${escapeHTML(property.title || key)}<input data-config-key="${escapeHTML(key)}" type="${property.type === 'string' ? 'text' : 'number'}" data-config-type="${escapeHTML(property.type)}" value="${escaped}"${property.minimum !== undefined ? ` min="${property.minimum}"` : ''}></label>`;
  }).join('') : '<p class="muted">此适配器没有可视化配置字段，请使用 JSON 高级配置。</p>';
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

$('#login-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const response = await fetch('/api/v1/admin/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password: $('#password').value }) });
  if (!response.ok) { $('#login-message').textContent = '登录失败，请检查密码。'; return; }
  $('#login-panel').hidden = true; $('#dashboard').hidden = false; await loadAll();
});

async function loadAll() { await Promise.all([loadMeta(), loadAdapters(), loadSites(), loadRules(), loadRuns(), loadConflicts(), loadFeedback(), loadUnmatched()]); renderOverview(); }
async function loadMeta() { const response = await fetch('/api/v1/meta'); meta = response.ok ? await response.json() : {}; renderSystem(); }
async function loadAdapters() { const response = await fetch('/api/v1/admin/adapters'); adapters = response.ok ? (await response.json()).adapters || [] : []; $('#site-adapter').innerHTML = adapters.map((item) => `<option value="${escapeHTML(item.key)}">${escapeHTML(item.displayName)}</option>`).join(''); renderConfigFields($('#site-config').value); }
async function loadSites() {
  const response = await fetch('/api/v1/admin/sites'); if (!response.ok) return;
  sites = (await response.json()).sites || []; $('#dashboard-message').textContent = `已登记 ${sites.length} 个站点`;
  $('#site-list').innerHTML = sites.map((site) => `<article class="row admin-row site-row${site.enabled ? '' : ' site-disabled'}"><div class="site-identity"><div class="site-name-line"><strong>${escapeHTML(site.name)}</strong>${site.enabled ? '' : '<span class="site-disabled-badge">已禁用</span>'}</div><span class="muted">${escapeHTML(site.sourceUrl)}</span>${site.customFailureReason ? `<span class="muted">公告原因：${escapeHTML(site.customFailureReason)}</span>` : ''}</div><div class="state ${site.acquisitionState}">${escapeHTML(site.acquisitionState)}</div><div><strong>${escapeHTML(site.adapterKey)}</strong><span class="muted">${Math.round(site.intervalSeconds / 60)} 分钟 · ${site.sessionRequired ? (site.sessionConfigured ? '需要登录 · 已有会话' : '需要登录 · 待同步') : '无需登录'}</span></div><div class="actions"><button data-rename-site="${site.id}">重命名</button><button data-edit-site="${site.id}">编辑</button><button data-toggle="${site.id}">${site.enabled ? '停用' : '启用'}</button><button data-collect="${site.id}">采集</button><button data-session="${site.id}">会话</button><button data-delete-site="${site.id}">删除</button></div></article>`).join('');
  document.querySelectorAll('[data-rename-site]').forEach((button) => button.onclick = () => openSite(Number(button.dataset.renameSite), true));
  document.querySelectorAll('[data-edit-site]').forEach((button) => button.onclick = () => openSite(Number(button.dataset.editSite)));
  document.querySelectorAll('[data-toggle]').forEach((button) => button.onclick = () => toggleSite(Number(button.dataset.toggle)));
  document.querySelectorAll('[data-collect]').forEach((button) => button.onclick = () => collectSite(Number(button.dataset.collect)));
  document.querySelectorAll('[data-session]').forEach((button) => button.onclick = () => importSession(Number(button.dataset.session)));
  document.querySelectorAll('[data-delete-site]').forEach((button) => button.onclick = () => deleteSite(Number(button.dataset.deleteSite)));
}
async function loadRules() { const response = await fetch('/api/v1/admin/rules'); if (!response.ok) return; rules = (await response.json()).rules || []; $('#rule-list').innerHTML = rules.map((rule) => `<article class="row admin-row"><div><strong>${escapeHTML(rule.canonicalName)}</strong><span class="muted">${escapeHTML(rule.provider)}</span></div><div>${rule.enabled ? '启用' : '停用'}</div><div><span class="muted">优先级 ${rule.priority}${rule.generated ? ' · 内置规则' : ''}</span></div><div class="actions"><button data-edit-rule="${rule.id}">编辑</button>${rule.generated ? '' : `<button data-delete-rule="${rule.id}">删除</button>`}</div></article>`).join(''); document.querySelectorAll('[data-edit-rule]').forEach((button) => button.onclick = () => openRule(Number(button.dataset.editRule))); document.querySelectorAll('[data-delete-rule]').forEach((button) => button.onclick = () => deleteRule(Number(button.dataset.deleteRule))); }
const runStatus = (status) => ({ success: ['成功', 'healthy'], partial: ['部分完成', 'degraded'], running: ['采集中', 'degraded'], failed: ['失败', 'failed'] }[status] || [status || '未知', 'no_samples']);
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
  return `<article class="run-entry">
    <div class="run-entry-head"><span class="state-badge ${stateClass}">${escapeHTML(label)}</span><strong>${escapeHTML(formatRunTime(run.startedAt))}</strong><span class="muted">耗时 ${escapeHTML(formatDuration(run))}</span></div>
    <div class="run-facts">
      <span><b>${run.modelsSeen}</b> 模型</span><span><b>${run.groupsSeen}</b> 分组</span>
      <span>目录 <b>${run.catalogComplete ? '完整' : '不完整'}</b></span><span>适配器 <b>${escapeHTML(run.adapterKey)}</b></span>
    </div>
    ${error ? `<div class="run-error"><strong>${escapeHTML(run.errorCode || '采集错误')}</strong>${run.errorMessage ? `<pre>${escapeHTML(run.errorMessage)}</pre>` : ''}</div>` : ''}
  </article>`;
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
        <span class="run-site-title"><strong>${escapeHTML(latest.siteName)}</strong><span class="muted">最近 ${items.length} 次 · ${escapeHTML(latest.adapterKey)}</span></span>
        <span class="run-site-counts"><b class="healthy">成功 ${successCount}</b>${partialCount ? `<b class="degraded">部分完成 ${partialCount}</b>` : ''}<b class="${failureCount ? 'failed' : 'muted'}">失败 ${failureCount}</b>${consecutiveFailures ? `<b class="failed">连续 ${consecutiveFailures} 次</b>` : ''}</span>
        <span class="run-site-latest"><span class="state-badge ${latestClass}">${escapeHTML(latestLabel)}</span><time>${escapeHTML(formatRunTime(latest.startedAt))}</time></span>
      </summary>
      <div class="run-history">${items.map(renderRun).join('')}</div>
    </details>`;
  }).join('') : '<p class="muted">暂无采集记录。</p>';
}
async function loadUnmatched() { const response = await fetch('/api/v1/admin/unmatched?limit=200'); if (!response.ok) return; const models = (await response.json()).models || []; $('#unmatched-list').innerHTML = models.length ? models.map((item) => `<article class="row admin-row"><div><strong>${escapeHTML(item.rawModelName)}</strong><span class="muted">${escapeHTML(item.siteName)}</span></div><div>${escapeHTML(item.providerHint || '未提供供应商')}</div><div class="muted">最近 ${escapeHTML(formatRunTime(item.lastSeenAt))}</div></article>`).join('') : '<p class="muted">没有未匹配模型。</p>'; }
function renderOverview() { $('#overview-summary').textContent = `站点 ${sites.length} · 规则 ${rules.length}`; $('#overview-content').innerHTML = `<div class="summary-cell"><strong>${sites.filter((site) => site.enabled).length}</strong><span>启用站点</span></div><div class="summary-cell"><strong>${sites.filter((site) => site.acquisitionState === 'fresh').length}</strong><span>最新采集</span></div><div class="summary-cell"><strong>${sites.filter((site) => site.sessionRequired && !site.sessionConfigured).length}</strong><span>待同步会话</span></div>`; }
function renderSystem() { $('#system-content').innerHTML = `<div class="summary-cell"><strong>${escapeHTML(meta.version || 'dev')}</strong><span>版本</span></div><div class="summary-cell"><strong>${escapeHTML(meta.commit || 'none')}</strong><span>提交</span></div><div class="summary-cell"><strong>${escapeHTML(meta.buildDate || 'unknown')}</strong><span>构建时间</span></div><div class="summary-cell"><strong>${escapeHTML(meta.serverTime || '')}</strong><span>服务器时间</span></div>`; }
async function loadConflicts() { const response = await fetch('/api/v1/admin/conflicts'); if (!response.ok) return; const conflicts = (await response.json()).conflicts || []; $('#conflict-list').innerHTML = conflicts.length ? conflicts.map((item) => `<article class="row conflict-row"><div><strong>${escapeHTML(item.rawModelName)}</strong><span class="muted">${escapeHTML(item.siteName)}</span></div><div class="muted">${(item.candidateRules || []).map(escapeHTML).join(' / ')}</div></article>`).join('') : '<p class="muted">当前没有多规则命中冲突。</p>'; }
async function loadFeedback() { const response = await fetch('/api/v1/admin/feedback'); if (!response.ok) return; const items = (await response.json()).feedback || []; $('#feedback-list').innerHTML = items.length ? items.map((item) => `<article class="row admin-row"><div><strong>${escapeHTML(item.user.name || item.user.username)}</strong><span class="muted">@${escapeHTML(item.user.username)} · ${escapeHTML(formatRunTime(item.createdAt))}</span></div><div>${escapeHTML(item.content)}</div></article>`).join('') : '<p class="muted">暂无用户反馈。</p>'; }

function openSite(id = 0, focusName = false) { const site = sites.find((item) => item.id === id); $('#site-form-title').textContent = site ? (focusName ? '重命名站点' : '编辑站点') : '新增站点'; $('#site-id').value = site?.id || ''; $('#site-name').value = site?.name || ''; $('#site-base-url').value = site?.baseUrl || ''; $('#site-source-url').value = site?.sourceUrl || ''; $('#site-adapter').value = site?.adapterKey || adapters[0]?.key || ''; $('#site-interval').value = Math.round((site?.intervalSeconds || 900) / 60); $('#site-jitter').value = site?.jitterSeconds || 120; $('#site-config').value = site?.adapterConfig || '{}'; renderConfigFields($('#site-config').value); $('#site-failure-reason').value = site?.customFailureReason || ''; $('#site-enabled').checked = site?.enabled ?? true; $('#site-session-required').checked = site?.sessionRequired ?? false; $('#site-base-url').disabled = false; $('#site-source-url').disabled = false; $('#site-dialog').showModal(); if (focusName) requestAnimationFrame(() => { $('#site-name').focus(); $('#site-name').select(); }); }
$('#site-adapter').addEventListener('change', () => renderConfigFields($('#site-config').value));
$('#site-form').addEventListener('submit', async (event) => { event.preventDefault(); const config = configFromFields(); try { JSON.parse(config); } catch { $('#dashboard-message').textContent = '适配器配置不是有效 JSON。'; return; } const id = Number($('#site-id').value); const payload = { name: $('#site-name').value.trim(), baseUrl: $('#site-base-url').value.trim(), sourceUrl: $('#site-source-url').value.trim(), adapterKey: $('#site-adapter').value, adapterConfig: config, customFailureReason: $('#site-failure-reason').value.trim(), enabled: $('#site-enabled').checked, sessionRequired: $('#site-session-required').checked, intervalSeconds: Number($('#site-interval').value) * 60, jitterSeconds: Number($('#site-jitter').value) }; const response = await fetch(id ? `/api/v1/admin/sites/${id}` : '/api/v1/admin/sites', { method: id ? 'PATCH' : 'POST', headers: jsonHeaders(), body: JSON.stringify(payload) }); if (response.ok) { $('#site-dialog').close(); await loadSites(); renderOverview(); $('#dashboard-message').textContent = id ? `站点“${payload.name}”已更新` : `已新增站点“${payload.name}”`; } else $('#dashboard-message').textContent = '站点保存失败。'; });
async function toggleSite(id) { const site = sites.find((item) => item.id === id); if (!site) return; const response = await fetch(`/api/v1/admin/sites/${id}`, { method: 'PATCH', headers: jsonHeaders(), body: JSON.stringify({ name: site.name, adapterKey: site.adapterKey, adapterConfig: site.adapterConfig, enabled: !site.enabled, sessionRequired: site.sessionRequired, intervalSeconds: site.intervalSeconds, jitterSeconds: site.jitterSeconds }) }); if (response.ok) await loadSites(); }
async function deleteSite(id) { const site = sites.find((item) => item.id === id); if (!site || !window.confirm(`删除站点“${site.name}”？历史数据会保留，可通过 API 恢复。`)) return; const response = await fetch(`/api/v1/admin/sites/${id}`, { method: 'DELETE', headers: { 'X-CSRF-Token': csrf() } }); $('#dashboard-message').textContent = response.ok ? '站点已删除。' : '站点删除失败。'; if (response.ok) { await Promise.all([loadSites(), loadRuns(), loadUnmatched()]); renderOverview(); } }
async function collectSite(id) { $('#dashboard-message').textContent = '正在采集…'; const response = await fetch(`/api/v1/admin/sites/${id}/collect`, { method: 'POST', headers: { 'X-CSRF-Token': csrf() } }); $('#dashboard-message').textContent = response.ok ? '采集完成。' : '采集失败，已保留上次成功数据。'; await Promise.all([loadSites(), loadRuns()]); }
async function importSession(id) { const raw = window.prompt('粘贴会话 JSON（含 userAgent 和 cookies；newapi_token 类型另需 authType、accessToken、userId，并包含 new_api_refresh Cookie 以便自动续期）。留空并确认不会修改。'); if (!raw) return; try { JSON.parse(raw); } catch { $('#dashboard-message').textContent = '会话 JSON 格式错误。'; return; } const response = await fetch(`/api/v1/admin/sites/${id}/session`, { method: 'POST', headers: jsonHeaders(), body: raw }); $('#dashboard-message').textContent = response.ok ? '会话已加密保存。' : '会话保存失败。'; await loadSites(); }

$('#session-sync').onclick = async () => { const response = await fetch('/api/v1/admin/session-sync/pair', { method: 'POST', headers: jsonHeaders() }); if (!response.ok) { $('#dashboard-message').textContent = '无法创建浏览器同步配对码。'; return; } const pairing = await response.json(); window.prompt('将此配对码输入浏览器同步扩展：', pairing.code); };

function rulePayload() { return { provider: $('#rule-provider').value.trim(), canonicalName: $('#rule-name').value.trim(), requiredTerms: words($('#rule-required').value), anyTerms: words($('#rule-any').value), excludedTerms: words($('#rule-excluded').value), aliases: words($('#rule-aliases').value), pattern: $('#rule-pattern').value.trim(), priority: Number($('#rule-priority').value), enabled: $('#rule-enabled').checked, generated: rules.find((item) => item.id === Number($('#rule-id').value))?.generated || false }; }
function openRule(id = 0) { const rule = rules.find((item) => item.id === id); $('#rule-id').value = rule?.id || ''; $('#rule-provider').value = rule?.provider || ''; $('#rule-name').value = rule?.canonicalName || ''; $('#rule-required').value = (rule?.requiredTerms || []).join(', '); $('#rule-any').value = (rule?.anyTerms || []).join(', '); $('#rule-excluded').value = (rule?.excludedTerms || []).join(', '); $('#rule-aliases').value = (rule?.aliases || []).join(', '); $('#rule-pattern').value = rule?.pattern || ''; $('#rule-priority').value = rule?.priority ?? 100; $('#rule-enabled').checked = rule?.enabled ?? true; $('#rule-preview').textContent = ''; $('#rule-dialog').showModal(); }
$('#rule-form').addEventListener('submit', async (event) => { event.preventDefault(); const id = Number($('#rule-id').value); const response = await fetch(id ? `/api/v1/admin/rules/${id}` : '/api/v1/admin/rules', { method: id ? 'PUT' : 'POST', headers: jsonHeaders(), body: JSON.stringify(rulePayload()) }); if (response.ok) { $('#rule-dialog').close(); await Promise.all([loadRules(), loadConflicts()]); } else $('#rule-preview').textContent = '规则保存失败，请检查正则表达式和模型名。'; });
$('#preview-rule').onclick = async () => { const response = await fetch('/api/v1/admin/rules/preview', { method: 'POST', headers: jsonHeaders(), body: JSON.stringify(rulePayload()) }); if (!response.ok) { $('#rule-preview').textContent = '预览失败。'; return; } const matches = (await response.json()).matches || []; $('#rule-preview').innerHTML = matches.length ? matches.map((item) => `<div>${escapeHTML(item.siteName)} · ${escapeHTML(item.rawModelName)}</div>`).join('') : '当前已发现模型中没有命中项。'; };
async function deleteRule(id) { if (!window.confirm('删除这条自定义规则？')) return; const response = await fetch(`/api/v1/admin/rules/${id}`, { method: 'DELETE', headers: { 'X-CSRF-Token': csrf() } }); if (response.ok) await Promise.all([loadRules(), loadConflicts()]); }

$('#add-site').onclick = () => openSite(); $('#add-rule').onclick = () => openRule(); $('#refresh').onclick = loadAll; $('#run-filter-apply').onclick = loadRuns; $('#unmatched-refresh').onclick = loadUnmatched; $('#logout').onclick = async () => { const response = await fetch('/api/v1/admin/logout', { method: 'POST', headers: { 'X-CSRF-Token': csrf() } }); if (response.ok) window.location.reload(); };
document.querySelectorAll('[data-close]').forEach((button) => button.onclick = () => $(`#${button.dataset.close}`).close());
document.querySelectorAll('[data-tab]').forEach((button) => button.onclick = () => { document.querySelectorAll('[data-tab]').forEach((item) => item.setAttribute('aria-pressed', String(item === button))); document.querySelectorAll('.admin-panel').forEach((panel) => { panel.hidden = panel.id !== `${button.dataset.tab}-panel`; }); });
