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
const customizePage = document.querySelector('#customize-page');
const customizeTabDisplay = document.querySelector('#customize-tab-display');
const customizeTabTags = document.querySelector('#customize-tab-tags');
const customizeDisplayPanel = document.querySelector('#customize-display');
const customizeTagsPanel = document.querySelector('#customize-tags');
const announcementDialog = document.querySelector('#announcement-dialog');
const announcementAction = document.querySelector('#announcement-action');
const announcementClose = document.querySelector('#announcement-close');
const announcementContent = document.querySelector('#announcement-content');
const noticeContent = document.querySelector('#notice-content');
const userAction = document.querySelector('#user-action');
const userMenu = document.querySelector('#user-menu');
const userMenuName = document.querySelector('#user-menu-name');
const userMenuMembership = document.querySelector('#user-menu-membership');
const userMenuAvatar = document.querySelector('#user-menu-avatar');
const feedbackDialog = document.querySelector('#feedback-dialog');
const feedbackClose = document.querySelector('#feedback-close');
const feedbackMessage = document.querySelector('#feedback-message');
const redeemDialog = document.querySelector('#redeem-dialog');
const redeemClose = document.querySelector('#redeem-close');
const redeemCodeInput = document.querySelector('#redeem-code');
const redeemMessage = document.querySelector('#redeem-message');
const rechargeDialog = document.querySelector('#recharge-dialog');
const rechargeClose = document.querySelector('#recharge-close');
const rechargePrice = document.querySelector('#recharge-price');
const pledgeCredit = document.querySelector('#pledge-credit');
const rechargeMessage = document.querySelector('#recharge-message');
const wishPage = document.querySelector('#wish-page');
const wishList = document.querySelector('#wish-list');
const wishNewButton = document.querySelector('#wish-new');
const wishFormDialog = document.querySelector('#wish-form-dialog');
const wishFormClose = document.querySelector('#wish-form-close');
const wishNameInput = document.querySelector('#wish-name');
const wishUrlInput = document.querySelector('#wish-url');
const wishInviteInput = document.querySelector('#wish-invite');
const wishFormMessage = document.querySelector('#wish-form-message');
const pledgeDialog = document.querySelector('#pledge-dialog');
const pledgeClose = document.querySelector('#pledge-close');
const pledgeTitle = document.querySelector('#pledge-title');
const pledgeSubtitle = document.querySelector('#pledge-subtitle');
const pledgeAmountInput = document.querySelector('#pledge-amount');
const pledgeMessage = document.querySelector('#pledge-message');
const customizeSubtitle = document.querySelector('#customize-subtitle');
const customizeNav = document.querySelector('.customize-nav');
const toastRegion = document.querySelector('#toast-region');
const wishBanner = document.querySelector('#wish-banner');

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
const TAG_COLOR_LABELS = { mint: '薄荷绿', blue: '天蓝', violet: '紫罗兰', amber: '琥珀', rose: '玫红', slate: '石板灰' };
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
let tagEditor = null;
let resetArmed = false;
let resetTimer = null;
let openSiteMenu = null;
let openColorMenu = null;
let tagStatus = { text: '', undo: null };
let tagFocusAfterRender = null;
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

// 非会员/已过期：定制暂停生效，看板恢复默认展示；本地与服务端数据都保留，续期后自动恢复
function clearAppliedPreferences() {
  hidden = { sites: new Set(), providers: new Set(), models: new Set() };
  defaultHealthy = false;
  tags = new Map();
  healthyOnly.checked = false;
  render();
}

function applyPreferencesForMembership() {
  if (membershipIs() === 'active') loadPreferences();
  else clearAppliedPreferences();
}

const saveHidden = () => { storageSet('relayscope-hidden', JSON.stringify({ sites: [...hidden.sites], providers: [...hidden.providers], models: [...hidden.models] })); scheduleCloudSave(); };
const saveDefaultHealthy = () => { storageSet('relayscope-default-healthy', defaultHealthy ? '1' : '0'); scheduleCloudSave(); };
const saveTags = () => { storageSet('relayscope-tags', JSON.stringify(Object.fromEntries([...tags].map(([name, tag]) => [name, { color: tag.color, sites: [...tag.sites] }])))); scheduleCloudSave(); };

function isHiddenCard(card) {
  return hidden.sites.has(card.siteName)
    || hidden.providers.has(card.provider || '未归类')
    || hidden.models.has(card.ruleName || card.rawModelName);
}

function cardTagsOf(siteName) {
  if (!tags.size) return [];
  return [...tags.entries()].filter(([, tag]) => tag.sites.has(siteName)).map(([name]) => name);
}

// ---- 标签数据操作（纯函数，供面板与测试共用，此块到 formatMetric 为止） ----
function tagPickColor(sourceMap, palette) {
  const counts = new Map(palette.map((color) => [color, 0]));
  for (const tag of sourceMap.values()) counts.set(tag.color, (counts.get(tag.color) || 0) + 1);
  return [...counts.entries()].sort((left, right) => left[1] - right[1])[0][0];
}
const tagSnapshot = (sourceMap) => new Map([...sourceMap].map(([name, tag]) => [name, { color: tag.color, sites: new Set(tag.sites) }]));
function tagCreate(sourceMap, name) {
  if (!name) return { error: '请输入标签名称。' };
  if (sourceMap.has(name)) return { error: '已存在同名标签。' };
  sourceMap.set(name, { color: tagPickColor(sourceMap, TAG_COLORS), sites: new Set() });
  return { ok: true };
}
function tagRename(sourceMap, oldName, name) {
  if (!name) return { error: '请输入标签名称。' };
  if (!sourceMap.has(oldName)) return { error: '标签不存在。' };
  if (name !== oldName && sourceMap.has(name)) return { error: '已存在同名标签。' };
  if (name !== oldName) {
    const tag = sourceMap.get(oldName);
    sourceMap.delete(oldName);
    sourceMap.set(name, tag);
  }
  return { ok: true };
}
function tagSetColor(sourceMap, name, color) {
  const tag = sourceMap.get(name);
  if (!tag || !TAG_COLORS.includes(color)) return false;
  tag.color = color;
  return true;
}
function tagSetSite(sourceMap, site, name, attach) {
  const tag = sourceMap.get(name);
  if (!tag) return false;
  if (attach) tag.sites.add(site);
  else tag.sites.delete(site);
  return true;
}
// ---- 标签数据操作结束 ----

