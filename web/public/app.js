const searchElement = document.querySelector('#search');
const contentElement = document.querySelector('#content');
const summaryElement = document.querySelector('#summary');
const modelViewButton = document.querySelector('#model-view');
const siteViewButton = document.querySelector('#site-view');
const detailDialog = document.querySelector('#detail-dialog');
const detailTitle = document.querySelector('#detail-title');
const detailSubtitle = document.querySelector('#detail-subtitle');
const detailContent = document.querySelector('#detail-content');
const detailClose = document.querySelector('#detail-close');
const healthyOnly = document.querySelector('#healthy-only');
const filterTree = document.querySelector('#filter-tree');
const clearFilters = document.querySelector('#clear-filters');
const userAction = document.querySelector('#user-action');
const feedbackDialog = document.querySelector('#feedback-dialog');
const feedbackMessage = document.querySelector('#feedback-message');
let rows = [];
let view = 'model';
let revision = null;
let selectedKeys = new Set();
let currentUser = null;
const expandedPaths = new Set();

const stateLabels = { healthy: '健康', degraded: '降级', failed: '故障', no_samples: '暂无样本', unknown: '未知' };
const formatMetric = (value, suffix = '') => value == null ? '—' : `${Number(value).toFixed(Math.abs(value) < 10 ? 2 : 0)}${suffix}`;
const formatRatio = (value) => value == null ? '—' : `${(Number(value) * 100).toFixed(1)}%`;
const formatTime = (value) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—';

function render() {
  const query = searchElement.value.trim().toLowerCase();
  const filtered = rows.filter((row) => (!selectedKeys.size || selectedKeys.has(rowKey(row))) && (!healthyOnly.checked || (row.serviceState === 'healthy' && row.acquisitionState === 'fresh')) && [row.provider, row.ruleName, row.siteName, row.rawModelName, row.groupName].join(' ').toLowerCase().includes(query));
  summaryElement.textContent = `显示 ${filtered.length} / ${rows.length} 条模型分组状态，数据来自站点自身探针。`;
  if (!filtered.length) { contentElement.innerHTML = '<div class="empty"><h1>暂无匹配数据</h1><p>当前没有符合筛选条件的已采集记录，或站点还没有成功采集。</p></div>'; return; }
  const grouped = new Map();
  for (const row of filtered) {
    const key = view === 'model' ? (row.ruleName || row.rawModelName) : row.siteName;
    if (!grouped.has(key)) grouped.set(key, []);
    grouped.get(key).push(row);
  }
  contentElement.innerHTML = [...grouped.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([name, items]) => `<section class="result-group"><div class="group-heading"><h2>${escapeHTML(name)}</h2><span class="muted">${items.length} 个站点分组</span></div>${items.sort(compareRows).map((row) => `<article class="row" tabindex="0" data-model="${escapeHTML(row.rawModelName)}" data-site="${escapeHTML(row.siteName)}"><div><strong>${escapeHTML(view === 'model' ? row.siteName : row.rawModelName)}</strong><span class="muted">${escapeHTML(row.rawModelName)} · ${escapeHTML(row.groupName)}</span></div><div class="state ${row.serviceState}">${stateLabels[row.serviceState] || row.serviceState}<br><span class="muted">采集：${escapeHTML(row.acquisitionState)}</span></div><div class="metric"><strong>${formatRatio(row.successRatio)}</strong><span class="muted">成功率</span></div><div class="metric"><strong>${formatMetric(row.averageLatencyMs, ' ms')}</strong><span class="muted">平均延迟<br>${formatTime(row.collectedAt)}</span></div></article>`).join('')}</section>`).join('');
  contentElement.querySelectorAll('.row').forEach((element) => { element.addEventListener('click', () => openDetails(element.dataset.model, element.dataset.site)); element.addEventListener('keydown', (event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); openDetails(element.dataset.model, element.dataset.site); } }); });
}

function rowKey(row) { return JSON.stringify([row.provider || '未归类', row.ruleName || row.rawModelName, row.siteName, row.rawModelName, row.groupName]); }

function treeNode(label) { return { label, keys: new Set(), children: new Map() }; }

function buildFilterTree() {
  const root = new Map();
  for (const row of rows) {
    const levels = [row.provider || '未归类', row.ruleName || row.rawModelName, row.siteName, row.groupName];
    const key = rowKey(row);
    let children = root;
    for (const label of levels) {
      if (!children.has(label)) children.set(label, treeNode(label));
      const node = children.get(label);
      node.keys.add(key);
      children = node.children;
    }
  }
  const knownKeys = new Set(rows.map(rowKey));
  selectedKeys = new Set([...selectedKeys].filter((key) => knownKeys.has(key)));
  filterTree.innerHTML = [...root.values()].sort(sortNodes).map((node) => renderTreeNode(node, node.label, 0)).join('');
  bindFilterTree();
}

function sortNodes(a, b) { return a.label.localeCompare(b.label, 'zh-CN'); }

function selectionState(keys) {
  let selected = 0;
  for (const key of keys) if (selectedKeys.has(key)) selected++;
  return { checked: keys.size > 0 && selected === keys.size, partial: selected > 0 && selected < keys.size };
}

function renderTreeNode(node, path, depth) {
  const keys = [...node.keys];
  const state = selectionState(node.keys);
  const checked = state.checked ? ' checked' : '';
  const input = `<input type="checkbox" aria-label="筛选 ${escapeHTML(node.label)}" data-filter-keys="${escapeHTML(JSON.stringify(keys))}"${checked}>`;
  const label = `<span class="filter-label" title="${escapeHTML(node.label)}">${escapeHTML(node.label)}</span><span class="filter-count">${node.keys.size}</span>`;
  if (!node.children.size) return `<label class="filter-leaf">${input}${label}</label>`;
  const open = depth === 0 || expandedPaths.has(path) ? ' open' : '';
  const children = [...node.children.values()].sort(sortNodes).map((child) => renderTreeNode(child, `${path}\u0000${child.label}`, depth + 1)).join('');
  return `<details data-tree-path="${escapeHTML(path)}"${open}><summary>${input}${label}</summary><div class="filter-children">${children}</div></details>`;
}

