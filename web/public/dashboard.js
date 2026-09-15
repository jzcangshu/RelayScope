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
const customizeDialog = document.querySelector('#customize-dialog');
const customizeAction = document.querySelector('#customize-action');
const customizeClose = document.querySelector('#customize-close');
const customizeTabDisplay = document.querySelector('#customize-tab-display');
const customizeTabTags = document.querySelector('#customize-tab-tags');
const customizeDisplayPanel = document.querySelector('#customize-display');
const customizeTagsPanel = document.querySelector('#customize-tags');
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
let announcements = [];
let announcementSignature = '';

// 定制个性化（全部保存在浏览器本地）
const TAG_COLORS = ['mint', 'blue', 'violet', 'amber', 'rose', 'slate'];
const CUSTOMIZE_DIMENSIONS = [
  { key: 'sites', title: '站点', valueOf: (card) => card.siteName },
  { key: 'providers', title: '模型供应商', valueOf: (card) => card.provider || '未归类' },
  { key: 'models', title: '标准模型', valueOf: (card) => card.ruleName || card.rawModelName }
];
const HIDDEN_KEYS = { site: 'sites', provider: 'providers', model: 'models' };

let hidden = { sites: new Set(), providers: new Set(), models: new Set() };
let defaultHealthy = false;
let tags = new Map();
let customizeTab = 'display';
let tagFormMode = null;
let tagArmed = null;
let resetArmed = false;
let resetTimer = null;
let openTagMenu = null;
let customizeSearches = { sites: '', providers: '', models: '', tagSites: '' };
let searchFocusKey = null;

const selectedFilters = { provider: new Set(), model: new Set(), site: new Set(), tag: new Set() };
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

const tagDefinition = { key: 'tag', title: '标签', allLabel: '全部标签', anyOf: true, value: (card) => card.tagNames || [] };
const activeFilterDefinitions = () => (tags.size ? [...filterDefinitions, tagDefinition] : filterDefinitions);

const storageGet = (key) => { try { return localStorage.getItem(key); } catch { return null; } };
const storageSet = (key, value) => { try { localStorage.setItem(key, value); } catch {} };
const stringSet = (value) => new Set(Array.isArray(value) ? value.filter((item) => typeof item === 'string') : []);

function loadPreferences() {
  try {
    const raw = JSON.parse(storageGet('relayscope-hidden') || '{}');
    hidden = { sites: stringSet(raw.sites), providers: stringSet(raw.providers), models: stringSet(raw.models) };
  } catch {
    hidden = { sites: new Set(), providers: new Set(), models: new Set() };
  }
  defaultHealthy = storageGet('relayscope-default-healthy') === '1';
  try {
    const raw = JSON.parse(storageGet('relayscope-tags') || '{}');
    tags = new Map();
    for (const [name, value] of Object.entries(raw)) {
      if (typeof value !== 'object' || value === null) continue;
      tags.set(name, { color: TAG_COLORS.includes(value.color) ? value.color : 'mint', sites: stringSet(value.sites) });
    }
  } catch {
    tags = new Map();
  }
  healthyOnly.checked = defaultHealthy;
}

const saveHidden = () => storageSet('relayscope-hidden', JSON.stringify({ sites: [...hidden.sites], providers: [...hidden.providers], models: [...hidden.models] }));
const saveDefaultHealthy = () => storageSet('relayscope-default-healthy', defaultHealthy ? '1' : '0');
const saveTags = () => storageSet('relayscope-tags', JSON.stringify(Object.fromEntries([...tags].map(([name, tag]) => [name, { color: tag.color, sites: [...tag.sites] }]))));

function isHiddenCard(card) {
  return hidden.sites.has(card.siteName)
    || hidden.providers.has(card.provider || '未归类')
    || hidden.models.has(card.ruleName || card.rawModelName);
}