// ---- 会员与同步纯函数（供面板与测试共用，此块到 wishPageTemplate 为止） ----
function membershipState(expiresAtMs, nowMs) {
  if (!expiresAtMs) return 'none';
  return expiresAtMs > nowMs ? 'active' : 'expired';
}
function membershipBadge(state) {
  if (state === 'active') return { text: '会员生效中', tone: 'healthy' };
  if (state === 'expired') return { text: '会员已过期', tone: 'muted' };
  return { text: '未开通会员', tone: 'muted' };
}
function preferencesIsEmpty(prefs) {
  if (!prefs) return true;
  const hidden = prefs.hidden || {};
  const hasHidden = (hidden.sites || []).length || (hidden.providers || []).length || (hidden.models || []).length;
  const tagCount = Object.keys(prefs.tags || {}).length;
  return !hasHidden && !tagCount && !prefs.defaultHealthy;
}
// 首次同步合并决策：云端有数据以云端为准；云端为空且本地有数据则把本地上传。
function mergePreferences(local, cloud) {
  const cloudEmpty = preferencesIsEmpty(cloud);
  const localEmpty = preferencesIsEmpty(local);
  if (!cloudEmpty) return { source: 'cloud', upload: false };
  if (!localEmpty) return { source: 'local', upload: true };
  return { source: 'default', upload: false };
}
function wishProgress(pledged, target) {
  if (target == null) return { undecided: true, percent: 0, label: '许愿目标尚未确定' };
  const capped = target > 0 ? Math.min(1, pledged / target) : 1;
  return { undecided: false, percent: Math.round(capped * 100), label: `已许愿 ${pledged} / ${target} LDC` };
}
function wishStatusBadge(status) {
  if (status === 'reached') return { text: '已达成，等待接入', tone: 'healthy' };
  if (status === 'connected') return { text: '已接入', tone: 'accent' };
  return null;
}
function escapeMarkdownHTML(text) {
  return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
// 极简 Markdown：标题/加粗/斜体/行内代码/链接(http s)/列表/引用/分隔线/段落。
// 输入先整体 HTML 转义，再生成白名单标签，天然防注入。
function renderMarkdown(source) {
  const lines = escapeMarkdownHTML(String(source || '')).split(/\r?\n/);
  const inline = (text) => text
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/\*([^*]+)\*/g, '<em>$1</em>')
    .replace(/\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
  const blocks = [];
  let list = null;
  let paragraph = [];
  const flushParagraph = () => {
    if (paragraph.length) { blocks.push(`<p>${inline(paragraph.join('<br>'))}</p>`); paragraph = []; }
  };
  const flushList = () => {
    if (list) { blocks.push(`<${list.tag}>${list.items.map((item) => `<li>${inline(item)}</li>`).join('')}</${list.tag}>`); list = null; }
  };
  for (const raw of lines) {
    const line = raw.trim();
    if (!line) { flushParagraph(); flushList(); continue; }
    const heading = line.match(/^(#{1,4})\s+(.*)$/);
    if (heading) { flushParagraph(); flushList(); blocks.push(`<h${heading[1].length + 2}>${inline(heading[2])}</h${heading[1].length + 2}>`); continue; }
    if (/^(-{3,}|\*{3,})$/.test(line)) { flushParagraph(); flushList(); blocks.push('<hr>'); continue; }
    const unordered = line.match(/^[-*]\s+(.*)$/);
    if (unordered) { flushParagraph(); if (!list || list.tag !== 'ul') { flushList(); list = { tag: 'ul', items: [] }; } list.items.push(unordered[1]); continue; }
    const ordered = line.match(/^\d+[.)]\s+(.*)$/);
    if (ordered) { flushParagraph(); if (!list || list.tag !== 'ol') { flushList(); list = { tag: 'ol', items: [] }; } list.items.push(ordered[1]); continue; }
    const quote = line.match(/^(?:>|&gt;)\s?(.*)$/);
    if (quote) { flushParagraph(); flushList(); blocks.push(`<blockquote>${inline(quote[1])}</blockquote>`); continue; }
    flushList();
    paragraph.push(line);
  }
  flushParagraph();
  flushList();
  return blocks.join('');
}
// ---- 会员与同步纯函数结束 ----

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
    // 让浏览器先绘制首帧再构建大卡片树；不能用 rAF：窗口被遮挡时 rAF 永不触发，
    // 而后续轮询会因 revision 未变提前返回，看板将一直空白。
    await new Promise((resolve) => setTimeout(resolve, 0));
    buildCards();
    render();
  } catch {}
}

function renderAnnouncements() {
  const sectionLabel = document.querySelector('.announcement-section');
  if (sectionLabel) sectionLabel.hidden = !(siteNotice && siteNotice.markdown);
  announcementContent.innerHTML = announcements.length
    ? `<ul class="announcement-list">${announcements.map((item) => `<li><span class="announcement-indicator" aria-hidden="true"></span><div class="announcement-item-body"><div class="announcement-item-heading"><strong>${escapeHTML(item.siteName)}</strong><code>${escapeHTML(item.failureCode)}</code></div><p class="announcement-reason">${escapeHTML(item.reason || '当前采集暂时失败，恢复成功后会自动撤下。')}</p></div></li>`).join('')}</ul>`
    : '<p class="muted announcement-empty">当前所有已启用站点均已恢复采集。</p>';
}

let siteNotice = null;

function renderNotice() {
  if (!noticeContent) return;
  if (siteNotice && siteNotice.markdown) {
    noticeContent.innerHTML = renderMarkdown(siteNotice.markdown);
    noticeContent.hidden = false;
  } else {
    noticeContent.hidden = true;
  }
}

