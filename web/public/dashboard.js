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
const filterPanel = document.querySelector('#filter-panel');
const clearFilters = document.querySelector('#clear-filters');
const themeToggle = document.querySelector('#theme-toggle');
const userAction = document.querySelector('#user-action');
const feedbackDialog = document.querySelector('#feedback-dialog');
const feedbackMessage = document.querySelector('#feedback-message');
const announcementDialog = document.querySelector('#announcement-dialog');
const announcementAction = document.querySelector('#announcement-action');
const announcementClose = document.querySelector('#announcement-close');
const announcementContent = document.querySelector('#announcement-content');

let rows = [];
let cards = [];
let historyBuckets = [];
let historyEnd = Date.now();
let view = 'model';
let revision = null;
let currentPage = 1;
let pageSize = 20;
let currentUser = null;
let announcements = [];
let announcementSignature = '';

const selectedFilters = { provider: new Set(), model: new Set(), site: new Set() };
const stateLabels = { healthy: '健康', degraded: '降级', failed: '故障', no_samples: '暂无样本', unknown: '未知' };
const acquisitionLabels = { fresh: '采集正常', stale: '采集过期', collection_failed: '采集失败', login_expired: '登录失效', challenge_pending: '等待验证', challenge_failed: '验证失败', unknown: '采集未知' };
const filterDefinitions = [
  { key: 'site', title: '站点', allLabel: '全部站点', single: true, value: (card) => card.siteName },
  { key: 'provider', title: '模型供应商', allLabel: '全部供应商', value: (card) => card.provider || '未归类' },
  { key: 'model', title: '标准模型', allLabel: '全部模型', value: (card) => card.ruleName || card.rawModelName }
];
const slotCount = 48;
const slotDuration = 30 * 60 * 1000;
const pageSizeOptions = [20, 40, 60, 100];