function cardTagsOf(siteName) {
  if (!tags.size) return [];
  return [...tags.entries()].filter(([, tag]) => tag.sites.has(siteName)).map(([name]) => name);
}

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
    card.tagNames = cardTagsOf(card.siteName);
    const cardHistory = historyByCard.get(card.key) || [];
    card.hasHistory = cardHistory.length > 0;
    card.timeline = buildTimeline(cardHistory);
    card.lowestPrice = lowestPrice(card.groups);
    return card;
  });

  for (const definition of filterDefinitions) {
    const hiddenKey = HIDDEN_KEYS[definition.key];
    const known = new Set(cards.map(definition.value));
    selectedFilters[definition.key] = new Set([...selectedFilters[definition.key]].filter((value) => known.has(value) && !(hiddenKey && hidden[hiddenKey].has(value))));
  }
  selectedFilters.tag = new Set([...selectedFilters.tag].filter((name) => tags.has(name)));
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
  return activeFilterDefinitions().every((definition) => {
    if (definition.key === excludedCategory) return true;
    const selected = selectedFilters[definition.key];
    if (!selected.size) return true;
    const value = definition.value(card);
    if (definition.anyOf) return value?.some((item) => selected.has(item)) || false;
    return selected.has(value);
  });
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
  const visibleCards = cards.filter((card) => !isHiddenCard(card));
  filterPanel.innerHTML = activeFilterDefinitions().map((definition) => {
    const available = visibleCards.filter((card) => cardMatchesFilters(card, definition.key));
    const counts = new Map();
    for (const card of available) {
      if (definition.anyOf) {
        for (const item of definition.value(card)) counts.set(item, (counts.get(item) || 0) + 1);
      } else {
        const value = definition.value(card);
        counts.set(value, (counts.get(value) || 0) + 1);
      }
    }
    const values = [...new Set(visibleCards.flatMap((card) => (definition.anyOf ? definition.value(card) : [definition.value(card)])))].sort((a, b) => a.localeCompare(b, 'zh-CN'));
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
  const tagsHTML = (card.tagNames || []).length
    ? `<div class="card-tags">${card.tagNames.map((name) => {
      const color = tags.get(name)?.color || 'mint';
      return `<button type="button" class="tag-chip ${color}" data-tag-name="${escapeHTML(name)}" title="按标签筛选：${escapeHTML(name)}"><i aria-hidden="true"></i>${escapeHTML(name)}</button>`;
    }).join('')}</div>`
    : '';
  const groups = card.groups.map((group) => {
    const multiplier = formatMultiplier(group.price);
    return `<span class="group-chip" title="${escapeHTML(group.groupName)}：${stateLabels[group.serviceState] || group.serviceState}"><i class="${group.serviceState}"></i>${escapeHTML(group.groupName)}${multiplier ? ` (${multiplier})` : ''}</span>`;
  }).join('');
  return `<article class="model-card ${card.serviceState}" tabindex="0" data-model="${escapeHTML(card.rawModelName)}" data-site="${escapeHTML(card.siteName)}"><div class="card-head"><div>${title}</div><span class="state-badge ${card.serviceState}">${stateLabels[card.serviceState] || card.serviceState}</span></div>${tagsHTML}<div class="card-price${card.lowestPrice ? '' : ' unavailable'}">${formatPrice(card.lowestPrice)}</div><div class="card-metrics"><div><span>24 小时成功率</span><strong>${formatRatio(card.successRatio)}</strong></div><div><span>24 小时平均延迟</span><strong>${formatMetric(card.averageLatencyMs, ' ms')}</strong></div></div>${renderTimeline(card.timeline)}<div class="group-list" aria-label="站内分组">${groups}</div><footer><span title="${escapeHTML(acquisitionLabels[card.acquisitionState] || card.acquisitionState)}">${escapeHTML(acquisitionLabels[card.acquisitionState] || card.acquisitionState)}</span><time>更新 ${formatCompactTime(card.collectedAt)}</time></footer></article>`;
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
  const visibleCards = cards.filter((card) => !isHiddenCard(card));
  const filtered = visibleCards.filter((card) => cardMatchesFilters(card)
    && (!healthyOnly.checked || (card.serviceState === 'healthy' && card.acquisitionState === 'fresh'))
    && (!query || card.searchText.includes(query)));
  const groupCount = filtered.reduce((total, card) => total + card.groups.length, 0);
  const hiddenCount = cards.length - visibleCards.length;
  summaryElement.innerHTML = `<span><strong>${filtered.length}</strong> / ${visibleCards.length} 个模型入口 · ${groupCount} 个站内分组${hiddenCount ? ` <button type="button" class="customize-hint" data-open-customize>已屏蔽 ${hiddenCount} 项</button>` : ''}</span><span class="timeline-legend" aria-label="状态条图例"><i class="healthy"></i>健康<i class="degraded"></i>降级<i class="failed"></i>故障<i class="no_samples"></i>无样本</span>`;
  renderFilters();

  if (!filtered.length) {
    currentPage = 1;
    contentElement.innerHTML = visibleCards.length
      ? '<div class="empty"><h1>暂无匹配数据</h1><p>当前没有符合筛选条件的已采集记录，或站点还没有成功采集。</p></div>'
      : '<div class="empty"><h1>内容已被屏蔽</h1><p>顶栏「定制」中可以恢复被屏蔽的站点、供应商或模型。</p><button type="button" class="customize-hint" data-open-customize>打开定制</button></div>';
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
      if (event.target !== element) return;
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        openDetails(element.dataset.model, element.dataset.site);
      }
    });
    element.querySelectorAll('.tag-chip').forEach((chip) => chip.addEventListener('click', (event) => {
      event.stopPropagation();
      const name = chip.dataset.tagName;
      if (selectedFilters.tag.has(name)) selectedFilters.tag.delete(name);
      else selectedFilters.tag.add(name);
      currentPage = 1;
      render();
    }));
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
      try { seen = localStorage.getItem('relayscope-announcements-seen') || ''; } catch (_) {}
      if (seen !== nextSignature) {
        try { localStorage.setItem('relayscope-announcements-seen', nextSignature); } catch (_) {}
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
  const icons = { auto: '<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8m-4-4v4"/>', light: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.4 1.4m11.2 11.2L19 19M5 19l1.4-1.4M17.6 6.4 19 5"/>', dark: '<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/>' };
  const labels = { auto: '主题：跟随系统', light: '主题：浅色模式', dark: '主题：深色模式' };
  themeToggle.dataset.mode = mode;
  themeToggle.querySelector('span').innerHTML = `<svg class="icon" aria-hidden="true" viewBox="0 0 24 24">${icons[mode]}</svg>`;
  themeToggle.setAttribute('aria-label', labels[mode]);
  themeToggle.title = labels[mode];
}

function initializeTheme() {
  let preference = 'auto';
  try {
    preference = localStorage.getItem('relayscope-theme') || 'auto';
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
  try { localStorage.setItem('relayscope-theme', preference); } catch (_) {}
  applyTheme(preference);
});
systemTheme.addEventListener('change', () => {
  if (themeToggle.dataset.mode === 'auto') applyTheme('auto');
});

/* ---------- 定制个性化弹窗 ---------- */

function focusTagInput() {
  const input = customizeTagsPanel.querySelector('[data-tag-name-input]');
  if (input) { input.focus(); input.select(); }
}

function renderCustomize() {
  if (customizeTab === 'display') renderCustomizeDisplay();
  else renderCustomizeTags();
  if (searchFocusKey) {
    const panel = customizeTab === 'display' ? customizeDisplayPanel : customizeTagsPanel;
    const input = panel.querySelector(`[data-pref-search="${searchFocusKey}"]`);
    if (input) {
      input.focus();
      input.setSelectionRange(input.value.length, input.value.length);
    }
  }
}

function setCustomizeTab(tab) {
  customizeTab = tab;
  customizeTabDisplay.setAttribute('aria-selected', String(tab === 'display'));
  customizeTabTags.setAttribute('aria-selected', String(tab === 'tags'));
  customizeDisplayPanel.hidden = tab !== 'display';
  customizeTagsPanel.hidden = tab !== 'tags';
  customizeTagsPanel.scrollTop = 0;
  customizeDisplayPanel.scrollTop = 0;
  renderCustomize();
}

function dimensionCounts(definition) {
  const counts = new Map();
  for (const card of cards) {
    const value = definition.valueOf(card);
    counts.set(value, (counts.get(value) || 0) + 1);
  }
  return [...counts.entries()].map(([value, count]) => ({ value, count })).sort((a, b) => a.value.localeCompare(b.value, 'zh-CN'));
}

function renderCustomizeDisplay() {
  const groups = CUSTOMIZE_DIMENSIONS.map((definition) => {
    const query = customizeSearches[definition.key].trim().toLowerCase();
    const rows = dimensionCounts(definition)
      .filter((item) => !query || item.value.toLowerCase().includes(query))
      .map((item) => {
        const visible = !hidden[definition.key].has(item.value);
        return `<label class="pref-row"><span class="pref-row-text"><strong>${escapeHTML(item.value)}</strong><small>${item.count} 个模型入口</small></span><span class="toggle"><input type="checkbox" data-pref-toggle data-pref-key="${definition.key}" data-pref-value="${escapeHTML(item.value)}"${visible ? ' checked' : ''} aria-label="显示 ${escapeHTML(item.value)}"><i></i></span></label>`;
      }).join('');
    const hiddenCount = hidden[definition.key].size;
    return `<section class="pref-section"><div class="pref-head"><h3>${definition.title}</h3>${hiddenCount ? `<span class="pref-count" data-pref-count="${definition.key}">已屏蔽 ${hiddenCount} 项</span><button type="button" class="pref-reset" data-pref-reset="${definition.key}">全部显示</button>` : ''}</div><label class="pref-search"><span>搜索${definition.title}</span><input type="search" data-pref-search="${definition.key}" value="${escapeHTML(customizeSearches[definition.key])}" placeholder="筛选${definition.title}"></label><div class="pref-list" data-pref-list="${definition.key}">${rows || '<p class="pref-empty">没有匹配的条目</p>'}</div></section>`;
  });
  customizeDisplayPanel.innerHTML = `<section class="pref-section"><div class="pref-head"><h3>默认状态</h3></div><label class="pref-row toggle-row"><span class="pref-row-text"><strong>默认只看当前可用模型</strong><small>开启后每次打开页面都会自动勾选首页的“只看当前可用”，当次访问仍可手动取消</small></span><span class="toggle"><input id="pref-default-healthy" type="checkbox"${defaultHealthy ? ' checked' : ''} aria-label="默认只看当前可用模型"><i></i></span></label></section>${groups.join('')}<section class="pref-foot"><p class="muted">新出现的站点、供应商或模型默认都会展示，需要时再在这里屏蔽。</p><button type="button" class="pref-reset-all${resetArmed ? ' armed' : ''}" data-pref-reset-all>${resetArmed ? '再次点击确认恢复' : '恢复默认'}</button></section>`;
}

function renderCustomizeTags() {
  const manager = [...tags.keys()].map((name) => {
    const tag = tags.get(name);
    const armed = tagArmed?.name === name;
    return `<span class="tag-chip ${tag.color}${armed ? ' armed' : ''}"><button type="button" class="tag-dot" data-tag-color="${escapeHTML(name)}" title="更换颜色" aria-label="更换标签 ${escapeHTML(name)} 的颜色"></button><span class="tag-name">${escapeHTML(name)}</span>${armed
      ? `<button type="button" class="tag-confirm-x" data-tag-delete="${escapeHTML(name)}">确认删除？</button>`
      : `<button type="button" class="tag-tool" data-tag-rename="${escapeHTML(name)}" title="重命名" aria-label="重命名标签 ${escapeHTML(name)}"><svg aria-hidden="true" viewBox="0 0 24 24" focusable="false"><path d="M4 20h4L19.5 8.5a2.1 2.1 0 0 0-3-3L5 17zm11.5-14 3 3" /></svg></button><button type="button" class="tag-tool tag-x" data-tag-delete="${escapeHTML(name)}" title="删除标签" aria-label="删除标签 ${escapeHTML(name)}">✕</button>`}</span>`;
  }).join('');
  const formVisible = tagFormMode !== null;
  const form = `<form class="tag-create${formVisible ? '' : ' hidden'}" data-tag-form><input data-tag-name-input maxlength="12" placeholder="${tagFormMode === 'create' ? '新标签名称' : '重命名标签'}" value="${escapeHTML(tagFormMode === 'create' ? '' : (tagFormMode || ''))}" aria-label="标签名称" autocomplete="off"><button class="primary-button" type="submit">${tagFormMode === 'create' ? '添加' : '保存'}</button><button class="ghost" type="button" data-tag-cancel>取消</button><p class="form-message" data-tag-message role="status" aria-live="polite" hidden></p></form>`;
  const taggedSites = new Set();
  for (const tag of tags.values()) for (const site of tag.sites) taggedSites.add(site);
  const allSites = [...new Set([...cards.map((card) => card.siteName), ...taggedSites])].sort((a, b) => a.localeCompare(b, 'zh-CN'));
  const query = customizeSearches.tagSites.trim().toLowerCase();
  const rows = allSites.filter((name) => !query || name.toLowerCase().includes(query)).map((name) => {
    const chips = [...tags.entries()].filter(([, tag]) => tag.sites.has(name)).map(([tagName]) => `<button type="button" class="tag-chip ${tags.get(tagName).color}" data-site-tag-remove="${escapeHTML(JSON.stringify([name, tagName]))}" title="移除标签" aria-label="从 ${escapeHTML(name)} 移除标签 ${escapeHTML(tagName)}"><i aria-hidden="true"></i>${escapeHTML(tagName)}</button>`).join('');
    const open = openTagMenu === name;
    const menuItems = [...tags.keys()].map((tagName) => `<button type="button" role="menuitemcheckbox" aria-checked="${tags.get(tagName).sites.has(name)}" data-tag-menu-item="${escapeHTML(JSON.stringify([name, tagName]))}"><i class="${tags.get(tagName).color}" aria-hidden="true"></i>${escapeHTML(tagName)}</button>`).join('');
    return `<div class="pref-row site-tag-row"><span class="pref-row-text"><strong>${escapeHTML(name)}</strong></span><span class="site-tags">${chips}<span class="site-tag-menu-wrap"><button type="button" class="tag-add" data-tag-add="${escapeHTML(name)}" aria-haspopup="menu" aria-expanded="${open}">＋</button><div class="tag-menu" role="${menuItems ? 'menu' : ''}"${open ? '' : ' hidden'}>${menuItems || '<p class="tag-menu-empty">先在上方新建标签</p>'}</div></span></span></div>`;
  }).join('');
  customizeTagsPanel.innerHTML = `<section class="pref-section"><div class="pref-head"><h3>我的标签</h3><span class="pref-note">点色点可更换颜色</span></div><div class="tag-manager">${manager || '<span class="muted">还没有标签，点击下方按钮新建。</span>'}<button type="button" class="tag-new" data-tag-new>＋ 新建标签</button></div>${form}</section><section class="pref-section"><div class="pref-head"><h3>站点标签</h3><span class="pref-count">已标记 ${taggedSites.size} 个</span></div><label class="pref-search"><span>搜索站点</span><input type="search" data-pref-search="tagSites" value="${escapeHTML(customizeSearches.tagSites)}" placeholder="筛选站点"></label><div class="pref-list" data-pref-list="tagSites">${rows || '<p class="pref-empty">没有匹配的站点</p>'}</div></section>`;
}

function closeTagMenu() {
  openTagMenu = null;
  customizeTagsPanel.querySelectorAll('[data-tag-add][aria-expanded="true"]').forEach((button) => button.setAttribute('aria-expanded', 'false'));
  customizeTagsPanel.querySelectorAll('.tag-menu').forEach((menu) => { menu.hidden = true; });
}

function handleCustomizeClick(event) {
  const target = event.target.closest('[data-pref-reset],[data-pref-reset-all],[data-tag-new],[data-tag-cancel],[data-tag-delete],[data-tag-color],[data-tag-rename],[data-tag-add],[data-site-tag-remove],[data-tag-menu-item]');
  if (!target) return;

  if (target.matches('[data-pref-reset]')) {
    hidden[target.dataset.prefReset].clear();
    saveHidden();
    currentPage = 1;
    render();
    renderCustomize();
    return;
  }
  if (target.matches('[data-pref-reset-all]')) {
    if (resetArmed) {
      window.clearTimeout(resetTimer);
      resetArmed = false;
      hidden.sites.clear();
      hidden.providers.clear();
      hidden.models.clear();
      defaultHealthy = false;
      saveHidden();
      saveDefaultHealthy();
      currentPage = 1;
      render();
      renderCustomize();
    } else {
      resetArmed = true;
      resetTimer = window.setTimeout(() => { resetArmed = false; if (customizeDialog.open) renderCustomize(); }, 2200);
      renderCustomize();
    }
    return;
  }
  if (target.matches('[data-tag-new]')) {
    tagFormMode = 'create';
    renderCustomize();
    focusTagInput();
    return;
  }
  if (target.matches('[data-tag-cancel]')) {
    tagFormMode = null;
    renderCustomize();
    return;
  }
  if (target.matches('[data-tag-delete]')) {
    const name = target.dataset.tagDelete;
    if (tagArmed?.name === name) {
      clearTimeout(tagArmed.timer);
      tagArmed = null;
      tags.delete(name);
      saveTags();
      render();
      renderCustomize();
    } else {
      if (tagArmed) clearTimeout(tagArmed.timer);
      tagArmed = { name, timer: window.setTimeout(() => { tagArmed = null; if (customizeDialog.open) renderCustomize(); }, 2200) };
      renderCustomize();
    }
    return;
  }
  if (target.matches('[data-tag-color]')) {
    const tag = tags.get(target.dataset.tagColor);
    if (!tag) return;
    tag.color = TAG_COLORS[(TAG_COLORS.indexOf(tag.color) + 1) % TAG_COLORS.length];
    saveTags();
    render();
    renderCustomize();
    return;
  }
  if (target.matches('[data-tag-rename]')) {
    tagFormMode = target.dataset.tagRename;
    renderCustomize();
    focusTagInput();
    return;
  }
  if (target.matches('[data-tag-add]')) {
    const site = target.dataset.tagAdd;
    if (openTagMenu === site) { closeTagMenu(); return; }
    openTagMenu = site;
    customizeTagsPanel.querySelectorAll('[data-tag-add]').forEach((button) => button.setAttribute('aria-expanded', button.dataset.tagAdd === site ? 'true' : 'false'));
    customizeTagsPanel.querySelectorAll('.tag-menu').forEach((menu) => { menu.hidden = menu.parentElement.querySelector('[data-tag-add]').dataset.tagAdd !== site; });
    return;
  }
  if (target.matches('[data-tag-menu-item]')) {
    const [site, tagName] = JSON.parse(target.dataset.tagMenuItem);
    const tag = tags.get(tagName);
    if (!tag) return;
    if (tag.sites.has(site)) tag.sites.delete(site);
    else tag.sites.add(site);
    saveTags();
    render();
    closeTagMenu();
    renderCustomize();
    return;
  }
  if (target.matches('[data-site-tag-remove]')) {
    const [site, tagName] = JSON.parse(target.dataset.siteTagRemove);
    tags.get(tagName)?.sites.delete(site);
    saveTags();
    render();
    renderCustomize();
  }
}

function handleCustomizeChange(event) {
  const input = event.target;
  if (input.id === 'pref-default-healthy') {
    defaultHealthy = input.checked;
    saveDefaultHealthy();
    healthyOnly.checked = defaultHealthy;
    currentPage = 1;
    render();
    return;
  }
  if (!input.matches('[data-pref-toggle]')) return;
  const key = input.dataset.prefKey;
  const value = input.dataset.prefValue;
  if (input.checked) hidden[key].delete(value);
  else hidden[key].add(value);
  saveHidden();
  currentPage = 1;
  render();
  const count = customizeDisplayPanel.querySelector(`[data-pref-count="${key}"]`);
  const resetButton = customizeDisplayPanel.querySelector(`[data-pref-reset="${key}"]`);
  const n = hidden[key].size;
  if (n) {
    if (count) { count.textContent = `已屏蔽 ${n} 项`; count.hidden = false; }
    if (resetButton) resetButton.hidden = false;
  } else {
    if (count) count.hidden = true;
    if (resetButton) resetButton.hidden = true;
  }
}

function handleCustomizeInput(event) {
  const search = event.target.closest('[data-pref-search]');
  if (!search) return;
  const key = search.dataset.prefSearch;
  customizeSearches[key] = search.value;
  searchFocusKey = key;
  const list = search.closest('.pref-section').querySelector('.pref-list');
  if (!list) return;
  const q = search.value.trim().toLowerCase();
  let matches = 0;
  list.querySelectorAll(':scope .pref-row').forEach((row) => {
    const name = row.querySelector('strong')?.textContent || '';
    const visible = !q || name.toLowerCase().includes(q);
    row.hidden = !visible;
    if (visible) matches += 1;
  });
  let empty = list.querySelector(':scope .pref-empty');
  if (!matches) {
    if (!empty) {
      empty = document.createElement('p');
      empty.className = 'pref-empty';
      empty.textContent = '没有匹配的条目';
      list.appendChild(empty);
    }
    empty.hidden = false;
  } else if (empty) {
    empty.hidden = true;
  }
}

function handleCustomizeSubmit(event) {
  const form = event.target.closest('[data-tag-form]');
  if (!form) return;
  event.preventDefault();
  const message = form.querySelector('[data-tag-message]');
  const input = form.querySelector('[data-tag-name-input]');
  const name = input.value.trim();
  message.hidden = true;
  const fail = (text) => { message.textContent = text; message.hidden = false; input.focus(); };
  if (!name) return fail('请输入标签名称。');
  if (tagFormMode === 'create') {
    if (tags.has(name)) return fail('已存在同名标签。');
    tags.set(name, { color: TAG_COLORS[tags.size % TAG_COLORS.length], sites: new Set() });
  } else if (typeof tagFormMode === 'string' && tags.has(tagFormMode)) {
    if (name !== tagFormMode && tags.has(name)) return fail('已存在同名标签。');
    const tag = tags.get(tagFormMode);
    tags.delete(tagFormMode);
    tags.set(name, tag);
  }
  tagFormMode = null;
  saveTags();
  render();
  renderCustomize();
}

customizeAction.addEventListener('click', () => {
  renderCustomize();
  customizeDialog.showModal();
});
customizeClose.addEventListener('click', () => customizeDialog.close());
customizeDialog.addEventListener('click', (event) => {
  if (event.target === customizeDialog) {
    const bounds = customizeDialog.getBoundingClientRect();
    if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) customizeDialog.close();
  }
  handleCustomizeClick(event);
});
customizeDialog.addEventListener('change', handleCustomizeChange);
customizeDialog.addEventListener('input', handleCustomizeInput);
customizeDialog.addEventListener('submit', handleCustomizeSubmit);
customizeDialog.addEventListener('focusin', (event) => {
  const search = event.target.closest('[data-pref-search]');
  if (search) searchFocusKey = search.dataset.prefSearch;
});
customizeTabDisplay.addEventListener('click', () => setCustomizeTab('display'));
customizeTabTags.addEventListener('click', () => setCustomizeTab('tags'));
customizeTabDisplay.addEventListener('keydown', (event) => {
  if (event.key === 'ArrowRight' || event.key === 'ArrowDown') { event.preventDefault(); setCustomizeTab('tags'); customizeTabTags.focus(); }
});
customizeTabTags.addEventListener('keydown', (event) => {
  if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') { event.preventDefault(); setCustomizeTab('display'); customizeTabDisplay.focus(); }
});
document.addEventListener('click', (event) => {
  const hint = event.target.closest('[data-open-customize]');
  if (hint) {
    renderCustomize();
    customizeDialog.showModal();
    return;
  }
  if (openTagMenu && !event.target.closest('.site-tag-menu-wrap')) closeTagMenu();
});

initializeTheme();
loadPreferences();
loadRows();
setInterval(loadRows, 60000);