async function loadAnnouncements() {
  try {
    const response = await fetch('/api/v1/public/announcements', { cache: 'no-store' });
    if (!response.ok) return;
    const payload = await response.json();
    siteNotice = payload.notice || null;
    renderNotice();
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
  if (searchFocusKey && !tagFocusAfterRender) {
    const panel = customizeTab === 'display' ? customizeDisplayPanel : customizeTagsPanel;
    const input = panel.querySelector(`[data-pref-search="${searchFocusKey}"]`);
    if (input) {
      input.focus();
      input.setSelectionRange(input.value.length, input.value.length);
    }
  }
}

function setCustomizeTab(tab) {
  // 非会员（含已过期）永远停留在门禁页，禁止通过切换标签渲染出定制内容
  if (membershipIs() !== 'active') { enterCustomize(); return; }
  customizeTab = tab;
  customizeTabDisplay.setAttribute('aria-selected', String(tab === 'display'));
  customizeTabTags.setAttribute('aria-selected', String(tab === 'tags'));
  customizeDisplayPanel.hidden = tab !== 'display';
  customizeTagsPanel.hidden = tab !== 'tags';
  customizeTagsPanel.scrollTop = 0;
  customizeDisplayPanel.scrollTop = 0;
  tagEditor = null;
  openSiteMenu = null;
  openColorMenu = null;
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

function siteTagCount(site) {
  // 站点行角标：该站点挂了多少个标签
  let count = 0;
  for (const tag of tags.values()) if (tag.sites.has(site)) count += 1;
  return count;
}
// 标签行角标：该标签挂了多少个站点
const tagSiteCount = (name) => tags.get(name)?.sites.size || 0;

const taggedSiteCount = () => [...new Set([...tags.values()].flatMap((tag) => [...tag.sites]))].length;

function siteTagChipsHTML(site) {
  return [...tags.entries()].filter(([, tag]) => tag.sites.has(site)).map(([tagName]) => {
    const tag = tags.get(tagName);
    return `<button type="button" class="tag-chip ${tag.color}" data-site-tag-remove="${escapeHTML(JSON.stringify([site, tagName]))}" title="从「${escapeHTML(site)}」移除标签「${escapeHTML(tagName)}」" aria-label="从「${escapeHTML(site)}」移除标签「${escapeHTML(tagName)}」"><i aria-hidden="true"></i><span class="chip-label">${escapeHTML(tagName)}</span><span class="chip-x" aria-hidden="true">✕</span></button>`;
  }).join('');
}

function siteTagMenuHTML(site) {
  const items = [...tags.keys()].map((tagName) => {
    const checked = tags.get(tagName).sites.has(site);
    return `<button type="button" role="menuitemcheckbox" class="tag-menu-item" data-tag-menu-item="${escapeHTML(JSON.stringify([site, tagName]))}" aria-checked="${checked}" tabindex="${checked ? 0 : -1}"><i class="${tags.get(tagName).color}" aria-hidden="true"></i><span class="chip-label">${escapeHTML(tagName)}</span></button>`;
  }).join('');
  return `<div class="site-tag-menu" role="menu" aria-label="为「${escapeHTML(site)}」选择标签"${openSiteMenu === site ? '' : ' hidden'}>${items || '<p class="tag-menu-empty">还没有标签，先在上方新建。</p>'}</div>`;
}

function siteTagRowHTML(name) {
  const isOpen = openSiteMenu === name;
  return `<div class="pref-row site-tag-row" data-site-row="${escapeHTML(name)}"><span class="site-tag-head"><strong>${escapeHTML(name)}</strong><span class="site-tag-count" data-site-tag-count>${siteTagCount(name)} 个标签</span></span><span class="site-tags"><span class="site-tags-chips" data-site-tags>${siteTagChipsHTML(name)}</span><span class="tag-popover"><button type="button" class="tag-add" data-tag-add="${escapeHTML(name)}" aria-haspopup="menu" aria-expanded="${isOpen}" aria-label="为「${escapeHTML(name)}」添加或移除标签" title="添加或移除标签">＋ 添加标签</button>${siteTagMenuHTML(name)}</span></span></div>`;
}

function tagColorMenuHTML(name) {
  const isOpen = openColorMenu === name;
  const options = TAG_COLORS.map((color) => {
    const checked = tags.get(name)?.color === color;
    return `<button type="button" role="menuitemradio" class="tag-color-option" data-tag-color-option="${escapeHTML(name)}" data-color="${color}" aria-checked="${checked}" tabindex="${checked ? 0 : -1}" title="改用${TAG_COLOR_LABELS[color]}"><i class="${color}" aria-hidden="true"></i><span class="chip-label">${TAG_COLOR_LABELS[color]}</span></button>`;
  }).join('');
  return `<div class="tag-color-menu" role="menu" aria-label="「${escapeHTML(name)}」的颜色"${isOpen ? '' : ' hidden'}>${options}</div>`;
}

function tagItemHTML(name) {
  const count = tagSiteCount(name);
  return `<div class="tag-item" data-tag-item="${escapeHTML(name)}"><span class="tag-popover tag-swatch-wrap"><button type="button" class="tag-swatch ${tags.get(name).color}" data-tag-swatch="${escapeHTML(name)}" aria-haspopup="menu" aria-expanded="${openColorMenu === name}" aria-label="更改「${escapeHTML(name)}」的颜色" title="更改颜色"><i aria-hidden="true"></i></button>${tagColorMenuHTML(name)}</span><button type="button" class="tag-rename" data-tag-rename="${escapeHTML(name)}" aria-label="重命名标签「${escapeHTML(name)}」" title="重命名标签"><span class="chip-label">${escapeHTML(name)}</span><svg aria-hidden="true" viewBox="0 0 24 24" focusable="false"><path d="M4 20h4L19.5 8.5a2.1 2.1 0 0 0-3-3L5 17zm11.5-14 3 3" /></svg></button><span class="tag-count" data-tag-count>${count} 个站点</span><button type="button" class="tag-delete" data-tag-delete="${escapeHTML(name)}" aria-label="删除标签「${escapeHTML(name)}」" title="删除标签">删除</button></div>`;
}

function tagEditorHTML() {
  const isCreate = tagEditor.mode === 'create';
  return `<form class="tag-editor" data-tag-form><div class="tag-editor-field"><input data-tag-name-input maxlength="12" value="${isCreate ? '' : escapeHTML(tagEditor.name)}" placeholder="${isCreate ? '输入新标签名称' : '输入新名称'}" aria-label="标签名称" autocomplete="off"><p class="tag-editor-error" data-tag-message role="alert" hidden></p></div><span class="tag-editor-actions"><button class="primary-button" type="submit">${isCreate ? '添加' : '保存'}</button><button class="ghost" type="button" data-tag-cancel>取消</button></span></form>`;
}

function tagStatusHTML() {
  if (!tagStatus.text) return '';
  return `<div class="tag-status" data-tag-status><span class="tag-status-message" role="status">${escapeHTML(tagStatus.text)}</span>${tagStatus.undo ? `<button type="button" class="tag-undo" data-tag-undo>撤销</button>` : ''}</div>`;
}

function refreshSiteTagRow(site) {
  const row = [...customizeTagsPanel.querySelectorAll('[data-site-row]')].find((node) => node.dataset.siteRow === site);
  if (!row) return;
  const slot = row.querySelector('[data-site-tags]');
  if (slot) slot.innerHTML = siteTagChipsHTML(site);
  row.querySelectorAll('[data-tag-menu-item]').forEach((item) => {
    const tagName = JSON.parse(item.dataset.tagMenuItem)[1];
    item.setAttribute('aria-checked', String(Boolean(tags.get(tagName)?.sites.has(site))));
  });
  const count = row.querySelector('[data-site-tag-count]');
  if (count) count.textContent = `${siteTagCount(site)} 个标签`;
  customizeTagsPanel.querySelectorAll('[data-tag-count]').forEach((badge) => {
    const tagName = badge.closest('[data-tag-item]')?.dataset.tagItem;
    if (tagName) badge.textContent = `${tagSiteCount(tagName)} 个站点`;
  });
  const total = customizeTagsPanel.querySelector('[data-tagged-count]');
  if (total) total.textContent = `已标记 ${taggedSiteCount()} 个站点`;
}

function handleTagMenuKeydown(event) {
  const menu = event.currentTarget;
  const items = [...menu.querySelectorAll('[role="menuitemcheckbox"], [role="menuitemradio"]')];
  if (!items.length) return;
  const current = items.indexOf(document.activeElement);
  const focusItem = (next) => {
    event.preventDefault();
    items.forEach((item) => { item.tabIndex = -1; });
    next.tabIndex = 0;
    next.focus();
  };
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    if (current === -1) focusItem(items[0]);
    else focusItem(items[(current + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length]);
    return;
  }
  if (event.key === 'Home' || event.key === 'End') {
    focusItem(event.key === 'Home' ? items[0] : items[items.length - 1]);
    return;
  }
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    if (current >= 0) items[current].click();
    return;
  }
  if (event.key === 'Escape') {
    event.preventDefault();
    const kind = menu.classList.contains('tag-color-menu') ? 'swatch' : 'add';
    const value = kind === 'swatch' ? openColorMenu : openSiteMenu;
    const lookup = kind === 'swatch' ? 'tagSwatch' : 'tagAdd';
    tagFocusAfterRender = () => [...customizeTagsPanel.querySelectorAll(kind === 'swatch' ? '[data-tag-swatch]' : '[data-tag-add]')].find((node) => node.dataset[lookup] === value) || null;
    closeTagMenus();
    return;
  }
  if (event.key === 'Tab') closeTagMenus();
}

function bindTagPanelBehaviors() {
  const editor = customizeTagsPanel.querySelector('[data-tag-form]');
  if (editor) {
    const input = editor.querySelector('[data-tag-name-input]');
    input.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        cancelTagEditor();
      }
    });
    input.addEventListener('blur', (event) => {
      if (event.relatedTarget && editor.contains(event.relatedTarget)) return;
      cancelTagEditor();
    });
  }
  customizeTagsPanel.querySelectorAll('.tag-color-menu:not([hidden]), .site-tag-menu:not([hidden])').forEach((menu) => menu.addEventListener('keydown', handleTagMenuKeydown));
}