function bindFilterTree() {
  filterTree.querySelectorAll('details').forEach((details) => details.addEventListener('toggle', () => { if (details.open) expandedPaths.add(details.dataset.treePath); else expandedPaths.delete(details.dataset.treePath); }));
  filterTree.querySelectorAll('input[data-filter-keys]').forEach((input) => {
    const keys = JSON.parse(input.dataset.filterKeys);
    const state = selectionState(new Set(keys));
    input.indeterminate = state.partial;
    input.addEventListener('click', (event) => event.stopPropagation());
    input.addEventListener('change', () => {
      for (const key of keys) { if (input.checked) selectedKeys.add(key); else selectedKeys.delete(key); }
      buildFilterTree();
      render();
    });
  });
}

function compareRows(a, b) {
  const stateRank = { healthy: 0, degraded: 1, no_samples: 2, unknown: 3, failed: 4 };
  const freshA = a.acquisitionState === 'fresh' ? 0 : 1; const freshB = b.acquisitionState === 'fresh' ? 0 : 1;
  return (stateRank[a.serviceState] ?? 9) - (stateRank[b.serviceState] ?? 9) || freshA - freshB || (b.successRatio ?? -1) - (a.successRatio ?? -1) || (b.requestCount ?? -1) - (a.requestCount ?? -1) || (a.averageLatencyMs ?? Infinity) - (b.averageLatencyMs ?? Infinity) || a.siteName.localeCompare(b.siteName);
}

function escapeHTML(value) { return String(value ?? '').replace(/[&<>"']/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]); }
async function loadRows() {
  try {
    const metaResponse = await fetch('/api/v1/meta', { cache: 'no-store' });
    const meta = metaResponse.ok ? await metaResponse.json() : {};
    if (revision !== null && meta.revision === revision) return;
    revision = meta.revision ?? revision;
    const response = await fetch('/api/v1/public/rows', { cache: 'no-store' });
    if (!response.ok) throw new Error('rows not ready');
    rows = (await response.json()).rows || [];
    buildFilterTree();
    render();
  } catch {}
}

async function openDetails(rawModel, siteName) {
  detailTitle.textContent = rawModel;
  detailSubtitle.textContent = siteName ? `${siteName} · 最近 24 小时` : '最近 24 小时';
  detailContent.innerHTML = '<p class="muted">正在读取分时健康度…</p>';
  detailDialog.showModal();
  const query = new URLSearchParams({ raw: rawModel, site: siteName, hours: '24' });
  const response = await fetch(`/api/v1/public/details?${query}`, { cache: 'no-store' });
  if (!response.ok) { detailContent.innerHTML = '<p class="muted">详情暂不可用。</p>'; return; }
  const buckets = (await response.json()).buckets || [];
  if (!buckets.length) { detailContent.innerHTML = '<p class="muted">暂无分时样本。健康度数据由站点自身探针提供。</p>'; return; }
  detailContent.innerHTML = buckets.map((bucket) => `<div class="bucket"><div><strong>${escapeHTML(bucket.groupName)}</strong><span class="muted">${formatTime(bucket.start)}</span></div><span class="state ${bucket.serviceState}">${stateLabels[bucket.serviceState] || bucket.serviceState}</span><span>成功率 ${formatRatio(bucket.successRatio)}</span><span>延迟 ${formatMetric(bucket.averageLatencyMs, ' ms')}</span><span>TPS ${formatMetric(bucket.tokensPerSecond)}</span></div>`).join('');
}

detailClose.addEventListener('click', () => detailDialog.close());

async function loadUser() {
  const response = await fetch('/api/v1/auth/me', { cache: 'no-store' });
  if (!response.ok) { userAction.hidden = true; return; }
  const identity = await response.json(); currentUser = identity.authenticated ? identity.user : null;
  userAction.textContent = currentUser ? `反馈 · ${currentUser.username}` : '登录';
}
userAction.addEventListener('click', () => { if (currentUser) { feedbackMessage.textContent = ''; feedbackDialog.showModal(); } else { window.location.assign('/api/v1/auth/linuxdo'); } });
document.querySelector('#feedback-close').addEventListener('click', () => feedbackDialog.close());
document.querySelector('#feedback-form').addEventListener('submit', async (event) => { event.preventDefault(); const content = document.querySelector('#feedback-content').value.trim(); const response = await fetch('/api/v1/feedback', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ content }) }); if (response.ok) { document.querySelector('#feedback-content').value = ''; feedbackMessage.textContent = '反馈已提交。'; } else if (response.status === 401) { feedbackDialog.close(); currentUser = null; await loadUser(); } else feedbackMessage.textContent = '提交失败，请稍后再试。'; });

searchElement.addEventListener('input', render);
healthyOnly.addEventListener('change', render);
clearFilters.addEventListener('click', () => { selectedKeys.clear(); buildFilterTree(); render(); });
modelViewButton.addEventListener('click', () => { view = 'model'; modelViewButton.setAttribute('aria-pressed', 'true'); siteViewButton.setAttribute('aria-pressed', 'false'); render(); });
siteViewButton.addEventListener('click', () => { view = 'site'; modelViewButton.setAttribute('aria-pressed', 'false'); siteViewButton.setAttribute('aria-pressed', 'true'); render(); });
loadRows();
loadUser();
setInterval(loadRows, 60000);