const formatMetric = (value, suffix = '') => value == null ? '—' : `${Number(value).toFixed(Math.abs(value) < 10 ? 2 : 0)}${suffix}`;
const formatRatio = (value) => value == null ? '—' : `${(Number(value) * 100).toFixed(1)}%`;
const formatMoney = (value) => {
  if (value == null) return '—';
  const amount = Math.abs(Number(value));
  const digits = amount >= 100 ? 2 : amount >= 1 ? 3 : amount >= 0.01 ? 4 : 6;
  return Number(value).toFixed(digits).replace(/0+$/, '').replace(/\.$/, '');
};
const formatMultiplier = (price) => price?.groupMultiplier == null ? '' : `×${formatMoney(price.groupMultiplier)}`;
const formatPricePart = (label, value, currency) => `<span class="price-part"><span class="price-label">${label}</span><span class="price-value">${value == null ? '—' : `${currency}${formatMoney(value)}`}</span></span>`;
const formatPrice = (price) => {
  if (!price?.available) return '<span class="price-unavailable">价格未提供</span>';
  const currency = escapeHTML(price.currencySymbol || price.currency || '');
  if (price.mode === 'fixed' || price.fixedPerRequest != null) return `<span class="price-part"><span class="price-value">${currency}${formatMoney(price.fixedPerRequest)}</span><span class="price-label">/次</span></span>`;
  if (price.inputPerMillion != null) {
    const parts = [formatPricePart('输入', price.inputPerMillion, currency), formatPricePart('输出', price.outputPerMillion, currency)];
    if (price.cacheReadPerMillion != null && price.cacheWritePerMillion != null && Math.abs(price.cacheReadPerMillion - price.cacheWritePerMillion) > 1e-12) {
      parts.push(formatPricePart('缓存读', price.cacheReadPerMillion, currency), formatPricePart('缓存写', price.cacheWritePerMillion, currency));
    } else {
      parts.push(formatPricePart('缓存', price.cacheReadPerMillion ?? price.cacheWritePerMillion, currency));
    }
    return parts.join('');
  }
  return '<span class="price-unavailable">价格未提供</span>';
};
const formatTime = (value) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—';
const formatCompactTime = (value) => value ? new Date(value).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }) : '—';
const escapeHTML = (value) => String(value ?? '').replace(/[&<>"']/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]);
const cardKey = (siteId, rawModelName) => JSON.stringify([siteId, rawModelName]);
const stateRank = (state) => ({ healthy: 0, degraded: 1, failed: 2, unknown: 3, no_samples: 4 })[state] ?? 5;

function compareRows(a, b) {
  const serviceRank = { healthy: 0, degraded: 1, failed: 2, unknown: 3, no_samples: 4 };
  const freshA = a.acquisitionState === 'fresh' ? 0 : 1;
  const freshB = b.acquisitionState === 'fresh' ? 0 : 1;
  return (serviceRank[a.serviceState] ?? 9) - (serviceRank[b.serviceState] ?? 9)
    || freshA - freshB
    || (b.successRatio ?? -1) - (a.successRatio ?? -1)
    || (b.requestCount ?? -1) - (a.requestCount ?? -1)
    || (a.averageLatencyMs ?? Infinity) - (b.averageLatencyMs ?? Infinity)
    || a.siteName.localeCompare(b.siteName, 'zh-CN');
}

function compareCards(a, b) {
  return compareRows(a, b)
    || a.siteName.localeCompare(b.siteName, 'zh-CN')
    || a.rawModelName.localeCompare(b.rawModelName, 'zh-CN');
}

function buildTimeline(buckets) {
  const end = Math.ceil(historyEnd / slotDuration) * slotDuration;
  const start = end - slotCount * slotDuration;
  const states = Array(slotCount).fill('no_samples');
  for (const bucket of buckets) {
    const bucketStart = Date.parse(bucket.start);
    const bucketEnd = Date.parse(bucket.end);
    if (!Number.isFinite(bucketStart) || !Number.isFinite(bucketEnd) || bucketEnd <= start || bucketStart >= end) continue;
    const first = Math.max(0, Math.floor((Math.max(bucketStart, start) - start) / slotDuration));
    const last = Math.min(slotCount - 1, Math.ceil((Math.min(bucketEnd, end) - start) / slotDuration) - 1);
    for (let index = first; index <= last; index++) {
      if (stateRank(bucket.serviceState) < stateRank(states[index])) states[index] = bucket.serviceState;
    }
  }
  return states.map((state, index) => ({
    state,
    start: new Date(start + index * slotDuration),
    end: new Date(start + (index + 1) * slotDuration)
  }));
}

function buildCards() {
  const grouped = new Map();
  for (const row of rows) {
    const key = cardKey(row.siteId, row.rawModelName);
    if (!grouped.has(key)) {
      grouped.set(key, {
        key,
        provider: row.provider || '未归类',
        ruleName: row.ruleName || row.rawModelName,
        siteId: row.siteId,
        siteName: row.siteName,
        siteUrl: row.siteUrl,
        rawModelName: row.rawModelName,
        groups: []
      });
    }
    grouped.get(key).groups.push(row);
  }

  const historyByCard = new Map();
  for (const bucket of historyBuckets) {
    const key = cardKey(bucket.siteId, bucket.rawModelName);
    if (!historyByCard.has(key)) historyByCard.set(key, []);
    historyByCard.get(key).push(bucket);
  }

  cards = [...grouped.values()].map((card) => {
    card.groups.sort((a, b) => compareRows(a, b) || a.groupName.localeCompare(b.groupName, 'zh-CN'));
    const representative = [...card.groups].sort(compareRepresentativeRows)[0];
    Object.assign(card, representative);
    card.groups = grouped.get(card.key).groups;
    card.key = cardKey(card.siteId, card.rawModelName);
    card.searchText = [card.provider, card.ruleName, card.siteName, card.rawModelName, ...card.groups.map((group) => group.groupName)].join(' ').toLowerCase();
    const cardHistory = historyByCard.get(card.key) || [];
    card.hasHistory = cardHistory.length > 0;
    card.timeline = buildTimeline(cardHistory);
    card.lowestPrice = lowestPrice(card.groups);
    return card;
  });

  for (const definition of filterDefinitions) {
    const known = new Set(cards.map(definition.value));
    selectedFilters[definition.key] = new Set([...selectedFilters[definition.key]].filter((value) => known.has(value)));
  }
}

function compareRepresentativeRows(a, b) {
  const stateOrder = { healthy: 0, degraded: 1, failed: 2, unknown: 3, no_samples: 4 };
  const freshA = a.acquisitionState === 'fresh' ? 0 : 1;
  const freshB = b.acquisitionState === 'fresh' ? 0 : 1;
  return (stateOrder[a.serviceState] ?? 9) - (stateOrder[b.serviceState] ?? 9)
    || freshA - freshB
    || (Date.parse(b.observedAt) || 0) - (Date.parse(a.observedAt) || 0)
    || (b.successRatio ?? -1) - (a.successRatio ?? -1)
    || (a.averageLatencyMs ?? Infinity) - (b.averageLatencyMs ?? Infinity)
    || a.groupName.localeCompare(b.groupName, 'zh-CN');
}

function priceValue(price) {
  if (!price?.available) return Infinity;
  if (price.inputPerMillion != null) return Number(price.inputPerMillion);
  if (price.fixedPerRequest != null) return Number(price.fixedPerRequest);
  return Infinity;
}

function lowestPrice(groups) {
  return groups
    .filter((group) => (group.serviceState === 'healthy' || group.serviceState === 'degraded') && group.price?.available)
    .sort((a, b) => priceValue(a.price) - priceValue(b.price))[0]?.price || null;
}

function cardMatchesFilters(card, excludedCategory = '') {
  return filterDefinitions.every((definition) => definition.key === excludedCategory
    || !selectedFilters[definition.key].size
    || selectedFilters[definition.key].has(definition.value(card)));
}

function updateFilterSelection(category, value, selectAll) {
  const selected = selectedFilters[category];
  if (selectAll) {
    selected.clear();
    return;
  }
  if (category === 'site') {
    const wasSelected = selected.has(value);
    selected.clear();
    if (!wasSelected) selected.add(value);
    return;
  }
  if (selected.has(value)) selected.delete(value);
  else selected.add(value);
}

function renderFilters() {
  filterPanel.innerHTML = filterDefinitions.map((definition) => {
    const available = cards.filter((card) => cardMatchesFilters(card, definition.key));
    const counts = new Map();
    for (const card of available) {
      const value = definition.value(card);
      counts.set(value, (counts.get(value) || 0) + 1);
    }
    const values = [...new Set(cards.map(definition.value))].sort((a, b) => a.localeCompare(b, 'zh-CN'));
    const allSelected = selectedFilters[definition.key].size === 0;
    const options = values.map((value) => {
      const selected = selectedFilters[definition.key].has(value);
      const selectionState = definition.single ? ` role="radio" aria-checked="${selected}"` : ` aria-pressed="${selected}"`;
      return `<button type="button" class="filter-chip${selected ? ' selected' : ''}" data-filter-category="${definition.key}" data-filter-value="${escapeHTML(value)}"${selectionState}${counts.has(value) ? '' : ' disabled'}><span>${escapeHTML(value)}</span><b>${counts.get(value) || 0}</b></button>`;
    }).join('');
    const groupRole = definition.single ? ' role="radiogroup"' : '';
    const allSelectionState = definition.single ? ` role="radio" aria-checked="${allSelected}"` : ` aria-pressed="${allSelected}"`;
    return `<section class="filter-section"><h3>${definition.title}</h3><div class="filter-options"${groupRole} aria-label="${definition.title}"><button type="button" class="filter-chip${allSelected ? ' selected' : ''}" data-filter-category="${definition.key}" data-filter-all="true"${allSelectionState}><span>${definition.allLabel}</span><b>${available.length}</b></button>${options}</div></section>`;
  }).join('');

  filterPanel.querySelectorAll('.filter-chip').forEach((button) => button.addEventListener('click', () => {
    const category = button.dataset.filterCategory;
    updateFilterSelection(category, button.dataset.filterValue, Boolean(button.dataset.filterAll));
    currentPage = 1;
    render();
  }));
}

function renderTimeline(timeline) {
  const counts = timeline.reduce((result, slot) => {
    result[slot.state] = (result[slot.state] || 0) + 1;
    return result;
  }, {});
  const summary = Object.entries(counts).map(([state, count]) => `${stateLabels[state] || state}${count}格`).join('，');
  return `<div class="uptime-strip" aria-label="最近 24 小时状态：${escapeHTML(summary)}">${timeline.map((slot) => `<i class="${slot.state}"></i>`).join('')}</div>`;
}

function renderCard(card) {
  const title = view === 'model'
    ? `<strong class="card-title"><span>${escapeHTML(card.siteName)}</span><small> · ${escapeHTML(card.rawModelName)}</small></strong>`
    : `<strong>${escapeHTML(card.rawModelName)}</strong>`;
  const groups = card.groups.map((group) => {
    const multiplier = formatMultiplier(group.price);
    return `<span class="group-chip" title="${escapeHTML(group.groupName)}：${stateLabels[group.serviceState] || group.serviceState}"><i class="${group.serviceState}"></i>${escapeHTML(group.groupName)}${multiplier ? ` (${multiplier})` : ''}</span>`;
  }).join('');
  return `<article class="model-card ${card.serviceState}" tabindex="0" data-model="${escapeHTML(card.rawModelName)}" data-site="${escapeHTML(card.siteName)}"><div class="card-head"><div>${title}</div><span class="state-badge ${card.serviceState}">${stateLabels[card.serviceState] || card.serviceState}</span></div><div class="card-price${card.lowestPrice ? '' : ' unavailable'}">${formatPrice(card.lowestPrice)}</div><div class="card-metrics"><div><span>24 小时成功率</span><strong>${formatRatio(card.successRatio)}</strong></div><div><span>24 小时平均延迟</span><strong>${formatMetric(card.averageLatencyMs, ' ms')}</strong></div></div>${renderTimeline(card.timeline)}<div class="group-list" aria-label="站内分组">${groups}</div><footer><span title="${escapeHTML(acquisitionLabels[card.acquisitionState] || card.acquisitionState)}">${escapeHTML(acquisitionLabels[card.acquisitionState] || card.acquisitionState)}</span><time>更新 ${formatCompactTime(card.collectedAt)}</time></footer></article>`;
}

function orderedCards(items) {
  const grouped = new Map();
  for (const card of items) {
    const key = view === 'model' ? (card.ruleName || card.rawModelName) : card.siteName;
    if (!grouped.has(key)) grouped.set(key, []);
    grouped.get(key).push(card);
  }
  return [...grouped.entries()]
    .sort(([left], [right]) => left.localeCompare(right, 'zh-CN'))
    .flatMap(([, groupCards]) => groupCards.sort(compareCards));
}

function paginationItems(pageCount) {
  if (pageCount <= 7) return Array.from({ length: pageCount }, (_, index) => index + 1);
  const visible = new Set([1, pageCount, currentPage - 1, currentPage, currentPage + 1]);
  const pages = [...visible].filter((page) => page >= 1 && page <= pageCount).sort((left, right) => left - right);
  const result = [];
  for (const page of pages) {
    if (result.length && page - result[result.length - 1] > 1) result.push(null);
    result.push(page);
  }
  return result;
}

function renderPagination(total, pageCount, start, end) {
  const firstDisabled = currentPage === 1 ? ' disabled' : '';
  const lastDisabled = currentPage === pageCount ? ' disabled' : '';
  const pages = paginationItems(pageCount).map((page) => page == null
    ? '<span class="pagination-gap" aria-hidden="true">…</span>'
    : `<button type="button" class="page-button page-number${page === currentPage ? ' current' : ''}" data-page="${page}"${page === currentPage ? ' aria-current="page"' : ''}>${page}</button>`).join('');
  const sizes = pageSizeOptions.map((size) => `<option value="${size}"${size === pageSize ? ' selected' : ''}>${size}</option>`).join('');
  return `<nav class="dashboard-pagination" aria-label="卡片分页"><div class="pagination-status"><strong>${start + 1}-${end}</strong><span>/ ${total}</span></div><div class="pagination-controls"><button type="button" class="page-button page-icon" data-page="1" aria-label="第一页" title="第一页"${firstDisabled}>«</button><button type="button" class="page-button page-icon" data-page="${currentPage - 1}" aria-label="上一页" title="上一页"${firstDisabled}>‹</button>${pages}<button type="button" class="page-button page-icon" data-page="${currentPage + 1}" aria-label="下一页" title="下一页"${lastDisabled}>›</button><button type="button" class="page-button page-icon" data-page="${pageCount}" aria-label="最后一页" title="最后一页"${lastDisabled}>»</button></div><label class="page-size"><span>每页</span><select aria-label="每页显示数量">${sizes}</select><span>条</span></label></nav>`;
}

function bindPagination() {
  contentElement.querySelectorAll('[data-page]').forEach((button) => button.addEventListener('click', () => {
    if (button.disabled) return;
    currentPage = Number(button.dataset.page);
    render();
    contentElement.scrollIntoView({ block: 'start' });
  }));
  contentElement.querySelector('.page-size select')?.addEventListener('change', (event) => {
    pageSize = Number(event.target.value);
    currentPage = 1;
    render();
    contentElement.scrollIntoView({ block: 'start' });
  });
}

function render() {
  const query = searchElement.value.trim().toLowerCase();
  const filtered = cards.filter((card) => cardMatchesFilters(card)
    && (!healthyOnly.checked || (card.serviceState === 'healthy' && card.acquisitionState === 'fresh'))
    && (!query || card.searchText.includes(query)));
  const groupCount = filtered.reduce((total, card) => total + card.groups.length, 0);
  summaryElement.innerHTML = `<span><strong>${filtered.length}</strong> / ${cards.length} 个模型入口 · ${groupCount} 个站内分组</span><span class="timeline-legend" aria-label="状态条图例"><i class="healthy"></i>健康<i class="degraded"></i>降级<i class="failed"></i>故障<i class="no_samples"></i>无样本</span>`;
  renderFilters();

  if (!filtered.length) {
    currentPage = 1;
    contentElement.innerHTML = '<div class="empty"><h1>暂无匹配数据</h1><p>当前没有符合筛选条件的已采集记录，或站点还没有成功采集。</p></div>';
    return;
  }

  const ordered = orderedCards(filtered);
  const pageCount = Math.max(1, Math.ceil(ordered.length / pageSize));
  currentPage = Math.min(Math.max(1, currentPage), pageCount);
  const start = (currentPage - 1) * pageSize;
  const end = Math.min(start + pageSize, ordered.length);
  const pageCards = ordered.slice(start, end);
  const grouped = new Map();
  for (const card of pageCards) {
    const key = view === 'model' ? (card.ruleName || card.rawModelName) : card.siteName;
    if (!grouped.has(key)) grouped.set(key, []);
    grouped.get(key).push(card);
  }

  const groupsHTML = [...grouped.entries()]
    .sort(([a], [b]) => a.localeCompare(b, 'zh-CN'))
    .map(([name, items]) => `<section class="result-group"><div class="group-heading"><div><h2>${escapeHTML(name)}</h2><span class="group-count">${items.length}</span></div></div><div class="card-grid">${items.sort(compareCards).map(renderCard).join('')}</div></section>`)
    .join('');
  contentElement.innerHTML = groupsHTML + renderPagination(ordered.length, pageCount, start, end);

  contentElement.querySelectorAll('.model-card').forEach((element) => {
    element.addEventListener('click', () => openDetails(element.dataset.model, element.dataset.site));
    element.addEventListener('keydown', (event) => {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        openDetails(element.dataset.model, element.dataset.site);
      }
    });
  });
  bindPagination();
}

async function loadRows() {
  try {
    await loadAnnouncements();
    const metaResponse = await fetch('/api/v1/meta', { cache: 'no-store' });
    const meta = metaResponse.ok ? await metaResponse.json() : {};
    if (revision !== null && meta.revision === revision) {
      return;
    }
    revision = meta.revision ?? revision;
    const dashboardResponse = await fetch('/api/v1/public/dashboard', { cache: 'no-store' });
    if (!dashboardResponse.ok) throw new Error('dashboard not ready');
    const dashboard = await dashboardResponse.json();
    revision = dashboard.revision ?? revision;
    rows = dashboard.rows || [];
    historyBuckets = dashboard.buckets || [];
    historyEnd = meta.serverTime ? Date.parse(meta.serverTime) : Date.now();
    // Let the page paint before building the large card tree on mobile.
    await new Promise((resolve) => requestAnimationFrame(resolve));
    buildCards();
    render();
  } catch {}
}

function renderAnnouncements() {
  announcementContent.innerHTML = announcements.length
    ? `<ul class="announcement-list">${announcements.map((item) => `<li><span class="announcement-indicator" aria-hidden="true"></span><div class="announcement-item-body"><div class="announcement-item-heading"><strong>${escapeHTML(item.siteName)}</strong><code>${escapeHTML(item.failureCode)}</code></div><p class="announcement-reason">${escapeHTML(item.reason || '当前采集暂时失败，恢复成功后会自动撤下。')}</p></div></li>`).join('')}</ul>`
    : '<p class="muted announcement-empty">当前所有已启用站点均已恢复采集。</p>';
}

async function loadAnnouncements() {
  try {
    const response = await fetch('/api/v1/public/announcements', { cache: 'no-store' });
    if (!response.ok) return;
    const payload = await response.json();
    const next = payload.announcements || [];
    const nextSignature = next.map((item) => `${item.siteId}:${item.failureCode}:${item.reason}`).join('|');
    const changed = nextSignature !== announcementSignature;
    announcements = next;
    announcementSignature = nextSignature;
    renderAnnouncements();
    if (changed && announcements.length) {
      let seen = '';
      try { seen = localStorage.getItem('relaypulse-announcements-seen') || ''; } catch (_) {}
      if (seen !== nextSignature) {
        try { localStorage.setItem('relaypulse-announcements-seen', nextSignature); } catch (_) {}
        announcementDialog.showModal();
      }
    }
  } catch (_) {}
}

announcementAction.addEventListener('click', () => announcementDialog.showModal());
announcementClose.addEventListener('click', () => announcementDialog.close());
announcementDialog.addEventListener('click', (event) => { if (event.target === announcementDialog) announcementDialog.close(); });

async function openDetails(rawModel, siteName) {
  detailTitle.textContent = rawModel;
  detailSubtitle.textContent = siteName ? `${siteName} · 最近 24 小时` : '最近 24 小时';
  detailContent.innerHTML = '<p class="muted">正在读取分时健康度…</p>';
  detailDialog.showModal();
  const query = new URLSearchParams({ raw: rawModel, site: siteName, hours: '24' });
  const response = await fetch(`/api/v1/public/details?${query}`, { cache: 'no-store' });
  if (!response.ok) {
    detailContent.innerHTML = '<p class="muted">详情暂不可用。</p>';
    return;
  }
  const payload = await response.json();
  const buckets = payload.buckets || [];
  const currentGroups = payload.groups || [];
  if (!buckets.length && !currentGroups.length) {
    detailContent.innerHTML = '<p class="muted">暂无分时样本。健康度数据由站点自身探针提供。</p>';
    return;
  }

  const groups = new Map();
  for (const group of currentGroups) {
    groups.set(group.groupName, { current: group, buckets: [] });
  }
  for (const bucket of buckets) {
    if (!groups.has(bucket.groupName)) groups.set(bucket.groupName, { current: null, buckets: [] });
    groups.get(bucket.groupName).buckets.push(bucket);
  }
  detailContent.innerHTML = [...groups.entries()].map(([groupName, group]) => {
    const latestBucket = group.buckets[group.buckets.length - 1] || null;
    const latest = group.current || latestBucket;
    const recentBucket = latestBucket && Date.parse(latestBucket.start) >= historyEnd - 2 * 60 * 60 * 1000;
    const currentState = group.current?.serviceState || (recentBucket ? latestBucket.serviceState : 'no_samples');
    const latestLabel = latestBucket
      ? (recentBucket ? `最新时段 ${formatTime(latestBucket.start)}` : `最后记录 ${formatTime(latestBucket.start)} · ${stateLabels[latestBucket.serviceState] || latestBucket.serviceState}`)
      : `更新 ${formatTime(group.current?.observedAt)} · 暂无分时样本`;
    const multiplier = formatMultiplier(latest?.price);
    const metrics = latestBucket || group.current || {};
    const successLabel = latestBucket ? '最新时段成功率' : '当前成功率';
    return `<section class="detail-group"><div class="detail-group-head"><div><strong>${escapeHTML(groupName)}${multiplier ? `<small class="detail-group-multiplier">${multiplier}</small>` : ''}</strong><span class="muted">${escapeHTML(latestLabel)}</span></div><span class="state-badge ${currentState}">${stateLabels[currentState] || currentState}</span></div>${renderTimeline(buildTimeline(group.buckets))}<div class="detail-metrics"><span>${successLabel} <strong>${formatRatio(metrics.successRatio)}</strong></span><span>平均延迟 <strong>${formatMetric(metrics.averageLatencyMs, ' ms')}</strong></span><span>首字延迟 <strong>${formatMetric(metrics.firstTokenMs, ' ms')}</strong></span><span>每秒令牌数 <strong>${formatMetric(metrics.tokensPerSecond)}</strong></span></div></section>`;
  }).join('');
}

const systemTheme = matchMedia('(prefers-color-scheme: dark)');

function applyTheme(preference) {
  const mode = preference === 'light' || preference === 'dark' ? preference : 'auto';
  document.documentElement.dataset.theme = mode === 'auto' ? (systemTheme.matches ? 'dark' : 'light') : mode;
  const icons = { auto: '◐', light: '☀', dark: '☾' };
  const labels = { auto: '主题：跟随系统', light: '主题：浅色模式', dark: '主题：深色模式' };
  themeToggle.dataset.mode = mode;
  themeToggle.querySelector('span').textContent = icons[mode];
  themeToggle.setAttribute('aria-label', labels[mode]);
  themeToggle.title = labels[mode];
}

function initializeTheme() {
  let preference = 'auto';
  try {
    preference = localStorage.getItem('relaypulse-theme') || 'auto';
  } catch (_) {}
  applyTheme(preference);
}

detailClose.addEventListener('click', () => detailDialog.close());
searchElement.addEventListener('input', () => { currentPage = 1; render(); });
healthyOnly.addEventListener('change', () => { currentPage = 1; render(); });
clearFilters.addEventListener('click', () => {
  for (const selected of Object.values(selectedFilters)) selected.clear();
  currentPage = 1;
  render();
});
modelViewButton.addEventListener('click', () => {
  view = 'model';
  currentPage = 1;
  modelViewButton.setAttribute('aria-pressed', 'true');
  siteViewButton.setAttribute('aria-pressed', 'false');
  render();
});
siteViewButton.addEventListener('click', () => {
  view = 'site';
  currentPage = 1;
  modelViewButton.setAttribute('aria-pressed', 'false');
  siteViewButton.setAttribute('aria-pressed', 'true');
  render();
});
themeToggle.addEventListener('click', () => {
  const modes = ['auto', 'light', 'dark'];
  const preference = modes[(modes.indexOf(themeToggle.dataset.mode) + 1) % modes.length];
  try { localStorage.setItem('relaypulse-theme', preference); } catch (_) {}
  applyTheme(preference);
});
systemTheme.addEventListener('change', () => {
  if (themeToggle.dataset.mode === 'auto') applyTheme('auto');
});

async function loadUser() {
  const response = await fetch('/api/v1/auth/me', { cache: 'no-store' });
  if (!response.ok) {
    userAction.hidden = true;
    return;
  }
  const identity = await response.json();
  currentUser = identity.authenticated ? identity.user : null;
  userAction.textContent = currentUser ? `反馈 · ${currentUser.username}` : '登录';
}

userAction.addEventListener('click', () => {
  if (currentUser) {
    feedbackMessage.textContent = '';
    feedbackDialog.showModal();
    return;
  }
  window.location.assign('/api/v1/auth/linuxdo');
});
document.querySelector('#feedback-close').addEventListener('click', () => feedbackDialog.close());
document.querySelector('#feedback-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const content = document.querySelector('#feedback-content').value.trim();
  const response = await fetch('/api/v1/feedback', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ content }) });
  if (response.ok) {
    document.querySelector('#feedback-content').value = '';
    feedbackMessage.textContent = '反馈已提交。';
  } else if (response.status === 401) {
    feedbackDialog.close();
    currentUser = null;
    await loadUser();
  } else {
    feedbackMessage.textContent = '提交失败，请稍后再试。';
  }
});

initializeTheme();
loadRows();
loadUser();
setInterval(loadRows, 60000);