function renderCustomizeTags() {
  const editing = tagEditor;
  const tagRows = [...tags.keys()].map((name) =>
    (editing?.mode === 'rename' && editing.name === name) ? tagEditorHTML() : tagItemHTML(name)
  ).join('');
  const createEditor = editing?.mode === 'create' ? tagEditorHTML() : '';
  const tagList = `<div class="tag-list" data-tag-list>${tagRows || (!editing ? '<p class="tag-empty">还没有标签，新建一个来标记常用站点，标签会显示在卡片上。</p>' : '')}<button type="button" class="tag-new" data-tag-new>＋ 新建标签</button>${createEditor}</div>`;
  const taggedSites = new Set();
  for (const tag of tags.values()) for (const site of tag.sites) taggedSites.add(site);
  const allSites = [...new Set([...cards.map((card) => card.siteName), ...taggedSites])].sort((a, b) => a.localeCompare(b, 'zh-CN'));
  const query = customizeSearches.tagSites.trim().toLowerCase();
  const siteRows = allSites.filter((name) => !query || name.toLowerCase().includes(query)).map(siteTagRowHTML).join('');
  customizeTagsPanel.innerHTML = `<section class="pref-section"><div class="pref-head"><h3>我的标签</h3><span class="pref-note">点名称重命名 · 点色块换颜色 · 删除可随时撤销</span></div>${tagStatusHTML()}${tagList}</section><section class="pref-section"><div class="pref-head"><h3>站点标签</h3><span class="pref-count" data-tagged-count>已标记 ${taggedSites.size} 个站点</span></div><label class="pref-search"><span>搜索站点</span><input type="search" data-pref-search="tagSites" value="${escapeHTML(customizeSearches.tagSites)}" placeholder="筛选站点"></label><div class="pref-list" data-pref-list="tagSites">${siteRows || `<p class="pref-empty">${allSites.length ? '没有匹配的站点' : '暂无站点数据。'}</p>`}</div></section>`;
  bindTagPanelBehaviors();
  if (openSiteMenu) {
    const menu = customizeTagsPanel.querySelector('.site-tag-menu:not([hidden])');
    const first = menu
      ? [...menu.querySelectorAll('[data-tag-menu-item]')].find((item) => item.getAttribute('aria-checked') === 'true') || menu.querySelector('[data-tag-menu-item]')
      : null;
    if (first) first.focus();
  } else if (openColorMenu) {
    const menu = customizeTagsPanel.querySelector('.tag-color-menu:not([hidden])');
    const current = menu?.querySelector('[aria-checked="true"]') || menu?.querySelector('[data-tag-color-option]');
    if (current) current.focus();
  }
  if (tagFocusAfterRender) {
    const target = tagFocusAfterRender();
    tagFocusAfterRender = null;
    if (target) target.focus();
  }
}

function closeTagMenus() {
  if (!openSiteMenu && !openColorMenu) return;
  openSiteMenu = null;
  openColorMenu = null;
  renderCustomize();
}

function cancelTagEditor() {
  if (!tagEditor) return;
  tagEditor = null;
  renderCustomize();
}

function handleCustomizeClick(event) {
  const target = event.target.closest('[data-pref-reset],[data-pref-reset-all],[data-tag-new],[data-tag-cancel],[data-tag-delete],[data-tag-rename],[data-tag-swatch],[data-tag-color-option],[data-tag-add],[data-site-tag-remove],[data-tag-menu-item],[data-tag-undo]');
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
      resetTimer = window.setTimeout(() => { resetArmed = false; if (!customizePage.hidden) renderCustomize(); }, 2200);
      renderCustomize();
    }
    return;
  }
  if (target.matches('[data-tag-new]')) {
    tagEditor = { mode: 'create' };
    renderCustomize();
    focusTagInput();
    return;
  }
  if (target.matches('[data-tag-cancel]')) {
    cancelTagEditor();
    return;
  }
  if (target.matches('[data-tag-rename]')) {
    tagEditor = { mode: 'rename', name: target.dataset.tagRename };
    renderCustomize();
    focusTagInput();
    return;
  }
  if (target.matches('[data-tag-delete]')) {
    const name = target.dataset.tagDelete;
    tagStatus = { text: `已删除标签「${name}」。`, undo: tagSnapshot(tags) };
    tags.delete(name);
    saveTags();
    render();
    tagFocusAfterRender = () => customizeTagsPanel.querySelector('[data-tag-undo]');
    renderCustomize();
    return;
  }
  if (target.matches('[data-tag-undo]')) {
    if (!tagStatus.undo) return;
    tags = tagStatus.undo;
    tagStatus = { text: '', undo: null };
    saveTags();
    render();
    renderCustomize();
    return;
  }
  if (target.matches('[data-tag-swatch]')) {
    const name = target.dataset.tagSwatch;
    if (openColorMenu === name) {
      closeTagMenus();
      return;
    }
    openColorMenu = name;
    openSiteMenu = null;
    renderCustomize();
    return;
  }
  if (target.matches('[data-tag-color-option]')) {
    const name = target.dataset.tagColorOption;
    const color = target.dataset.color;
    const tag = tags.get(name);
    if (!tag) return;
    if (tag.color !== color) {
      tagStatus = { text: `「${name}」已改为${TAG_COLOR_LABELS[color] || color}色。`, undo: tagSnapshot(tags) };
      tagSetColor(tags, name, color);
      saveTags();
      render();
    }
    tagFocusAfterRender = () => [...customizeTagsPanel.querySelectorAll('[data-tag-swatch]')].find((node) => node.dataset.tagSwatch === name) || null;
    closeTagMenus();
    return;
  }
  if (target.matches('[data-tag-add]')) {
    const site = target.dataset.tagAdd;
    if (openSiteMenu === site) {
      closeTagMenus();
      return;
    }
    openSiteMenu = site;
    openColorMenu = null;
    renderCustomize();
    return;
  }
  if (target.matches('[data-tag-menu-item]')) {
    const [site, tagName] = JSON.parse(target.dataset.tagMenuItem);
    const attach = !tags.get(tagName)?.sites.has(site);
    if (tagSetSite(tags, site, tagName, attach)) {
      saveTags();
      render();
      refreshSiteTagRow(site);
    }
    return;
  }
  if (target.matches('[data-site-tag-remove]')) {
    const [site, tagName] = JSON.parse(target.dataset.siteTagRemove);
    if (tagSetSite(tags, site, tagName, false)) {
      saveTags();
      render();
      refreshSiteTagRow(site);
      const addButton = [...customizeTagsPanel.querySelectorAll('[data-tag-add]')].find((node) => node.dataset.tagAdd === site);
      if (addButton) addButton.focus();
    }
    return;
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
  if (!form || !tagEditor) return;
  event.preventDefault();
  const message = form.querySelector('[data-tag-message]');
  const input = form.querySelector('[data-tag-name-input]');
  const name = input.value.trim();
  message.hidden = true;
  const fail = (text) => { message.textContent = text; message.hidden = false; input.focus(); };
  const snapshot = tagSnapshot(tags);
  if (tagEditor.mode === 'create') {
    const result = tagCreate(tags, name);
    if (result.error) return fail(result.error);
    tagStatus = { text: `已创建标签「${name}」。`, undo: snapshot };
  } else if (tagEditor.mode === 'rename') {
    if (name === tagEditor.name) {
      tagEditor = null;
      renderCustomize();
      return;
    }
    const result = tagRename(tags, tagEditor.name, name);
    if (result.error) return fail(result.error);
    tagStatus = { text: `已重命名为「${name}」。`, undo: snapshot };
  } else {
    return;
  }
  tagEditor = null;
  saveTags();
  render();
  renderCustomize();
}

customizePage.addEventListener('click', handleCustomizeClick);
customizePage.addEventListener('change', handleCustomizeChange);
customizePage.addEventListener('input', handleCustomizeInput);
customizePage.addEventListener('submit', handleCustomizeSubmit);
customizePage.addEventListener('focusin', (event) => {
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
    window.location.hash = '#customize';
    return;
  }
  if ((openSiteMenu || openColorMenu) && !event.target.closest('.tag-popover')) closeTagMenus();
});

// ===== 账号 / 会员 / 定制云同步 / 许愿池 =====
let currentUser = null;
let membership = null;
let cloudSynced = false;
let cloudSaveTimer = null;
let siteSettings = { membershipMonthlyPriceLdc: 15, wishDefaultTargetLdc: 30 };
let pledgeCreditAvailable = 0;
let wishItems = [];
let pledgeTargetId = null;

const formatDate = (value) => value ? new Date(value).toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' }) : '—';
const collectLocalPreferences = () => ({
  hidden: { sites: [...hidden.sites], providers: [...hidden.providers], models: [...hidden.models] },
  defaultHealthy,
  tags: Object.fromEntries([...tags].map(([name, tag]) => [name, { color: tag.color, sites: [...tag.sites] }]))
});

function showToast(message, tone = '') {
  if (!toastRegion) return;
  const toast = document.createElement('div');
  toast.className = `toast${tone ? ` ${tone}` : ''}`;
  toast.textContent = message;
  toastRegion.appendChild(toast);
  requestAnimationFrame(() => toast.classList.add('visible'));
  setTimeout(() => {
    toast.classList.remove('visible');
    setTimeout(() => toast.remove(), 250);
  }, 3200);
}

const membershipIs = () => membership ? membershipState(membership.expiresAt ? new Date(membership.expiresAt).getTime() : 0, Date.now()) : 'none';

function renderUserArea() {
  if (!userAction) return;
  const state = membershipIs();
  const badge = membershipBadge(state);
  if (currentUser) {
    userAction.innerHTML = `<span class="user-avatar user-avatar-chip" aria-hidden="true">${escapeHTML((currentUser.username || '?').slice(0, 1).toUpperCase())}</span><span class="user-action-label">${escapeHTML(currentUser.username)}</span>`;
    userAction.setAttribute('aria-label', `账号 · ${currentUser.username}`);
    if (userMenuName) {
      userMenuName.textContent = currentUser.name || currentUser.username;
      userMenuMembership.textContent = `${badge.text}${state === 'active' && membership?.expiresAt ? ' · ' + formatDate(membership.expiresAt) + '到期' : ''}`;
      userMenuAvatar.textContent = (currentUser.username || '?').slice(0, 1).toUpperCase();
    }
  } else {
    userAction.innerHTML = '<span class="user-action-label">登录</span>';
    userAction.setAttribute('aria-label', '登录');
  }
}

async function loadUser() {
  try {
    const response = await fetch('/api/v1/auth/me', { cache: 'no-store' });
    if (!response.ok) {
      currentUser = null;
      membership = null;
    } else {
      const identity = await response.json();
      currentUser = identity.authenticated ? identity.user : null;
      membership = identity.membership || null;
    }
  } catch { /* 网络失败：保留现状 */ }
  renderUserArea();
  updateCustomizeSubtitle();
  renderWishBanner();
  applyPreferencesForMembership();
  if (currentUser) await syncPreferencesFromCloud();
  if (!wishPage.hidden) loadWishes();
}

// ---- 定制云同步：本地即时生效，800ms 防抖上云；首次合并云端优先 ----
function scheduleCloudSave() {
  if (!cloudSynced || !currentUser) return;
  clearTimeout(cloudSaveTimer);
  cloudSaveTimer = setTimeout(pushPreferencesToCloud, 800);
}

async function pushPreferencesToCloud() {
  if (!cloudSynced || !currentUser) return;
  try {
    const response = await fetch('/api/v1/me/preferences', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(collectLocalPreferences()) });
    if (response.status === 403) {
      cloudSynced = false;
      membership = { expiresAt: membership?.expiresAt || null, active: false };
      renderUserArea();
      updateCustomizeSubtitle();
      if (!customizePage.hidden) enterCustomize();
      clearAppliedPreferences();
      showToast('会员已过期，定制已暂停生效，数据已保留');
    }
  } catch { /* 离线：下次修改再试 */ }
}

async function syncPreferencesFromCloud() {
  if (membershipIs() !== 'active') return;
  try {
    const response = await fetch('/api/v1/me/preferences', { cache: 'no-store' });
    if (!response.ok) return;
    const cloud = await response.json();
    const decision = mergePreferences(collectLocalPreferences(), cloud);
    if (decision.source === 'cloud') {
      const cloudHidden = cloud.hidden || {};
      hidden = { sites: new Set(cloudHidden.sites || []), providers: new Set(cloudHidden.providers || []), models: new Set(cloudHidden.models || []) };
      defaultHealthy = !!cloud.defaultHealthy;
      tags = new Map();
      for (const [name, tag] of Object.entries(cloud.tags || {})) {
        if (typeof tag !== 'object' || tag === null) continue;
        tags.set(name, { color: TAG_COLORS.includes(tag.color) ? tag.color : 'mint', sites: new Set(Array.isArray(tag.sites) ? tag.sites : []) });
      }
      healthyOnly.checked = defaultHealthy;
      // cloudSynced 尚未置真，镜像到 localStorage 不会触发回环上传
      storageSet('relayscope-hidden', JSON.stringify({ sites: [...hidden.sites], providers: [...hidden.providers], models: [...hidden.models] }));
      storageSet('relayscope-default-healthy', defaultHealthy ? '1' : '0');
      storageSet('relayscope-tags', JSON.stringify(Object.fromEntries([...tags].map(([name, tag]) => [name, { color: tag.color, sites: [...tag.sites] }]))));
      render();
      if (!customizePage.hidden) renderCustomize();
      showToast('已同步你的定制设置');
    }
    cloudSynced = true;
    updateCustomizeSubtitle();
    if (decision.upload) pushPreferencesToCloud();
  } catch { /* 离线：保持本地 */ }
}

// ---- 定制页门禁 ----
function updateCustomizeSubtitle() {
  if (!customizeSubtitle) return;
  if (membershipIs() === 'active') customizeSubtitle.textContent = cloudSynced ? '设置已自动同步到你的账号，换设备也不丢。' : '设置将自动同步到你的账号。';
  else customizeSubtitle.textContent = '会员有效期内定制才会生效；到期后暂停，数据保留，续期后自动恢复。';
}

function enterCustomize() {
  const state = membershipIs();
  tagEditor = null;
  openSiteMenu = null;
  openColorMenu = null;
  if (customizeNav) customizeNav.hidden = state !== 'active';
  customizeTagsPanel.hidden = state !== 'active';
  if (state === 'active') {
    renderCustomize();
  } else {
    renderCustomizeGate(!currentUser);
  }
  updateCustomizeSubtitle();
}

function renderCustomizeGate(loggedOut) {
  customizeDisplayPanel.innerHTML = `<section class="pref-section customize-gate"><span class="gate-mark" aria-hidden="true">✦</span><h3>${loggedOut ? '登录后使用定制' : '定制需要有效会员'}</h3><p class="muted">${loggedOut ? '定制是会员功能：登录 LINUX DO 账号并开通会员后，可以屏蔽站点与模型、管理标签，设置自动云端同步。' : '会员到期后定制已暂停生效（看板恢复默认展示），你的设置仍保留在服务器，续期后自动恢复。'}</p><p class="gate-pitch">成为会员：<b>${siteSettings.membershipMonthlyPriceLdc || 15} LDC / 月</b><br>每月额外获赠 <b>${siteSettings.wishFreeCreditLdc || 10} LDC</b> <a class="gate-wish-link" href="#wishes">许愿</a>额度</p><div class="gate-actions">${loggedOut ? '<button type="button" class="primary-button" data-gate-login>登录 LINUX DO</button>' : '<button type="button" class="primary-button" data-gate-redeem>兑换会员</button><button type="button" class="ghost" data-gate-recharge>LDC 直充</button>'}</div></section>`;
}

// ---- 账号菜单 ----
userAction.addEventListener('click', () => {
  if (!currentUser) {
    window.location.assign('/api/v1/auth/linuxdo');
    return;
  }
  const willOpen = userMenu.hidden;
  userMenu.hidden = !willOpen;
  userAction.setAttribute('aria-expanded', String(willOpen));
});
document.addEventListener('click', (event) => {
  if (userMenu && !userMenu.hidden && !event.target.closest('#user-menu') && !event.target.closest('#user-action')) {
    userMenu.hidden = true;
    userAction.setAttribute('aria-expanded', 'false');
  }
});
document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape' && userMenu && !userMenu.hidden) {
    userMenu.hidden = true;
    userAction.setAttribute('aria-expanded', 'false');
  }
});
userMenu.addEventListener('click', async (event) => {
  const item = event.target.closest('[data-user-menu]');
  if (!item) return;
  userMenu.hidden = true;
  userAction.setAttribute('aria-expanded', 'false');
  const action = item.dataset.userMenu;
  if (action === 'redeem') {
    redeemCodeInput.value = '';
    redeemMessage.textContent = '';
    redeemMessage.dataset.state = '';
    redeemDialog.showModal();
  } else if (action === 'recharge') {
    openRecharge();
  } else if (action === 'feedback') {
    feedbackMessage.textContent = '';
    feedbackMessage.dataset.state = '';
    feedbackDialog.showModal();
  } else if (action === 'logout') {
    try { await fetch('/api/v1/auth/logout', { method: 'POST' }); } catch { /* 忽略网络错误 */ }
    currentUser = null;
    membership = null;
    cloudSynced = false;
    renderUserArea();
    updateCustomizeSubtitle();
    showToast('已退出登录');
  }
});

// ---- 兑换码 ----
redeemClose.addEventListener('click', () => redeemDialog.close());
redeemDialog.addEventListener('click', (event) => {
  if (event.target === redeemDialog) {
    const bounds = redeemDialog.getBoundingClientRect();
    if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) redeemDialog.close();
  }
});
document.querySelector('#redeem-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const button = event.currentTarget.querySelector('button[type="submit"]');
  if (button.disabled) return;
  const code = redeemCodeInput.value.trim();
  redeemMessage.dataset.state = '';
  if (!code) {
    redeemMessage.textContent = '请输入兑换码。';
    return;
  }
  button.disabled = true;
  button.textContent = '兑换中…';
  try {
    const response = await fetch('/api/v1/redeem', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code }), signal: AbortSignal.timeout(20000) });
    const payload = await response.json().catch(() => ({}));
    if (response.ok) {
      membership = payload.membership || membership;
      renderUserArea();
      redeemMessage.dataset.state = 'success';
      redeemMessage.textContent = `兑换成功，会员有效期至 ${formatDate(membership?.expiresAt)}。`;
      redeemCodeInput.value = '';
    } else {
      redeemMessage.dataset.state = 'error';
      redeemMessage.textContent = payload.error || '兑换失败，请核对兑换码。';
    }
  } catch {
    redeemMessage.dataset.state = 'error';
    redeemMessage.textContent = '连接失败，请重试。';
  } finally {
    button.disabled = false;
    button.textContent = '兑换';
  }
});

// ---- LDC 直充会员 ----
function updateRechargePrice() {
  const price = siteSettings.membershipMonthlyPriceLdc || 15;
  rechargePrice.textContent = `需支付 ${price} LDC（1 个月）`;
}
function openRecharge() {
  updateRechargePrice();
  rechargeMessage.textContent = '';
  rechargeMessage.dataset.state = '';
  rechargeDialog.showModal();
}
rechargeClose.addEventListener('click', () => rechargeDialog.close());
rechargeDialog.addEventListener('click', (event) => {
  if (event.target === rechargeDialog) {
    const bounds = rechargeDialog.getBoundingClientRect();
    if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) rechargeDialog.close();
  }
});
document.querySelector('#recharge-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const button = event.currentTarget.querySelector('button[type="submit"]');
  if (button.disabled) return;
  button.disabled = true;
  button.textContent = '创建订单…';
  rechargeMessage.dataset.state = '';
  try {
    const response = await fetch('/api/v1/membership/recharge', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}', signal: AbortSignal.timeout(20000) });
    const payload = await response.json().catch(() => ({}));
    if (response.ok && payload.payUrl) {
      rechargeDialog.close();
      window.location.assign(payload.payUrl);
    } else {
      rechargeMessage.dataset.state = 'error';
      rechargeMessage.textContent = payload.error || '下单失败，请稍后再试。';
    }
  } catch {
    rechargeMessage.dataset.state = 'error';
    rechargeMessage.textContent = '连接失败，请重试。';
  } finally {
    button.disabled = false;
    button.textContent = '去支付';
  }
});

// ---- 定制门禁里的入口按钮 ----
customizePage.addEventListener('click', (event) => {
  if (event.target.closest('[data-gate-login]')) {
    window.location.assign('/api/v1/auth/linuxdo');
    return;
  }
  if (event.target.closest('[data-gate-redeem]')) {
    redeemCodeInput.value = '';
    redeemMessage.textContent = '';
    redeemDialog.showModal();
    return;
  }
  if (event.target.closest('[data-gate-recharge]')) {
    openRecharge();
  }
});

// ---- 许愿池页面 ----
function applyRoute() {
  const hash = window.location.hash;
  const wishView = hash === '#wishes';
  const customizeView = hash === '#customize';
  const boardView = !wishView && !customizeView;
  wishPage.hidden = !wishView;
  customizePage.hidden = !customizeView;
  document.querySelector('.toolbar').hidden = !boardView;
  summaryElement.hidden = !boardView;
  document.querySelector('.filter-layout').hidden = !boardView;
  const navTargets = { board: document.querySelector('[data-nav-board]'), wishes: document.querySelector('[data-nav-wishes]'), customize: document.querySelector('[data-nav-customize]') };
  const active = customizeView ? 'customize' : wishView ? 'wishes' : 'board';
  for (const [name, link] of Object.entries(navTargets)) {
    if (!link) continue;
    if (name === active) link.setAttribute('aria-current', 'page');
    else link.removeAttribute('aria-current');
  }
  if (wishView) loadWishes();
  if (customizeView) enterCustomize();
}
window.addEventListener('hashchange', applyRoute);

async function loadWishes() {
  try {
    const response = await fetch('/api/v1/wishes', { cache: 'no-store' });
    if (!response.ok) return;
    const payload = await response.json();
    wishItems = Array.isArray(payload.wishes) ? payload.wishes : [];
    renderWishes();
  } catch {
    wishList.innerHTML = '<p class="muted">许愿池加载失败，请稍后刷新重试。</p>';
  }
}

function wishStats(items) {
  const open = items.filter((wish) => wish.status === 'open').length;
  const reached = items.filter((wish) => wish.status === 'reached' || wish.status === 'connected').length;
  const totalLdc = items.reduce((sum, wish) => sum + (wish.pledgedLdc || 0), 0);
  const pledgers = items.reduce((sum, wish) => sum + (wish.pledgers || 0), 0);
  return { open, reached, totalLdc, pledgers };
}

function renderWishBanner() {
  if (!wishBanner) return;
  const member = membershipIs() === 'active';
  wishBanner.hidden = member;
  if (member) return;
  wishBanner.innerHTML = `花 <b>${siteSettings.membershipMonthlyPriceLdc || 15} LDC</b> 开通会员，每月可获赠 <b>${siteSettings.wishFreeCreditLdc || 10} LDC</b> 许愿额度，并获得 <b>定制功能 &amp; 云端同步</b> 权限<a class="wish-banner-cta" href="#customize">开通会员 →</a>`;
}

function renderWishes() {
  renderWishBanner();
  const stats = wishStats(wishItems);
  const statsHTML = `<div class="wish-stats" role="group" aria-label="许愿池统计">
      <div class="wish-stat"><strong>${stats.open}</strong><span>正在许愿</span></div>
      <div class="wish-stat"><strong>${stats.totalLdc}</strong><span>累计 LDC</span></div>
      <div class="wish-stat"><strong>${stats.pledgers}</strong><span>人次参与</span></div>
      <div class="wish-stat accent"><strong>${stats.reached}</strong><span>已达成</span></div>
    </div>`;
  if (!wishItems.length) {
    wishList.innerHTML = statsHTML + '<div class="empty wish-empty"><span class="wish-empty-mark" aria-hidden="true">✦</span><h1>许愿池还是空的</h1><p>第一个被许愿的站点最有可能被接入。许下你在用的中转站，攒够 LDC 站长就会安排接入。</p></div>';
    return;
  }
  wishList.innerHTML = statsHTML + wishItems.map((wish, index) => {
    const progress = wishProgress(wish.pledgedLdc, wish.targetLdc);
    const statusBadge = wishStatusBadge(wish.status);
    const canPledge = wish.status === 'open' && currentUser;
    const remaining = wish.targetLdc != null && wish.status === 'open' ? Math.max(0, wish.targetLdc - wish.pledgedLdc) : null;
    const remainingNote = remaining === null ? '' : remaining > 0 ? `<span class="wish-remaining">还差 <b>${remaining}</b> LDC 达成</span>` : '<span class="wish-remaining done">目标已达成</span>';
    return `<article class="wish-card" data-wish-id="${wish.id}" style="--stagger-index:${Math.min(index, 8)}">
      <div class="wish-card-head">
        <div class="wish-card-title"><strong class="wish-name">${escapeHTML(wish.name)}</strong><a class="wish-domain" href="${escapeHTML(wish.url)}" target="_blank" rel="noopener noreferrer">${escapeHTML(wish.domain)}<span aria-hidden="true"> ↗</span></a></div>
        <div class="wish-badges">${wish.inviteRequired ? '<span class="wish-badge invite">需邀请码</span>' : ''}${statusBadge ? `<span class="wish-badge ${statusBadge.tone}">${statusBadge.text}</span>` : ''}</div>
      </div>
      <div class="wish-progress-row">
        <div class="wish-progress${progress.undecided ? ' undecided' : ''}" role="progressbar" aria-valuemin="0" aria-valuemax="100"${progress.undecided ? '' : ` aria-valuenow="${progress.percent}"`} aria-label="${escapeHTML(wish.name)} 许愿进度"><i style="transform: scaleX(${progress.percent / 100})"></i></div>
        <span class="wish-progress-label${progress.undecided ? ' undecided' : ''}">${progress.undecided ? escapeHTML(progress.label) : `${progress.percent}%`}</span>
      </div>
      <div class="wish-card-foot">
        <span class="muted">${wish.pledgers} 人许愿${wish.myPledgedLdc ? ` · 我出了 ${wish.myPledgedLdc} LDC` : ''}${remainingNote ? ` · ${remainingNote}` : ''}</span>
        ${canPledge ? `<button type="button" class="ghost wish-pledge" data-wish-pledge="${wish.id}">我来许愿</button>` : (wish.status === 'open' && !currentUser ? '<span class="muted">登录后可助力</span>' : '')}
      </div>
    </article>`;
  }).join('');
}

wishNewButton.addEventListener('click', () => {
  if (!currentUser) {
    showToast('请先登录后再许愿');
    return;
  }
  wishNameInput.value = '';
  wishUrlInput.value = '';
  wishInviteInput.checked = false;
  wishFormMessage.textContent = '';
  wishFormMessage.dataset.state = '';
  wishFormDialog.showModal();
});
wishFormClose.addEventListener('click', () => wishFormDialog.close());
document.querySelector('#wish-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const button = event.currentTarget.querySelector('button[type="submit"]');
  if (button.disabled) return;
  const name = wishNameInput.value.trim();
  const url = wishUrlInput.value.trim();
  wishFormMessage.dataset.state = '';
  if (!name || !url) {
    wishFormMessage.textContent = '请填写网站名和访问网址。';
    return;
  }
  button.disabled = true;
  button.textContent = '提交中…';
  try {
    const response = await fetch('/api/v1/wishes', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name, url, inviteRequired: wishInviteInput.checked }), signal: AbortSignal.timeout(20000) });
    const payload = await response.json().catch(() => ({}));
    if (response.ok) {
      wishFormDialog.close();
      showToast(payload.wish?.inviteRequired ? '许愿成功，等待站长在后台定价' : '许愿成功，目标 30 LDC，快邀请朋友来助力吧', 'success');
      await loadWishes();
    } else {
      wishFormMessage.dataset.state = 'error';
      wishFormMessage.textContent = payload.error || '提交失败，请稍后再试。';
    }
  } catch {
    wishFormMessage.dataset.state = 'error';
    wishFormMessage.textContent = '连接失败，请重试。';
  } finally {
    button.disabled = false;
    button.textContent = '提交许愿';
  }
});

// ---- 助力（LDC 支付） ----
wishList.addEventListener('click', (event) => {
  const button = event.target.closest('[data-wish-pledge]');
  if (!button) return;
  const wish = wishItems.find((item) => String(item.id) === button.dataset.wishPledge);
  if (!wish) return;
  pledgeTargetId = wish.id;
  pledgeTitle.textContent = `为「${wish.name}」许愿`;
  pledgeSubtitle.textContent = wish.targetLdc == null ? '该站点目标尚未确定，你的助力会累计到进度里。' : `当前进度 ${wish.pledgedLdc} / ${wish.targetLdc} LDC`;
  pledgeAmountInput.value = 10;
  pledgeMessage.textContent = '';
  pledgeMessage.dataset.state = '';
  pledgeCreditAvailable = 0;
  pledgeCredit.hidden = true;
  pledgeDialog.showModal();
  fetch('/api/v1/me/wish-credit', { cache: 'no-store' })
    .then((response) => response.ok ? response.json() : null)
    .then((payload) => {
      if (!payload || !pledgeDialog.open) return;
      if (payload.eligible && payload.available > 0) {
        pledgeCreditAvailable = payload.available;
        pledgeCredit.hidden = false;
        pledgeCredit.textContent = `本月免费许愿额度：可用 ${payload.available} LDC，助力时优先抵扣`;
      } else if (payload.eligible) {
        pledgeCredit.hidden = false;
        pledgeCredit.textContent = '本月免费许愿额度已用完';
      }
    })
    .catch(() => {});
});
pledgeClose.addEventListener('click', () => pledgeDialog.close());
document.querySelectorAll('[data-pledge-amount]').forEach((chip) => chip.addEventListener('click', () => {
  pledgeAmountInput.value = chip.dataset.pledgeAmount;
}));
document.querySelector('#pledge-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const button = event.currentTarget.querySelector('button[type="submit"]');
  if (button.disabled || !pledgeTargetId) return;
  const amount = Math.max(1, Math.min(10000, parseInt(pledgeAmountInput.value, 10) || 0));
  button.disabled = true;
  button.textContent = '创建订单…';
  pledgeMessage.dataset.state = '';
  try {
    const response = await fetch(`/api/v1/wishes/${pledgeTargetId}/pledge`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ amountLdc: amount }), signal: AbortSignal.timeout(20000) });
    const payload = await response.json().catch(() => ({}));
    if (response.ok && payload.payUrl) {
      pledgeDialog.close();
      if (payload.creditUsed > 0) showToast(`已使用 ${payload.creditUsed} LDC 免费额度，剩余 ${payload.amountLdc - payload.creditUsed} LDC 请完成支付`, 'success');
      window.location.assign(payload.payUrl);
    } else if (response.ok && payload.creditUsed > 0) {
      pledgeDialog.close();
      showToast(`已用免费额度助力 ${payload.creditUsed} LDC，感谢支持！`, 'success');
      await loadWishes();
    } else {
      pledgeMessage.dataset.state = 'error';
      pledgeMessage.textContent = payload.error || '下单失败，请稍后再试。';
    }
  } catch {
    pledgeMessage.dataset.state = 'error';
    pledgeMessage.textContent = '连接失败，请重试。';
  } finally {
    button.disabled = false;
    button.textContent = '去支付';
  }
});

// ---- 支付回跳确认：轮询订单状态直到落账 ----
function readPaidOrderNo() {
  const direct = new URLSearchParams(window.location.search).get('paid');
  if (direct) return direct;
  const hashQuery = window.location.hash.split('?')[1];
  return hashQuery ? new URLSearchParams(hashQuery).get('paid') : null;
}

function pollOrderStatus(orderNo, attempt = 0) {
  if (attempt > 10) {
    showToast('支付确认超时，如已完成支付请稍后刷新页面');
    return;
  }
  fetch(`/api/v1/payment/orders/${encodeURIComponent(orderNo)}`, { cache: 'no-store' })
    .then((response) => response.ok ? response.json() : null)
    .then((payload) => {
      if (!payload || !payload.order) return;
      if (payload.order.status === 'paid') {
        if (payload.membership) {
          membership = payload.membership;
          renderUserArea();
          updateCustomizeSubtitle();
        }
        showToast(payload.order.kind === 'membership' ? '支付成功，会员已生效' : '支付成功，感谢助力！', 'success');
        if (!wishPage.hidden) loadWishes();
        return;
      }
      if (payload.order.status === 'cancelled') {
        showToast('订单已过期，如未完成支付可重新发起');
        return;
      }
      setTimeout(() => pollOrderStatus(orderNo, attempt + 1), 3000);
    })
    .catch(() => setTimeout(() => pollOrderStatus(orderNo, attempt + 1), 3000));
}

async function loadSiteSettings() {
  try {
    const response = await fetch('/api/v1/meta', { cache: 'no-store' });
    if (!response.ok) return;
    const meta = await response.json();
    siteSettings.membershipMonthlyPriceLdc = parseInt(meta.membershipMonthlyPriceLdc, 10) || 15;
    siteSettings.wishDefaultTargetLdc = parseInt(meta.wishDefaultTargetLdc, 10) || 30;
  } catch { /* 用默认值 */ }
}

// ---- 意见反馈 ----
feedbackClose.addEventListener('click', () => feedbackDialog.close());
feedbackDialog.addEventListener('click', (event) => {
  if (event.target !== feedbackDialog) return;
  const bounds = feedbackDialog.getBoundingClientRect();
  if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) feedbackDialog.close();
});
document.querySelector('#feedback-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const button = event.currentTarget.querySelector('button[type="submit"]');
  if (button.disabled) return;
  const content = document.querySelector('#feedback-content').value.trim();
  feedbackMessage.dataset.state = 'error';
  if (!content) { feedbackMessage.textContent = '请填写反馈内容。'; return; }
  button.disabled = true;
  button.textContent = '正在提交…';
  feedbackMessage.textContent = '';
  try {
    const response = await fetch('/api/v1/feedback', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ content }), signal: AbortSignal.timeout(20000) });
    if (response.ok) {
      document.querySelector('#feedback-content').value = '';
      feedbackMessage.dataset.state = 'success';
      feedbackMessage.textContent = '反馈已提交。';
    } else if (response.status === 401) {
      feedbackDialog.close();
      currentUser = null;
      await loadUser();
    } else {
      feedbackMessage.textContent = '提交失败，请稍后再试。';
    }
  } catch {
    feedbackMessage.textContent = '连接失败，内容已保留，请重试。';
  } finally {
    button.disabled = false;
    button.textContent = '提交反馈';
  }
});

initializeTheme();
loadRows();
loadSiteSettings();
// 登录态就绪后再做首次路由渲染，否则 #customize 刷新会因 currentUser 尚为空而误显登录门禁
loadUser().finally(applyRoute);
{
  const paidOrder = readPaidOrderNo();
  if (paidOrder) pollOrderStatus(paidOrder);
}
setInterval(loadRows, 60000);
