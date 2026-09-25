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
const detailSiteFilter = document.querySelector('#detail-site-filter');
const healthyOnly = document.querySelector('#healthy-only');
const filterPanel = document.querySelector('#filter-panel');
const clearFilters = document.querySelector('#clear-filters');
const themeToggle = document.querySelector('#theme-toggle');
const customizePage = document.querySelector('#customize-page');
const customizeTabDisplay = document.querySelector('#customize-tab-display');
const customizeTabTags = document.querySelector('#customize-tab-tags');
const customizeDisplayPanel = document.querySelector('#customize-display');
const customizeSettingsPanel = document.querySelector('#customize-display-settings');
const customizeSitesPanel = document.querySelector('#customize-sites');
const customizeProvidersPanel = document.querySelector('#customize-providers');
const customizeModelsPanel = document.querySelector('#customize-models');
const customizeTagsPanel = document.querySelector('#customize-tags');
const customizeNotifyPanel = document.querySelector('#customize-notify');
const boardShell = document.querySelector('#board-shell');
const ncPanel = document.querySelector('#nc-sidebar');
const announcementAction = document.querySelector('#announcement-action');
const ncCloseBtn = document.querySelector('#nc-close-btn');
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
const sortTrigger = document.querySelector('#sort-mode');
const sortDropdown = document.querySelector('#sort-dropdown');
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
let sitesWithAnnouncements = new Set(); // siteIds that have recent announcements

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
let openSiteMenu = null;
let openColorMenu = null;
let tagStatus = { text: '', undo: null };
let tagFocusAfterRender = null;
let customizeSearches = { sites: '', providers: '', models: '', tagSites: '' };
let searchFocusKey = null;
let sortMode = { model: 'default', site: 'default' };

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
const activeFilterDefinitions = () => (tags.size ? [tagDefinition, ...filterDefinitions] : filterDefinitions);

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
  try {
    const raw = JSON.parse(storageGet('relayscope-sorting') || '{}');
    const validModels = new Set(SORT_OPTIONS.model.map((o) => o.value));
    const validSites = new Set(SORT_OPTIONS.site.map((o) => o.value));
    sortMode = {
      model: validModels.has(raw.model) ? raw.model : 'default',
      site: validSites.has(raw.site) ? raw.site : 'default',
    };
  } catch { sortMode = { model: 'default', site: 'default' }; }
}

// 非会员/已过期：定制暂停生效，看板恢复默认展示；本地与服务端数据都保留，续期后自动恢复
function clearAppliedPreferences() {
  hidden = { sites: new Set(), providers: new Set(), models: new Set() };
  defaultHealthy = false;
  tags = new Map();
  healthyOnly.checked = false;
  sortMode = { model: 'default', site: 'default' };
  renderSortSelect();
  render();
}

function applyPreferencesForMembership() {
  if (membershipIs() === 'active') loadPreferences();
  else clearAppliedPreferences();
}

const saveHidden = () => { storageSet('relayscope-hidden', JSON.stringify({ sites: [...hidden.sites], providers: [...hidden.providers], models: [...hidden.models] })); scheduleCloudSave(); };
const saveDefaultHealthy = () => { storageSet('relayscope-default-healthy', defaultHealthy ? '1' : '0'); scheduleCloudSave(); };
const saveTags = () => { storageSet('relayscope-tags', JSON.stringify(Object.fromEntries([...tags].map(([name, tag]) => [name, { color: tag.color, sites: [...tag.sites] }])))); scheduleCloudSave(); };
const saveSorting = () => { storageSet('relayscope-sorting', JSON.stringify(sortMode)); scheduleCloudSave(); };

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
  const sorting = prefs.sorting;
  const hasSorting = sorting && (sorting.model !== 'default' || sorting.site !== 'default');
  return !hasHidden && !tagCount && !prefs.defaultHealthy && !hasSorting;
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
  const capped = target > 0 ? Math.min(1, pledged / target) : 0;
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

// ---- 排序纯函数（供面板与测试共用，此块到 formatMetric 为止） ----

const SORT_OPTIONS = {
  model: [
    { value: 'smart', label: '✦ 智能排序', gold: true },
    { value: 'default', label: '默认排序' },
    { value: 'price', label: '按价格' },
    { value: 'availability', label: '按可用率' },
    { value: 'latency', label: '按延迟' },
  ],
  site: [
    { value: 'smart', label: '✦ 智能排序', gold: true },
    { value: 'default', label: '默认排序' },
    { value: 'availability-median', label: '按可用率中位数' },
    { value: 'latency', label: '按延迟' },
    { value: 'healthy-count', label: '按可用模型数量' },
  ],
};

const SMART_MODEL_WEIGHTS = { a: 0.45, l: 0.30, p: 0.25 };
const SMART_CONFIDENCE_MIN = 30;
const SMART_CONFIDENCE_PENALTY = 0.05;
const SITE_Q_WEIGHTS = { a: 0.50, l: 0.30, s: 0.20 };
const SITE_LATENCY_PIVOT = 8000;
const SITE_STABILITY_PENALTY = 0.5;
const SITE_TOP_K = 6;
const SITE_DISCOUNT = 0.75;
const SITE_GOOD_THRESHOLD = 0.80;
const SITE_GOOD_CAP = 10;
const SITE_PEAK_WEIGHT = 0.7;
const SITE_BREADTH_WEIGHT = 0.3;
const FX_RATES = { CNY: 1, USD: 7.25, $: 7.25, '¥': 1, '￥': 1 };

function sortMedian(values) {
  if (!values.length) return null;
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
}

function timelineStability(timeline) {
  if (!timeline || timeline.length === 0) return null;
  const isUp = (s) => s === 'healthy' || s === 'degraded';
  let covered = 0;
  let upSlots = 0;
  let switches = 0;
  let prevWasUp = null;
  for (const slot of timeline) {
    if (slot.state === 'no_samples') continue;
    covered++;
    const wasUp = isUp(slot.state);
    if (wasUp) upSlots++;
    if (prevWasUp !== null && wasUp !== prevWasUp) switches++;
    prevWasUp = wasUp;
  }
  if (covered < 4) return null;
  return Math.max(0, upSlots / covered - SITE_STABILITY_PENALTY * switches / covered);
}

function priceValueForSort(price) {
  const raw = priceValue(price);
  if (!isFinite(raw)) return Infinity;
  const rate = FX_RATES[price?.currency] ?? FX_RATES[price?.currencySymbol] ?? 1;
  return raw * rate;
}

// Borda 排名融合：对一组卡片在可用率/延迟/价格三维度上排名并加权融合
function smartBordaScores(cards) {
  const hasSample = (c) => c.successRatio != null;
  const sampled = cards.filter(hasSample);
  const unsampled = cards.filter((c) => !hasSample(c));
  if (!sampled.length) return new Map();

  // 分档：{healthy, degraded} 前档，failed 后档（故障端点不因便宜跃升）
  const isT1 = (c) => c.serviceState === 'healthy' || c.serviceState === 'degraded';
  const tier1 = sampled.filter(isT1);
  const tier2 = sampled.filter((c) => !isT1(c));
  const tiers = [tier1, tier2];
  const scoreMap = new Map();

  for (const tier of tiers) {
    if (!tier.length) continue;
    const n = tier.length;
    if (n === 1) {
      scoreMap.set(tier[0], 1.0 - SMART_CONFIDENCE_PENALTY);
      continue;
    }
    // 计算三维度排名（标准竞争排名：同值取平均）
    const dims = [
      { key: 'a', getter: (c) => c.successRatio ?? 0, asc: false },
      { key: 'l', getter: (c) => c.averageLatencyMs ?? Infinity, asc: true },
      { key: 'p', getter: (c) => priceValueForSort(c.lowestPrice), asc: true },
    ];
    const ranks = new Map();
    for (const card of tier) ranks.set(card, { a: 0, l: 0, p: 0 });

    for (const dim of dims) {
      const sorted = [...tier].sort((x, y) => {
        const vx = dim.getter(x), vy = dim.getter(y);
        return dim.asc ? vx - vy : vy - vx;
      });
      // 标准竞争排名：同值取平均排名
      let i = 0;
      while (i < sorted.length) {
        const val = dim.getter(sorted[i]);
        let j = i;
        while (j < sorted.length && dim.getter(sorted[j]) === val) j++;
        const avgRank = (i + j - 1) / 2;
        for (let k = i; k < j; k++) ranks.get(sorted[k])[dim.key] = avgRank;
        i = j;
      }
    }

    // 排名→分数→加权融合
    for (const card of tier) {
      const r = ranks.get(card);
      const rankScore = (rank) => 1 - rank / (n - 1);
      const base = SMART_MODEL_WEIGHTS.a * rankScore(r.a)
        + SMART_MODEL_WEIGHTS.l * rankScore(r.l)
        + SMART_MODEL_WEIGHTS.p * rankScore(r.p);
      const count = card.requestCount ?? 0;
      const confidence = Math.min(1, count / SMART_CONFIDENCE_MIN);
      scoreMap.set(card, base - SMART_CONFIDENCE_PENALTY * (1 - confidence));
    }
  }
  return scoreMap;
}

// 模型视图：对组内卡片按指定 mode 排序，返回排序后的新数组
function sortGroupCards(cards, mode) {
  const arr = [...cards];
  if (mode === 'default') {
    arr.sort(compareCards);
    return arr;
  }
  if (mode === 'price') {
    arr.sort((a, b) => {
      const pa = priceValueForSort(a.lowestPrice);
      const pb = priceValueForSort(b.lowestPrice);
      if (pa !== pb) return pa - pb;
      return compareCards(a, b);
    });
    return arr;
  }
  if (mode === 'availability') {
    arr.sort((a, b) => {
      const ra = a.successRatio ?? -1;
      const rb = b.successRatio ?? -1;
      if (ra !== rb) return rb - ra;
      return compareCards(a, b);
    });
    return arr;
  }
  if (mode === 'latency') {
    arr.sort((a, b) => {
      const la = a.averageLatencyMs ?? Infinity;
      const lb = b.averageLatencyMs ?? Infinity;
      if (la !== lb) return la - lb;
      return compareCards(a, b);
    });
    return arr;
  }
  if (mode === 'smart') {
    const scores = smartBordaScores(arr);
    // 按档位和分数排序
    const tierOf = (c) => c.successRatio == null ? 2
      : (c.serviceState === 'healthy' || c.serviceState === 'degraded') ? 0 : 1;
    arr.sort((a, b) => {
      const ta = tierOf(a), tb = tierOf(b);
      if (ta !== tb) return ta - tb;
      const sa = scores.get(a) ?? 0;
      const sb = scores.get(b) ?? 0;
      if (Math.abs(sa - sb) > 1e-9) return sb - sa;
      return compareCards(a, b);
    });
    // 附加分数供 render 重用
    for (const card of arr) card.__smartScore = scores.get(card) ?? 0;
    return arr;
  }
  arr.sort(compareCards);
  return arr;
}

// 站点智能分数：专精识别算法
// 返回 { score, peak, breadth, goodCount, participating } 或 null（零参与）
function smartSiteScore(siteCards) {
  const participating = siteCards.filter((c) => c.successRatio != null);
  if (!participating.length) return null;

  const qs = participating.map((card) => {
    const a = card.successRatio;
    const latency = card.averageLatencyMs;
    const l = latency != null ? 1 / (1 + latency / SITE_LATENCY_PIVOT) : 0.25;
    const stability = timelineStability(card.timeline);
    const s = stability != null ? stability : a;
    return { card, q: SITE_Q_WEIGHTS.a * a + SITE_Q_WEIGHTS.l * l + SITE_Q_WEIGHTS.s * s };
  });

  // 按 Q 降序，取 top-k 折扣峰值
  qs.sort((x, y) => y.q - x.q);
  const k = Math.min(qs.length, SITE_TOP_K);
  let num = 0, den = 0;
  for (let i = 0; i < k; i++) {
    const w = Math.pow(SITE_DISCOUNT, i);
    num += qs[i].q * w;
    den += w;
  }
  const peak = den > 0 ? num / den : 0;

  const goodCount = qs.filter((x) => x.q >= SITE_GOOD_THRESHOLD).length;
  const breadth = Math.min(1, Math.log(1 + goodCount) / Math.log(1 + SITE_GOOD_CAP));
  const score = SITE_PEAK_WEIGHT * peak + SITE_BREADTH_WEIGHT * breadth;

  return { score, peak, breadth, goodCount, participating: participating.length };
}

// ---- 排序纯函数结束 ----

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
    card.hasAnnouncements = sitesWithAnnouncements.has(card.siteId);
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
      if (definition.key === 'tag') {
        const color = tags.get(value)?.color || 'mint';
        return `<button type="button" class="filter-chip tag-colored ${color}${selected ? ' selected' : ''}" data-filter-category="${definition.key}" data-filter-value="${escapeHTML(value)}"${selectionState}${counts.has(value) ? '' : ' disabled'}><i aria-hidden="true"></i><span>${escapeHTML(value)}</span><b>${counts.get(value) || 0}</b></button>`;
      }
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

const HOMEPAGE_OVERRIDE = {
  'HXI AI': 'https://runanytime.hxi.me/',
};

function homepageOf(siteName, url) {
  if (HOMEPAGE_OVERRIDE[siteName]) return HOMEPAGE_OVERRIDE[siteName];
  try { return new URL(url).origin + '/'; } catch (_) { return ''; }
}

function renderCard(card) {
  const homeUrl = homepageOf(card.siteName, card.siteUrl);
  const homeLink = homeUrl ? `<a class="site-home-link" href="${escapeHTML(homeUrl)}" target="_blank" rel="noopener" title="访问站点主页" onclick="event.stopPropagation()"><svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 2h5v5"/><path d="M14 2L7 9"/><path d="M2 5v7a2 2 0 0 0 2 2h7"/></svg></a>` : '';
  const annAttr = card.hasAnnouncements ? ` onclick="event.stopPropagation();showSiteAnnouncements(${card.siteId},'${escapeHTML(card.siteName)}')"` : '';
  const title = view === 'model'
    ? `<strong class="card-title"><span class="card-title-text"${annAttr}>${escapeHTML(card.siteName)}</span><small class="card-title-text"> · ${escapeHTML(card.rawModelName)}</small>${homeLink}</strong>`
    : `<strong class="card-title"><span class="card-title-text"${annAttr}>${escapeHTML(card.rawModelName)}</span>${homeLink}</strong>`;
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
  const entries = [...grouped.entries()];
  if (view === 'model') {
    const mode = sortMode.model;
    entries.sort(([left], [right]) => left.localeCompare(right, 'zh-CN'));
    return entries.flatMap(([, groupCards]) => sortGroupCards(groupCards, mode));
  }
  // 站点视图：按站点聚合排序
  const mode = sortMode.site;
  if (mode === 'default') {
    entries.sort(([left], [right]) => left.localeCompare(right, 'zh-CN'));
    return entries.flatMap(([, groupCards]) => groupCards.sort(compareCards));
  }
  // 计算站点聚合值
  const aggMap = new Map();
  for (const [name, cards] of entries) {
    if (mode === 'availability-median') {
      const ratios = cards.filter((c) => c.successRatio != null).map((c) => c.successRatio);
      aggMap.set(name, ratios.length ? sortMedian(ratios) : null);
    } else if (mode === 'latency') {
      const lats = cards.filter((c) => c.serviceState === 'healthy' && c.averageLatencyMs != null).map((c) => c.averageLatencyMs);
      aggMap.set(name, lats.length ? sortMedian(lats) : null);
    } else if (mode === 'healthy-count') {
      aggMap.set(name, cards.filter((c) => c.serviceState === 'healthy').length);
    } else if (mode === 'smart') {
      const result = smartSiteScore(cards);
      aggMap.set(name, result ? result.score : null);
    }
  }
  entries.sort(([a], [b]) => {
    const va = aggMap.get(a), vb = aggMap.get(b);
    const na = va == null, nb = vb == null;
    if (na && nb) return a.localeCompare(b, 'zh-CN');
    if (na) return 1;
    if (nb) return -1;
    if (mode === 'latency') return va - vb; // ASC
    return vb - va; // DESC for ratio, count, smart
  });
  return entries.flatMap(([, groupCards]) => groupCards.sort(compareCards));
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
  // 标签可能在数据加载后才就绪，渲染前刷新 card.tagNames
  for (const card of cards) card.tagNames = cardTagsOf(card.siteName);
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

  const groupEntries = [...grouped.entries()];
  // 站点视图非默认排序：保持 Map 插入序（orderedCards 已预排序）
  if (view !== 'site' || sortMode.site === 'default') {
    groupEntries.sort(([a], [b]) => a.localeCompare(b, 'zh-CN'));
  }
  const groupsHTML = groupEntries
    .map(([name, items]) => {
      const sorted = view === 'model' && sortMode.model !== 'default' ? sortGroupCards(items, sortMode.model) : items.sort(compareCards);
      return `<section class="result-group"><div class="group-heading"><div><h2>${escapeHTML(name)}</h2><span class="group-count">${items.length}</span></div></div><div class="card-grid">${sorted.map(renderCard).join('')}</div></section>`;
    })
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
    const metaResponse = await fetch('/api/v1/meta', { cache: 'no-store' });
    const meta = metaResponse.ok ? await metaResponse.json() : {};
    await loadSiteSettings(meta);
    if (revision !== null && meta.revision === revision) {
      await loadAnnouncements();
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
    await loadAnnouncements();
    await new Promise((resolve) => setTimeout(resolve, 0));
    buildCards();
    render();
  } catch {}
}

/* ---------- 通知中心渲染 ---------- */

const NC_RANGES = [
  { key: '24h', ms: 86400000 },
  { key: '7d', ms: 604800000 },
  { key: '30d', ms: 2592000000 },
];
// 'auto' 表示跟随数据：取能装下最新公告的最窄范围。站点公告本身很稀疏（有的站
// 一个月才发一条），固定 24h 默认会让"站点公告"长期显示为空。
let ncRange = 'auto';
let ncSiteAnnouncements = []; // site announcements from API
let ncAllItems = []; // merged + sorted items

function ncRangeMs(key) {
  const range = NC_RANGES.find(item => item.key === key);
  return range ? range.ms : NC_RANGES[0].ms;
}

function resolveNcRange(items, now) {
  if (ncRange !== 'auto') return ncRange; // 用户手动选过就听用户的
  for (const range of NC_RANGES) {
    if (items.some(item => item.time && now - item.time.getTime() <= range.ms)) return range.key;
  }
  return NC_RANGES[NC_RANGES.length - 1].key;
}

function renderAnnouncements() {
  ncSiteAnnouncements = ncSiteAnnouncements.map(item => ({
    type: 'site',
    siteId: item.siteId,
    siteName: item.siteName || `站点 #${item.siteId}`,
    title: item.title,
    content: item.content,
    annType: item.annType || 'default',
    time: item.publishedAt ? new Date(item.publishedAt) : null,
  }));
  ncAllItems = [...ncSiteAnnouncements].sort((a, b) => {
    if (!a.time && !b.time) return 0;
    if (!a.time) return 1;
    if (!b.time) return -1;
    return b.time - a.time;
  });
  renderNotificationCenter();
}

function renderNotificationCenter() {
  const container = announcementContent;
  const now = Date.now();
  // Filter out hidden sites
  const filtered = ncAllItems.filter(item => !(hidden.sites && hidden.sites.has(item.siteName)));
  const visibleFailures = announcements.filter(item => !(hidden.sites && hidden.sites.has(item.siteName)));
  const rangeKey = resolveNcRange(filtered, now);
  const cutoff = now - ncRangeMs(rangeKey);
  const siteItems = filtered.filter(item => !item.time || item.time.getTime() >= cutoff);
  const hasFailures = visibleFailures.length > 0;
  // 有公告数据就要渲染分节（含时间范围切换），即使当前范围里一条都没有 ——
  // 否则分节连同胶囊一起消失，用户没有任何入口把范围放大。
  const hasSiteFeed = filtered.length > 0;

  if (!hasFailures && !hasSiteFeed) {
    container.innerHTML = `<div class="nc-empty"><div class="nc-empty-icon">🔔</div><p class="nc-empty-title">暂无通知</p><p class="nc-empty-desc">站点公告和采集异常都会出现在这里。</p></div>`;
    return;
  }

  let html = '';

  // Section 1: 采集异常
  if (hasFailures) {
    html += `<div class="nc-section"><div class="nc-section-head"><span class="nc-section-dot nc-section-dot--alert"></span><span class="nc-section-title">采集异常</span><span class="nc-section-count">${visibleFailures.length}</span></div>`;
    for (const item of visibleFailures) {
      const initial = (item.siteName || '?')[0];
      html += `<div class="nc-item nc-item--failure"><div class="nc-avatar">${escapeHTML(initial)}</div><div class="nc-content"><div class="nc-meta"><span class="nc-site-name">${escapeHTML(item.siteName)}</span><span class="nc-badge nc-badge-error">${escapeHTML(item.failureCode)}</span></div><div class="nc-text">${escapeHTML(item.reason || '当前采集暂时失败，恢复成功后会自动撤下。')}</div></div></div>`;
    }
    html += '</div>';
  }

  // Section 2: 站点公告时间线 (with inline range pills)
  if (hasSiteFeed) {
    const groups = new Map();
    for (const item of siteItems) {
      const dateKey = item.time ? formatDateKey(item.time) : '其他';
      if (!groups.has(dateKey)) groups.set(dateKey, []);
      groups.get(dateKey).push(item);
    }
    const pills = NC_RANGES.map(range => `<button class="nc-range-btn${rangeKey === range.key ? ' active' : ''}" data-range="${range.key}">${range.key}</button>`).join('');
    html += `<div class="nc-section"><div class="nc-section-head"><span class="nc-section-dot"></span><span class="nc-section-title">站点公告</span><span class="nc-section-count">${siteItems.length}</span><div class="nc-range-group" id="nc-range-group">${pills}</div></div>`;
    if (!siteItems.length) {
      html += `<p class="nc-range-empty">该时间范围内没有公告，换一个范围看看。</p>`;
    }
    for (const [dateKey, groupItems] of groups) {
      html += `<div class="nc-date-group"><div class="nc-date-label">${escapeHTML(dateKey)}</div>`;
      for (const item of groupItems) {
        html += renderNCTimelineItem(item);
      }
      html += '</div>';
    }
    html += '</div>';
  }

  container.innerHTML = html;
  // Re-bind range buttons
  container.querySelector('#nc-range-group')?.addEventListener('click', (e) => {
    const btn = e.target.closest('.nc-range-btn');
    if (!btn || !btn.dataset.range) return;
    ncRange = btn.dataset.range;
    renderNotificationCenter();
  });
}

function renderNCTimelineItem(item) {
  const isFailure = item.type === 'failure';
  const initial = (item.siteName || '?')[0];
  const timeStr = item.time ? formatNCTime(item.time) : '';
  const badgeClass = isFailure ? 'nc-badge-error' : `nc-badge-${item.annType || 'default'}`;
  const badgeLabel = isFailure ? item.code : ({ default: '', success: '成功', warning: '警告', error: '错误', ongoing: '进行中' }[item.annType] || '');
  const titleHTML = item.title ? `<div class="nc-title-line">${escapeHTML(item.title)}</div>` : '';
  const contentPreview = truncateContent(item.content || '', 150);
  const needsExpand = (item.content || '').length > 150;
  const expandBtn = needsExpand ? `<button class="nc-expand-btn" onclick="ncExpandContent(this)">展开全文</button>` : '';
  const fullContent = escapeHTML(item.content || '');

  return `<div class="nc-item ${isFailure ? 'nc-item--failure' : ''}">
    <div class="nc-avatar">${escapeHTML(initial)}</div>
    <div class="nc-content">
      <div class="nc-meta">
        <span class="nc-site-name">${escapeHTML(item.siteName)}</span>
        ${badgeLabel ? `<span class="nc-badge ${badgeClass}">${escapeHTML(badgeLabel)}</span>` : ''}
        <span class="nc-time">${escapeHTML(timeStr)}</span>
      </div>
      ${titleHTML}
      <div class="nc-text" data-full="${fullContent}">${escapeHTML(contentPreview)}</div>
      ${expandBtn}
    </div>
  </div>`;
}

function ncExpandContent(btn) {
  const textEl = btn.previousElementSibling;
  if (!textEl) return;
  const full = textEl.dataset.full;
  if (textEl.classList.contains('nc-text-expanded')) {
    textEl.textContent = truncateContent(full, 150);
    textEl.classList.remove('nc-text-expanded');
    btn.textContent = '展开全文';
  } else {
    textEl.textContent = full;
    textEl.classList.add('nc-text-expanded');
    btn.textContent = '收起';
  }
}

function truncateContent(text, max) {
  if (text.length <= max) return text;
  return text.substring(0, max) + '…';
}

function formatDateKey(date) {
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const target = new Date(date.getFullYear(), date.getMonth(), date.getDate());
  const diff = today - target;
  if (diff === 0) return '今天';
  if (diff === 86400000) return '昨天';
  if (diff < 604800000) return `${Math.floor(diff / 86400000)} 天前`;
  return `${date.getMonth() + 1} 月 ${date.getDate()} 日`;
}

function formatNCTime(date) {
  const h = String(date.getHours()).padStart(2, '0');
  const m = String(date.getMinutes()).padStart(2, '0');
  return `${h}:${m}`;
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
    sitesWithAnnouncements = new Set(payload.siteAnnouncementSiteIds || []);
    const next = payload.announcements || [];
    const nextSignature = next.map((item) => `${item.siteId}:${item.failureCode}:${item.reason}`).join('|');
    const changed = nextSignature !== announcementSignature;
    announcements = next;
    announcementSignature = nextSignature;
    ncSiteAnnouncements = payload.siteAnnouncements || [];
    renderAnnouncements();
    if (changed && announcements.length) {
      let seen = '';
      try { seen = localStorage.getItem('relayscope-announcements-seen') || ''; } catch (_) {}
      if (seen !== nextSignature) {
        try { localStorage.setItem('relayscope-announcements-seen', nextSignature); } catch (_) {}
        toggleNCSidebar(true);
      }
    }
  } catch (_) {}
}

// Sidebar toggle — 通知面板是 board-shell 的右侧拓展列，开合会带动整页重新居中
function toggleNCSidebar(show) {
  if (!boardShell) return;
  const isOpen = boardShell.classList.contains('nc-open');
  const next = show !== undefined ? show : !isOpen;
  boardShell.classList.toggle('nc-open', next);
  if (ncPanel) ncPanel.inert = !next; // 收起时 0 宽面板不应进入 Tab 顺序
  announcementAction?.setAttribute('aria-expanded', next ? 'true' : 'false');
  try { localStorage.setItem('relayscope-nc-open', next ? '1' : '0'); } catch {}
}
announcementAction?.addEventListener('click', () => toggleNCSidebar());
ncCloseBtn?.addEventListener('click', () => toggleNCSidebar(false));
// Subscribe button → jump to customize notify tab
const ncSubscribeBtn = document.querySelector('#nc-subscribe-btn');
if (ncSubscribeBtn) {
  ncSubscribeBtn.addEventListener('click', () => {
    toggleNCSidebar(false);
    window.location.hash = '#customize'; // hashchange → applyRoute 打开定制页
    if (!currentUser) return; // 未登录走门禁拦截页
    setCustomizeTab('notify');
    window.scrollTo({ top: 0, behavior: 'smooth' });
  });
}
// Restore sidebar state
let ncRestoreOpen = true;
try { ncRestoreOpen = localStorage.getItem('relayscope-nc-open') !== '0'; } catch {}
toggleNCSidebar(ncRestoreOpen);

async function openDetails(rawModel, siteName) {
  detailTitle.textContent = rawModel;
  detailSubtitle.textContent = siteName ? `${siteName} · 最近 24 小时` : '最近 24 小时';
  detailSiteFilter.hidden = !siteName;
  detailSiteFilter.dataset.site = siteName || '';
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
// 一键把筛选器改为"仅筛选本站"：清空全部条件后只保留该站点
detailSiteFilter.addEventListener('click', () => {
  const site = detailSiteFilter.dataset.site;
  if (!site) return;
  for (const set of Object.values(selectedFilters)) set.clear();
  selectedFilters.site.add(site);
  detailDialog.close();
  currentPage = 1;
  render();
  window.scrollTo({ top: 0, behavior: 'smooth' });
});
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
  renderSortSelect();
  render();
});
siteViewButton.addEventListener('click', () => {
  view = 'site';
  currentPage = 1;
  modelViewButton.setAttribute('aria-pressed', 'false');
  siteViewButton.setAttribute('aria-pressed', 'true');
  renderSortSelect();
  render();
});
function closeSortDropdown() {
  if (!sortDropdown) return;
  sortDropdown.hidden = true;
  sortTrigger.setAttribute('aria-expanded', 'false');
  sortTrigger.classList.remove('open');
}

function selectSortOption(value) {
  if (value === 'smart' && membershipIs() !== 'active') {
    closeSortDropdown();
    showToast('✦ 智能排序为会员专属功能');
    openRecharge();
    return;
  }
  sortMode[view] = value;
  closeSortDropdown();
  currentPage = 1;
  render();
  renderSortSelect();
  if (membershipIs() === 'active') saveSorting();
}

if (sortTrigger) {
  sortTrigger.addEventListener('click', () => {
    const isOpen = !sortDropdown.hidden;
    if (isOpen) { closeSortDropdown(); return; }
    sortDropdown.hidden = false;
    sortTrigger.setAttribute('aria-expanded', 'true');
    sortTrigger.classList.add('open');
    // 选中当前项获得焦点
    const current = sortDropdown.querySelector('.sort-option.selected');
    if (current) current.focus();
  });
  sortTrigger.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeSortDropdown();
    if (e.key === 'ArrowDown' || e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      sortDropdown.hidden = false;
      sortTrigger.setAttribute('aria-expanded', 'true');
      sortTrigger.classList.add('open');
      const current = sortDropdown.querySelector('.sort-option.selected');
      if (current) current.focus();
    }
  });
}
if (sortDropdown) {
  sortDropdown.addEventListener('click', (e) => {
    const btn = e.target.closest('.sort-option');
    if (btn) selectSortOption(btn.dataset.value);
  });
  sortDropdown.addEventListener('keydown', (e) => {
    const options = [...sortDropdown.querySelectorAll('.sort-option')];
    const idx = options.indexOf(document.activeElement);
    if (e.key === 'ArrowDown') { e.preventDefault(); options[Math.min(idx + 1, options.length - 1)]?.focus(); }
    if (e.key === 'ArrowUp') { e.preventDefault(); options[Math.max(idx - 1, 0)]?.focus(); }
    if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); if (idx >= 0) selectSortOption(options[idx].dataset.value); }
    if (e.key === 'Escape') { closeSortDropdown(); sortTrigger.focus(); }
    if (e.key === 'Tab') closeSortDropdown();
  });
}
// 点击外部关闭
document.addEventListener('click', (e) => {
  if (sortDropdown && !sortDropdown.hidden && !e.target.closest('#sort-field')) closeSortDropdown();
  if (!e.target.closest('#customize-display-settings .sort-field')) closeCustomizeSortDropdowns();
  if (!e.target.closest('[data-notify-platform-field]')) closeNotifyPlatformDropdown();
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
  else if (customizeTab === 'sites') renderChipGrid(CUSTOMIZE_DIMENSIONS[0], customizeSitesPanel);
  else if (customizeTab === 'providers') renderChipGrid(CUSTOMIZE_DIMENSIONS[1], customizeProvidersPanel);
  else if (customizeTab === 'models') renderChipGrid(CUSTOMIZE_DIMENSIONS[2], customizeModelsPanel);
  else if (customizeTab === 'tags') renderCustomizeTags();
  else if (customizeTab === 'notify') renderCustomizeNotify();
  updateNavBadges();
  if (searchFocusKey && !tagFocusAfterRender) {
    const panel = customizeTab === 'display' ? customizeSettingsPanel : customizeTab === 'tags' ? customizeTagsPanel : (customizeTab === 'sites' ? customizeSitesPanel : customizeTab === 'providers' ? customizeProvidersPanel : customizeModelsPanel);
    const input = panel.querySelector(`[data-pref-search="${searchFocusKey}"]`);
    if (input) {
      input.focus();
      input.setSelectionRange(input.value.length, input.value.length);
    }
  }
}

const CUSTOMIZE_PANEL_BY_TAB = { display: customizeSettingsPanel, sites: customizeSitesPanel, providers: customizeProvidersPanel, models: customizeModelsPanel, tags: customizeTagsPanel, notify: customizeNotifyPanel };

function updateNavBadges() {
  for (const key of ['sites', 'providers', 'models']) {
    const badge = customizeNav.querySelector(`[data-nav-badge="${key}"]`);
    if (!badge) continue;
    const n = hidden[key].size;
    badge.textContent = String(n);
    badge.hidden = n === 0;
  }
}

function setCustomizeTab(tab) {
  // 非会员（含已过期）永远停留在门禁页，禁止通过切换标签渲染出定制内容（通知订阅除外）
  if (membershipIs() !== 'active' && tab !== 'notify') { enterCustomize(); return; }
  customizeTab = tab;
  customizeNav.querySelectorAll('[data-customize-tab]').forEach((button) => {
    button.setAttribute('aria-selected', String(button.dataset.customizeTab === tab));
  });
  for (const [key, panel] of Object.entries(CUSTOMIZE_PANEL_BY_TAB)) {
    panel.hidden = key !== tab;
    panel.scrollTop = 0;
  }
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

function renderSortSelect() {
  if (!sortTrigger || !sortDropdown) return;
  const options = SORT_OPTIONS[view] || SORT_OPTIONS.model;
  const isMember = membershipIs() === 'active';
  const current = options.find((o) => o.value === sortMode[view]) || options[0];
  // 更新触发按钮
  const label = current.gold && !isMember ? '✦ 智能排序' : current.label;
  sortTrigger.querySelector('.sort-trigger-label').textContent = label;
  sortTrigger.classList.toggle('gold-active', !!(current.gold && isMember));
  // 构建下拉选项（智能与普通之间插入分隔线）
  const parts = [];
  options.forEach((o) => {
    const isSelected = sortMode[view] === o.value;
    const isSmart = o.gold;
    const displayLabel = isSmart && !isMember ? '智能排序' : o.label.replace('✦ ', '');
    const badge = isSmart && !isMember ? '<span class="sort-badge">会员</span>' : '';
    const mark = isSmart ? '<span class="sort-mark" aria-hidden="true">✦</span>' : '';
    parts.push(`<button class="sort-option${isSelected ? ' selected' : ''}${isSmart ? ' smart' : ''}" role="option" data-value="${o.value}"${isSelected ? ' aria-selected="true"' : ''} tabindex="${isSelected ? '0' : '-1'}">${mark}<span class="sort-option-label">${displayLabel}</span>${badge}${isSelected ? '<svg class="sort-check" aria-hidden="true" viewBox="0 0 24 24" focusable="false"><path d="m5 13 4 4L19 7" /></svg>' : ''}</button>`);
    // 智能排序之后插入分隔线
    if (isSmart) parts.push('<div class="sort-option-sep" role="separator" aria-hidden="true"></div>');
  });
  sortDropdown.innerHTML = parts.join('');
}

function renderCustomizeSortSelect(key) {
  const field = customizeSettingsPanel.querySelector(`[data-customize-sort-field="${key}"]`);
  if (!field) return;
  const trigger = field.querySelector('[data-customize-sort]');
  const dropdown = field.querySelector('[data-customize-sort-dropdown]');
  const options = SORT_OPTIONS[key] || SORT_OPTIONS.model;
  const isMember = membershipIs() === 'active';
  const current = options.find((o) => o.value === sortMode[key]) || options[0];
  const label = current.gold && !isMember ? '✦ 智能排序' : current.label;
  trigger.querySelector('.sort-trigger-label').textContent = label;
  trigger.classList.toggle('gold-active', !!(current.gold && isMember));
  const parts = [];
  options.forEach((o) => {
    const isSelected = sortMode[key] === o.value;
    const isSmart = o.gold;
    const displayLabel = isSmart && !isMember ? '智能排序' : o.label.replace('✦ ', '');
    const badge = isSmart && !isMember ? '<span class="sort-badge">会员</span>' : '';
    const mark = isSmart ? '<span class="sort-mark" aria-hidden="true">✦</span>' : '';
    parts.push(`<button class="sort-option${isSelected ? ' selected' : ''}${isSmart ? ' smart' : ''}" role="option" data-value="${o.value}"${isSelected ? ' aria-selected="true"' : ''} tabindex="${isSelected ? '0' : '-1'}">${mark}<span class="sort-option-label">${displayLabel}</span>${badge}${isSelected ? '<svg class="sort-check" aria-hidden="true" viewBox="0 0 24 24" focusable="false"><path d="m5 13 4 4L19 7" /></svg>' : ''}</button>`);
    if (isSmart) parts.push('<div class="sort-option-sep" role="separator" aria-hidden="true"></div>');
  });
  dropdown.innerHTML = parts.join('');
}

function selectCustomizeSortOption(key, value) {
  if (value === 'smart' && membershipIs() !== 'active') {
    closeCustomizeSortDropdowns();
    showToast('✦ 智能排序为会员专属功能');
    openRecharge();
    return;
  }
  sortMode[key] = value;
  closeCustomizeSortDropdowns();
  saveSorting();
  currentPage = 1;
  render();
  renderCustomizeSortSelect(key);
  renderSortSelect();
}

function closeCustomizeSortDropdowns() {
  if (!customizeSettingsPanel) return;
  customizeSettingsPanel.querySelectorAll('[data-customize-sort-field]').forEach((field) => {
    const trigger = field.querySelector('[data-customize-sort]');
    const dropdown = field.querySelector('[data-customize-sort-dropdown]');
    if (!dropdown || dropdown.hidden) return;
    dropdown.hidden = true;
    trigger.setAttribute('aria-expanded', 'false');
    trigger.classList.remove('open');
  });
}

function closeNotifyPlatformDropdown() {
  if (!customizeNotifyPanel) return;
  const trigger = customizeNotifyPanel.querySelector('[data-notify-platform-trigger]');
  const dropdown = customizeNotifyPanel.querySelector('[data-notify-platform-dropdown]');
  if (!dropdown || dropdown.hidden) return;
  dropdown.hidden = true;
  if (trigger) { trigger.setAttribute('aria-expanded', 'false'); trigger.classList.remove('open'); }
}

function renderCustomizeDisplay() {
  const sortRow = (key, label) => `<div class="pref-row sort-row"><span class="pref-row-text"><strong>${label}</strong></span><div class="sort-field" data-customize-sort-field="${key}"><button type="button" class="sort-trigger" data-customize-sort="${key}" aria-haspopup="listbox" aria-expanded="false" aria-label="${label}自动排序"><svg class="sort-trigger-icon" aria-hidden="true" viewBox="0 0 24 24" focusable="false"><path d="M3 6h18M3 12h12M3 18h6" /></svg><span class="sort-trigger-label"></span><svg class="sort-trigger-chevron" aria-hidden="true" viewBox="0 0 24 24" focusable="false"><path d="m6 9 6 6 6-6" /></svg></button><div class="sort-dropdown" role="listbox" aria-label="${label}自动排序" data-customize-sort-dropdown="${key}" hidden></div></div></div>`;
  customizeSettingsPanel.innerHTML = `<section class="pref-section"><div class="pref-head"><h3>默认状态</h3></div><label class="pref-row toggle-row"><span class="pref-row-text"><strong>默认只看当前可用模型</strong><small>开启后每次访问都自动勾选"只看当前可用"</small></span><span class="toggle"><input id="pref-default-healthy" type="checkbox"${defaultHealthy ? ' checked' : ''} aria-label="默认只看当前可用模型"><i></i></span></label></section><section class="pref-section"><div class="pref-head"><h3>自动排序</h3></div>${sortRow('model', '按模型展示时')}${sortRow('site', '按站点展示时')}<p class="pref-sort-hint">* 智能排序综合可用率、延迟、价格自动优选；站点视图侧重识别有多个长期稳定低延迟模型的专精站点。</p></section>`;
  renderCustomizeSortSelect('model');
  renderCustomizeSortSelect('site');
}

function renderChipGrid(definition, panel) {
  const key = definition.key;
  const query = customizeSearches[key].trim().toLowerCase();
  const chips = dimensionCounts(definition)
    .filter((item) => !query || item.value.toLowerCase().includes(query))
    .map((item) => {
      const visible = !hidden[key].has(item.value);
      return `<button type="button" class="hide-chip${visible ? '' : ' hidden'}" data-pref-toggle data-pref-key="${key}" data-pref-value="${escapeHTML(item.value)}" aria-pressed="${visible ? 'true' : 'false'}" title="${escapeHTML(item.value)}（${item.count} 个模型入口）"><span class="hide-chip-name">${escapeHTML(item.value)}</span><span class="hide-chip-count">${item.count}</span></button>`;
    }).join('');
  const hiddenCount = hidden[key].size;
  panel.innerHTML = `<section class="pref-section chip-grid-section"><div class="pref-head"><h3>屏蔽${definition.title}</h3><span class="pref-note">点击胶囊切换显示；已隐藏的会变暗并带删除线</span></div><div class="chip-grid-head"><label class="pref-search"><span>搜索${definition.title}</span><input type="search" data-pref-search="${key}" value="${escapeHTML(customizeSearches[key])}" placeholder="筛选${definition.title}"></label>${hiddenCount ? `<button type="button" class="pref-reset" data-pref-reset="${key}">全部显示</button>` : ''}</div><div class="chip-grid" data-pref-list="${key}">${chips || '<p class="pref-empty">没有匹配的条目</p>'}</div></section>`;
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
  const target = event.target.closest('[data-pref-reset],[data-pref-toggle],[data-customize-sort],[data-customize-sort-dropdown] .sort-option,[data-tag-new],[data-tag-cancel],[data-tag-delete],[data-tag-rename],[data-tag-swatch],[data-tag-color-option],[data-tag-add],[data-site-tag-remove],[data-tag-menu-item],[data-tag-undo],[data-notify-platform-trigger],[data-notify-platform-dropdown] .sort-option');
  if (!target) return;

  if (target.matches('[data-notify-platform-trigger]')) {
    const dropdown = customizeNotifyPanel.querySelector('[data-notify-platform-dropdown]');
    if (!dropdown) return;
    const isOpen = !dropdown.hidden;
    closeNotifyPlatformDropdown();
    if (!isOpen) {
      dropdown.hidden = false;
      target.setAttribute('aria-expanded', 'true');
      target.classList.add('open');
      const current = dropdown.querySelector('.sort-option.selected');
      if (current) current.focus();
    }
    return;
  }

  if (target.matches('[data-notify-platform-dropdown] .sort-option')) {
    const prev = notifyPlatform;
    notifyPlatform = target.dataset.value;
    closeNotifyPlatformDropdown();
    if (prev !== notifyPlatform) { notifyChannelDirty = true; renderCustomizeNotify(); }
    return;
  }

  if (target.matches('[data-customize-sort]')) {
    const key = target.dataset.customizeSort;
    const dropdown = customizeSettingsPanel.querySelector(`[data-customize-sort-dropdown="${key}"]`);
    const isOpen = !dropdown.hidden;
    closeCustomizeSortDropdowns();
    if (!isOpen) {
      dropdown.hidden = false;
      target.setAttribute('aria-expanded', 'true');
      target.classList.add('open');
      const current = dropdown.querySelector('.sort-option.selected');
      if (current) current.focus();
    }
    return;
  }

  if (target.matches('[data-customize-sort-dropdown] .sort-option')) {
    const dropdown = target.closest('[data-customize-sort-dropdown]');
    selectCustomizeSortOption(dropdown.dataset.customizeSortDropdown, target.dataset.value);
    return;
  }

  if (target.matches('[data-pref-toggle]')) {
    const key = target.dataset.prefKey;
    const value = target.dataset.prefValue;
    if (!CUSTOMIZE_DIMENSIONS.some((d) => d.key === key)) return;
    toggleHiddenKey(key, value);
    return;
  }

  if (target.matches('[data-pref-reset]')) {
    hidden[target.dataset.prefReset].clear();
    saveHidden();
    currentPage = 1;
    render();
    renderCustomize();
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
}

function toggleHiddenKey(key, value) {
  if (hidden[key].has(value)) hidden[key].delete(value);
  else hidden[key].add(value);
  saveHidden();
  currentPage = 1;
  render();
  renderCustomize();
  updateNavBadges();
  const panel = CUSTOMIZE_PANEL_BY_TAB[customizeTab];
  const count = panel ? panel.querySelector(`[data-pref-count="${key}"]`) : null;
  const resetButton = panel ? panel.querySelector(`[data-pref-reset="${key}"]`) : null;
  const n = hidden[key].size;
  if (count) count.hidden = n === 0;
  if (resetButton) resetButton.hidden = n === 0;
}

function handleCustomizeInput(event) {
  const search = event.target.closest('[data-pref-search]');
  if (!search) return;
  const key = search.dataset.prefSearch;
  customizeSearches[key] = search.value;
  searchFocusKey = key;
  const section = search.closest('.pref-section');
  if (!section) return;
  const list = section.querySelector('.chip-grid') || section.querySelector('.pref-list');
  if (!list) return;
  const q = search.value.trim().toLowerCase();
  let matches = 0;
  list.querySelectorAll(':scope .hide-chip, :scope .pref-row').forEach((item) => {
    const name = item.querySelector('strong, .hide-chip-name')?.textContent || item.dataset.prefValue || '';
    const visible = !q || name.toLowerCase().includes(q);
    item.hidden = !visible;
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
// 定制页排序下拉键盘导航
customizeSettingsPanel.addEventListener('keydown', (event) => {
  const trigger = event.target.closest('[data-customize-sort]');
  if (trigger) {
    if (event.key === 'Escape') { closeCustomizeSortDropdowns(); trigger.focus(); }
    if (event.key === 'ArrowDown' || event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      const key = trigger.dataset.customizeSort;
      const dropdown = customizeSettingsPanel.querySelector(`[data-customize-sort-dropdown="${key}"]`);
      if (dropdown.hidden) {
        closeCustomizeSortDropdowns();
        dropdown.hidden = false;
        trigger.setAttribute('aria-expanded', 'true');
        trigger.classList.add('open');
      }
      const current = dropdown.querySelector('.sort-option.selected');
      if (current) current.focus();
    }
    return;
  }
  const dropdown = event.target.closest('[data-customize-sort-dropdown]');
  if (dropdown) {
    const options = [...dropdown.querySelectorAll('.sort-option')];
    const idx = options.indexOf(document.activeElement);
    if (event.key === 'ArrowDown') { event.preventDefault(); options[Math.min(idx + 1, options.length - 1)]?.focus(); }
    if (event.key === 'ArrowUp') { event.preventDefault(); options[Math.max(idx - 1, 0)]?.focus(); }
    if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); if (idx >= 0) selectCustomizeSortOption(dropdown.dataset.customizeSortDropdown, options[idx].dataset.value); }
    if (event.key === 'Escape') { closeCustomizeSortDropdowns(); dropdown.closest('.sort-field').querySelector('[data-customize-sort]').focus(); }
    if (event.key === 'Tab') closeCustomizeSortDropdowns();
  }
});
// 通知订阅面板平台下拉键盘导航
customizeNotifyPanel.addEventListener('keydown', (event) => {
  const trigger = event.target.closest('[data-notify-platform-trigger]');
  if (trigger) {
    if (event.key === 'Escape') { closeNotifyPlatformDropdown(); trigger.focus(); }
    if (event.key === 'ArrowDown' || event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      const dropdown = customizeNotifyPanel.querySelector('[data-notify-platform-dropdown]');
      if (dropdown.hidden) {
        closeNotifyPlatformDropdown();
        dropdown.hidden = false;
        trigger.setAttribute('aria-expanded', 'true');
        trigger.classList.add('open');
      }
      const current = dropdown.querySelector('.sort-option.selected');
      if (current) current.focus();
    }
    return;
  }
  const dropdown = event.target.closest('[data-notify-platform-dropdown]');
  if (dropdown) {
    const options = [...dropdown.querySelectorAll('.sort-option')];
    const idx = options.indexOf(document.activeElement);
    if (event.key === 'ArrowDown') { event.preventDefault(); options[Math.min(idx + 1, options.length - 1)]?.focus(); }
    if (event.key === 'ArrowUp') { event.preventDefault(); options[Math.max(idx - 1, 0)]?.focus(); }
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      if (idx >= 0) { notifyPlatform = options[idx].dataset.value; notifyChannelDirty = true; closeNotifyPlatformDropdown(); renderCustomizeNotify(); }
    }
    if (event.key === 'Escape') { closeNotifyPlatformDropdown(); customizeNotifyPanel.querySelector('[data-notify-platform-trigger]')?.focus(); }
    if (event.key === 'Tab') closeNotifyPlatformDropdown();
  }
});
const customizeTabOrder = ['display', 'sites', 'providers', 'models', 'tags', 'notify'];
const customizeTabButtons = Object.fromEntries(customizeTabOrder.map((key) => [key, customizeNav.querySelector(`[data-customize-tab="${key}"]`)]));
customizeNav.querySelectorAll('[data-customize-tab]').forEach((button) => {
  button.addEventListener('click', () => setCustomizeTab(button.dataset.customizeTab));
});
customizeNav.addEventListener('keydown', (event) => {
  const current = event.target.closest('[data-customize-tab]');
  if (!current) return;
  const index = customizeTabOrder.indexOf(current.dataset.customizeTab);
  if (index === -1) return;
  let next = null;
  if (event.key === 'ArrowRight' || event.key === 'ArrowDown') next = customizeTabOrder[(index + 1) % customizeTabOrder.length];
  if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') next = customizeTabOrder[(index - 1 + customizeTabOrder.length) % customizeTabOrder.length];
  if (next) {
    event.preventDefault();
    setCustomizeTab(next);
    customizeTabButtons[next].focus();
  }
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
let siteSettingsLoaded = false;
let pledgeCreditAvailable = 0;
let wishItems = [];
let pledgeTargetId = null;

const formatDate = (value) => value ? new Date(value).toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' }) : '—';
const collectLocalPreferences = () => ({
  hidden: { sites: [...hidden.sites], providers: [...hidden.providers], models: [...hidden.models] },
  defaultHealthy,
  tags: Object.fromEntries([...tags].map(([name, tag]) => [name, { color: tag.color, sites: [...tag.sites] }])),
  sorting: { ...sortMode },
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
  renderSortSelect();
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
      if (cloud.sorting && typeof cloud.sorting === 'object') {
        const validModels = new Set(SORT_OPTIONS.model.map((o) => o.value));
        const validSites = new Set(SORT_OPTIONS.site.map((o) => o.value));
        sortMode = {
          model: validModels.has(cloud.sorting.model) ? cloud.sorting.model : 'default',
          site: validSites.has(cloud.sorting.site) ? cloud.sorting.site : 'default',
        };
      }
      // cloudSynced 尚未置真，镜像到 localStorage 不会触发回环上传
      storageSet('relayscope-hidden', JSON.stringify({ sites: [...hidden.sites], providers: [...hidden.providers], models: [...hidden.models] }));
      storageSet('relayscope-default-healthy', defaultHealthy ? '1' : '0');
      storageSet('relayscope-tags', JSON.stringify(Object.fromEntries([...tags].map(([name, tag]) => [name, { color: tag.color, sites: [...tag.sites] }]))));
      storageSet('relayscope-sorting', JSON.stringify(sortMode));
      renderSortSelect();
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
  if (customizeNav) customizeNav.hidden = state !== 'active' && !currentUser;
  if (state === 'active') {
    setCustomizeTab(customizeTab);
  } else if (currentUser) {
    // Logged in but not member: show notify tab only
    customizeNav.hidden = false;
    customizeSettingsPanel.hidden = true;
    customizeSitesPanel.hidden = true;
    customizeProvidersPanel.hidden = true;
    customizeModelsPanel.hidden = true;
    customizeTagsPanel.hidden = true;
    customizeNotifyPanel.hidden = false;
    renderCustomizeGate(false);
  } else {
    // 门禁态：仅 settings 面板承载 gate，其余面板隐藏
    customizeSettingsPanel.hidden = false;
    customizeSitesPanel.hidden = true;
    customizeProvidersPanel.hidden = true;
    customizeModelsPanel.hidden = true;
    customizeTagsPanel.hidden = true;
    customizeNotifyPanel.hidden = true;
    renderCustomizeGate(!currentUser);
  }
  updateCustomizeSubtitle();
}

/* ---------- 通知订阅管理 ---------- */

let notifySubscriptions = [];
// 已为哪个用户加载过订阅列表（空列表也算加载完成，避免反复显示"正在加载…"）
let notifySubscriptionsLoadedFor = null;

async function loadNotifySubscriptions() {
  const uid = currentUser?.id ?? 0;
  if (!currentUser) { notifySubscriptions = []; notifySubscriptionsLoadedFor = uid; return; }
  try {
    const resp = await fetch('/api/v1/me/notification-subscriptions');
    if (!resp.ok) { notifySubscriptions = []; notifySubscriptionsLoadedFor = uid; return; }
    const data = await resp.json();
    notifySubscriptions = data.subscriptions || [];
  } catch { notifySubscriptions = []; }
  notifySubscriptionsLoadedFor = uid;
}

const NOTIFY_FREE_LIMIT = 3;
const NOTIFY_PLATFORMS = [
  { key: 'telegram', label: 'Telegram', icon: '📱', placeholder: 'Chat ID（如 123456789）' },
  { key: 'feishu', label: '飞书', icon: '💬', placeholder: 'Webhook URL（如 https://open.feishu.cn/...）' },
  { key: 'bark', label: 'Bark', icon: '🔔', placeholder: '设备 Key' },
];
let notifyPlatform = 'telegram';
// 渠道输入框是否有未保存的修改（输入或切换平台都会置脏；保存后清掉）
let notifyChannelDirty = false;

// 从已有订阅里推出“当前生效的渠道”：按 platform+target 计数取众数。
// 返回 null 表示用户还没保存过任何渠道——此时不允许订阅站点。
function notifySavedChannel() {
  if (!notifySubscriptions.length) return null;
  const counts = new Map();
  for (const sub of notifySubscriptions) {
    const key = sub.platform + '|' + sub.target;
    counts.set(key, (counts.get(key) || 0) + 1);
  }
  let best = null, bestCount = 0;
  for (const [key, count] of counts) {
    if (count > bestCount) { best = key; bestCount = count; }
  }
  const sep = best.indexOf('|');
  return {
    platform: sep < 0 ? notifyPlatform : best.slice(0, sep),
    target: sep < 0 ? '' : best.slice(sep + 1),
    count: bestCount,
    distinctTargets: counts.size,
  };
}

// 脱敏显示目标：只露尾 4 位，用户能确认“是我那个 key”又不泄露全文
function maskNotifyTarget(target) {
  if (!target) return '';
  return target.length <= 4 ? target : '…' + target.slice(-4);
}

function platformLabel(key) {
  return NOTIFY_PLATFORMS.find(p => p.key === key)?.label || key;
}

function renderCustomizeNotify() {
  if (!currentUser) {
    customizeNotifyPanel.innerHTML = '<div class="empty-state"><p>请先登录以管理通知订阅。</p></div>';
    return;
  }
  if (notifySubscriptionsLoadedFor !== (currentUser?.id ?? 0)) {
    loadNotifySubscriptions().then(() => renderCustomizeNotify());
    customizeNotifyPanel.innerHTML = '<div class="empty-state"><p>正在加载…</p></div>';
    return;
  }
  const isMember = membershipIs() === 'active';
  const siteMap = new Map(rows.map(r => [r.siteId, r.siteName]));
  const uniqueSites = [...siteMap.entries()].sort((a, b) => a[1].localeCompare(b[1], 'zh-CN'));
  const subscribedSiteIds = new Set(notifySubscriptions.map(s => s.siteId));
  const subCount = subscribedSiteIds.size;
  const limitText = isMember ? '会员无限' : `${subCount} / ${NOTIFY_FREE_LIMIT}`;

  let html = '<div class="notify-panel">';

  // Header with limit indicator — 与其它定制页面板同款 pref-head 标题行
  html += '<section class="pref-section">';
  html += '<div class="pref-head">';
  html += '<h3>通知订阅</h3>';
  html += `<div class="notify-limit ${!isMember && subCount >= NOTIFY_FREE_LIMIT ? 'notify-limit--warn' : ''}"><span class="notify-limit-count">${limitText}</span>${!isMember ? '<span class="notify-limit-label">免费额度</span>' : ''}</div>`;
  html += '</div>';
  html += '<p class="pref-desc">订阅站点公告更新，第一时间收到推送通知。</p>';

  // Platform config —— 渠道是第一步：必须先显式保存，站点订阅才能用上它
  const saved = notifySavedChannel();
  // 未编辑时，平台与输入框都回填已保存的渠道，让用户一眼看到“现在生效的是这个”
  if (!notifyChannelDirty && saved) {
    notifyPlatform = saved.platform;
  }
  const dirtyInput = customizeNotifyPanel.querySelector('#notify-target')?.value ?? '';
  const currentValue = notifyChannelDirty ? dirtyInput : (saved?.target ?? '');

  html += '<div class="notify-platform-section">';
  html += '<div class="notify-platform-row">';
  const plat = NOTIFY_PLATFORMS.find(p => p.key === notifyPlatform) || NOTIFY_PLATFORMS[0];
  html += '<div class="notify-field"><span class="notify-field-label">推送平台</span>';
  html += '<div class="sort-field" data-notify-platform-field>';
  html += `<button type="button" class="sort-trigger" data-notify-platform-trigger aria-haspopup="listbox" aria-expanded="false" aria-label="推送平台"><span class="sort-trigger-label">${plat.icon} ${plat.label}</span><svg class="sort-trigger-chevron" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="13" height="13"><path d="m6 9 6 6 6-6"/></svg></button>`;
  html += '<div class="sort-dropdown" data-notify-platform-dropdown role="listbox" hidden>';
  for (const p of NOTIFY_PLATFORMS) {
    const sel = p.key === notifyPlatform;
    html += `<button class="sort-option${sel ? ' selected' : ''}" role="option" data-value="${p.key}"${sel ? ' aria-selected="true"' : ''}>`;
    html += `<span class="sort-option-label">${p.icon} ${p.label}</span>`;
    if (sel) html += '<svg class="sort-check" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" width="15" height="15"><polyline points="20 6 9 17 4 12"/></svg>';
    html += '</button>';
  }
  html += '</div></div></div>';
  html += `<label class="notify-field notify-field--flex"><span class="notify-field-label">推送目标</span><input id="notify-target" class="notify-input" type="text" value="${escapeHTML(currentValue)}" placeholder="${plat.placeholder}" autocomplete="off" spellcheck="false"></label>`;
  html += '</div>';
  html += '<div class="notify-channel-status" data-notify-channel-status></div>';
  const canSave = notifyChannelDirty && currentValue.trim() !== '';
  html += `<div class="notify-channel-actions"><span class="notify-test-hint">保存前可先测试当前填写值</span><button type="button" class="btn btn-sm" data-notify-test>测试推送</button><button type="button" class="btn btn-primary btn-sm" data-notify-channel-save ${canSave ? '' : 'disabled'}>保存渠道</button></div>`;
  html += '</div>';
  html += '</section>';

  // Site list
  html += '<section class="pref-section">';
  const allSelected = uniqueSites.length > 0 && uniqueSites.every(([siteId]) => subscribedSiteIds.has(siteId));
  const channelSummary = saved ? ` · 渠道 ${platformLabel(saved.platform)} ${maskNotifyTarget(saved.target)}` : ' · 请先保存渠道';
  html += `<div class="pref-head notify-site-list-head"><h3>选择站点</h3><span class="pref-count" data-notify-count>已选 ${subscribedSiteIds.size} 个站点${channelSummary}</span><button type="button" class="pref-reset" data-notify-select-all>${allSelected ? '全不选' : '全选'}</button></div>`;
  for (const [siteId, siteName] of uniqueSites) {
    const isSubscribed = subscribedSiteIds.has(siteId);
    const sub = notifySubscriptions.find(s => s.siteId === siteId);
    const canToggle = isSubscribed || isMember || subCount < NOTIFY_FREE_LIMIT;
    html += `<label class="notify-site-item ${isSubscribed ? 'notify-site-item--active' : ''} ${!canToggle ? 'notify-site-item--disabled' : ''}" data-site-id="${siteId}">`;
    html += `<input type="checkbox" class="notify-site-check" data-site-id="${siteId}" ${isSubscribed ? 'checked' : ''} ${!canToggle ? 'disabled' : ''}>`;
    html += `<span class="notify-site-avatar">${escapeHTML(siteName[0])}</span>`;
    html += `<span class="notify-site-name">${escapeHTML(siteName)}</span>`;
    if (isSubscribed && sub) {
      const subPlat = NOTIFY_PLATFORMS.find(p => p.key === sub.platform);
      html += `<span class="notify-site-channel" title="${escapeHTML(platformLabel(sub.platform))} · ${escapeHTML(sub.target)}">${subPlat?.icon || '📢'} ${escapeHTML(maskNotifyTarget(sub.target))}</span>`;
    }
    html += '</label>';
  }
  html += '</div>';
  html += '</section>';

  // Upgrade CTA for non-members at limit
  if (!isMember && subCount >= NOTIFY_FREE_LIMIT) {
    html += '<div class="notify-upgrade"><span class="notify-upgrade-icon">✦</span><span>升级会员解锁无限站点订阅</span><button class="btn btn-primary btn-sm" type="button" onclick="showUpgradeGate()">开通会员</button></div>';
  }

  html += '</div>';
  customizeNotifyPanel.innerHTML = html;
  updateNotifyChannelStatus();

  // Event: site checkbox toggle
  customizeNotifyPanel.querySelectorAll('.notify-site-check').forEach(cb => {
    cb.addEventListener('change', () => handleSiteToggle(cb.dataset.siteId, cb.checked));
  });
  // 输入只置脏 + 局部刷新状态行，不整页重渲染（否则输入框会丢焦点）
  customizeNotifyPanel.querySelector('#notify-target')?.addEventListener('input', () => {
    notifyChannelDirty = true;
    updateNotifyChannelStatus();
  });
  customizeNotifyPanel.querySelector('[data-notify-test]')?.addEventListener('click', handleNotifyTest);
  customizeNotifyPanel.querySelector('[data-notify-channel-save]')?.addEventListener('click', handleNotifyChannelSave);
  customizeNotifyPanel.querySelector('[data-notify-select-all]')?.addEventListener('click', handleNotifySelectAll);
}

// 渠道状态行：不重渲染，只更新文本/按钮，避免输入时丢焦点
function updateNotifyChannelStatus() {
  const statusEl = customizeNotifyPanel.querySelector('[data-notify-channel-status]');
  const saveBtn = customizeNotifyPanel.querySelector('[data-notify-channel-save]');
  if (!statusEl) return;
  const saved = notifySavedChannel();
  const value = customizeNotifyPanel.querySelector('#notify-target')?.value?.trim() ?? '';
  let text = '', cls = 'notify-channel-status';
  if (!saved) {
    text = '尚未保存任何推送渠道 —— 填写目标并保存后，再订阅站点';
    cls += ' notify-channel-status--hint';
  } else if (notifyChannelDirty) {
    if (saved.distinctTargets > 1) {
      text = `● 有未保存的修改 —— 现有订阅存在 ${saved.distinctTargets} 个不同目标，保存后将统一为当前填写值`;
    } else {
      text = '● 有未保存的修改 —— 保存后才会应用到已订阅站点';
    }
    cls += ' notify-channel-status--dirty';
  } else {
    text = `✓ 已保存 · ${platformLabel(saved.platform)} ${maskNotifyTarget(saved.target)} · ${saved.count} 个站点订阅使用此渠道`;
    cls += ' notify-channel-status--saved';
  }
  statusEl.className = cls;
  statusEl.textContent = text;
  if (saveBtn) saveBtn.disabled = !(notifyChannelDirty && value !== '');
}

// 保存渠道：把平台+目标统一写入用户名下全部订阅，待发送队列同步改道
async function handleNotifyChannelSave(event) {
  const button = event.currentTarget;
  const target = customizeNotifyPanel.querySelector('#notify-target')?.value?.trim();
  const platform = notifyPlatform || 'telegram';
  if (!target) { showToast('请先填写推送目标', 'error'); return; }
  button.disabled = true;
  try {
    const resp = await fetch('/api/v1/me/notification-subscriptions', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ platform, target })
    });
    if (!resp.ok) {
      const err = await resp.json().catch(() => ({}));
      showToast(err.message || '保存渠道失败', 'error');
      updateNotifyChannelStatus();
      return;
    }
    const data = await resp.json().catch(() => ({}));
    notifyChannelDirty = false;
    await loadNotifySubscriptions();
    renderCustomizeNotify();
    const n = data.updated ?? 0;
    showToast(n > 0 ? `渠道已保存 · 已应用到 ${n} 个订阅` : '渠道已保存', 'success');
  } catch {
    showToast('保存渠道失败', 'error');
    updateNotifyChannelStatus();
  }
}

// 测试推送：向当前填写的渠道目标同步发送一条验证消息
async function handleNotifyTest(event) {
  const button = event.currentTarget;
  const target = document.querySelector('#notify-target')?.value?.trim();
  if (!target) {
    showToast('请先填写推送目标', 'error');
    return;
  }
  button.disabled = true;
  try {
    const resp = await fetch(`/api/v1/me/notification-test?platform=${encodeURIComponent(notifyPlatform || 'telegram')}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ target })
    });
    if (!resp.ok) {
      const err = await resp.json().catch(() => ({}));
      showToast(err.message || '测试推送失败', 'error');
      return;
    }
    showToast('测试消息已发送，请查收', 'success');
  } catch {
    showToast('测试推送失败', 'error');
  } finally {
    button.disabled = false;
  }
}

// 全选/全不选：用已保存的渠道逐个调用订阅接口，免费额度门禁照常生效
async function handleNotifySelectAll() {
  const isMember = membershipIs() === 'active';
  const uniqueSites = [...new Map(rows.map(r => [r.siteId, r.siteName])).keys()];
  const subscribed = new Set(notifySubscriptions.map(s => s.siteId));
  const allSelected = uniqueSites.length > 0 && uniqueSites.every(id => subscribed.has(id));
  const saved = notifySavedChannel();

  if (allSelected) {
    let removed = 0;
    for (const sub of [...notifySubscriptions]) {
      try {
        const resp = await fetch(`/api/v1/me/notification-subscriptions/${sub.id}`, { method: 'DELETE' });
        if (!resp.ok) break;
        removed += 1;
      } catch { break; }
    }
    await loadNotifySubscriptions();
    renderCustomizeNotify();
    if (removed) showToast(`已取消 ${removed} 个订阅`);
    return;
  }

  if (!saved) {
    showToast('请先保存推送渠道，再订阅站点', 'error');
    return;
  }
  const { platform, target } = saved;
  let added = 0;
  let failed = false;
  for (const siteId of uniqueSites) {
    if (subscribed.has(siteId)) continue;
    if (!isMember && subscribed.size >= NOTIFY_FREE_LIMIT) {
      showNotifyLimitGate();
      break;
    }
    try {
      const resp = await fetch('/api/v1/me/notification-subscriptions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ siteId, platform, target })
      });
      if (!resp.ok) { failed = true; break; }
      subscribed.add(siteId);
      added += 1;
    } catch { failed = true; break; }
  }
  await loadNotifySubscriptions();
  renderCustomizeNotify();
  if (added && !failed) showToast(`已订阅 ${added} 个站点`, 'success');
  else if (added && failed) showToast(`已订阅 ${added} 个站点，其余失败，请重试`, 'error');
}

async function handleSiteToggle(siteId, checked) {
  siteId = Number(siteId);
  const isMember = membershipIs() === 'active';

  if (checked) {
    // 订阅用的是已保存的渠道，而不是输入框里的草稿——这样“保存渠道”才是唯一事实来源
    const saved = notifySavedChannel();
    if (!saved) {
      showToast('请先保存推送渠道，再订阅站点', 'error');
      renderCustomizeNotify();
      return;
    }
    // Check limit
    if (!isMember) {
      const uniqueSites = new Set(notifySubscriptions.map(s => s.siteId));
      if (uniqueSites.size >= NOTIFY_FREE_LIMIT) {
        showNotifyLimitGate();
        // Re-render to uncheck
        renderCustomizeNotify();
        return;
      }
    }
    const { platform, target } = saved;
    try {
      const resp = await fetch('/api/v1/me/notification-subscriptions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ siteId, platform, target })
      });
      if (!resp.ok) { const err = await resp.json().catch(() => ({})); showToast(err.message || '订阅失败', 'error'); renderCustomizeNotify(); return; }
      showToast(`已订阅 · 推送到 ${platformLabel(platform)}（${maskNotifyTarget(target)}）`, 'success');
      await loadNotifySubscriptions();
      renderCustomizeNotify();
    } catch { showToast('订阅失败', 'error'); renderCustomizeNotify(); }
  } else {
    const sub = notifySubscriptions.find(s => s.siteId === siteId);
    if (!sub) return;
    try {
      await fetch(`/api/v1/me/notification-subscriptions/${sub.id}`, { method: 'DELETE' });
      showToast('已取消订阅');
      await loadNotifySubscriptions();
      renderCustomizeNotify();
    } catch { showToast('取消失败', 'error'); renderCustomizeNotify(); }
  }
}

function showNotifyLimitGate() {
  const price = siteSettings.membershipMonthlyPriceLdc || 15;
  showToast(`✦ 免费版最多订阅 ${NOTIFY_FREE_LIMIT} 个站点，升级会员解锁无限订阅`);
}

function showUpgradeGate() {
  // Jump to display tab which shows the gate
  setCustomizeTab('display');
}

// Pre-load subscriptions when user logs in
if (currentUser) loadNotifySubscriptions();

function renderCustomizeGate(loggedOut) {
  const price = siteSettings.membershipMonthlyPriceLdc || 15;
  const freeCredit = siteSettings.wishFreeCreditLdc || 10;
  const title = loggedOut ? '开通会员以使用定制' : '定制需要有效会员';
  const actions = loggedOut
    ? '<button type="button" class="gate-cta gate-cta-primary" data-gate-login>登录 LINUX DO<svg aria-hidden="true" viewBox="0 0 24 24" focusable="false"><path d="M5 12h14m-6-6 6 6-6 6" /></svg></button>'
    : '<button type="button" class="gate-cta gate-cta-primary" data-gate-redeem>兑换会员</button><button type="button" class="gate-cta gate-cta-ghost" data-gate-recharge>LDC 直充</button>';
  const features = `
    <li class="gate-feature"><span class="gate-feature-icon" aria-hidden="true"><svg viewBox="0 0 24 24" focusable="false"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" /><path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" /><path d="M14.12 14.12a3 3 0 1 1-4.24-4.24" /><path d="m1 1 22 22" /></svg></span><span class="gate-feature-text"><strong>站点筛选</strong><small>只看你想看的站点</small></span></li>
    <li class="gate-feature"><span class="gate-feature-icon" aria-hidden="true"><svg viewBox="0 0 24 24" focusable="false"><path d="M20.6 13.4 13.4 20.6a2 2 0 0 1-2.8 0L3 13V3h10l7.6 7.6a2 2 0 0 1 0 2.8z" /><path d="M7 7h.01" /></svg></span><span class="gate-feature-text"><strong>标签管理</strong><small>为站点设置自定义标签</small></span></li>
    <li class="gate-feature"><span class="gate-feature-icon" aria-hidden="true"><svg viewBox="0 0 24 24" focusable="false"><path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/></svg></span><span class="gate-feature-text"><strong>订阅站点通知</strong><small>站点公告更新时获得推送</small></span></li>`;
  customizeSettingsPanel.innerHTML = `<section class="pref-section customize-gate">
    <div class="gate-hero">
      <span class="gate-mark" aria-hidden="true">✦</span>
      <h3>${title}</h3>
    </div>
    <ul class="gate-features">${features}</ul>
    <div class="gate-price"><span class="gate-price-label">会员价</span><span class="gate-price-value"><b>${price}</b><small>LDC / 月</small></span><span class="gate-price-perk">每月额外获赠 <b>${freeCredit} LDC</b> <a class="gate-wish-link" href="#wishes">许愿额度</a></span></div>
    <div class="gate-actions">${actions}</div>
  </section>`;
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
    button.textContent = '✦ 立即兑换';
  }
});

// ---- LDC 直充会员（金色尊贵风：快捷档位 + 自定义月数） ----
const rechargeForm = document.querySelector('#recharge-form');
const rechargeMonthsInput = document.querySelector('#recharge-months');
let rechargeMonths = 1;
function selectedRechargeMonths() {
  const checked = rechargeForm.querySelector('input[name="recharge-months"]:checked');
  if (!checked) return 1;
  return checked.value === 'custom' ? Math.min(36, Math.max(1, Math.round(Number(rechargeMonthsInput.value) || 0))) : Number(checked.value);
}
function updateRechargePrice() {
  const unit = Number(siteSettings.membershipMonthlyPriceLdc) || 15;
  for (const label of rechargeForm.querySelectorAll('[data-plan-price]')) {
    label.textContent = `${Number(label.dataset.planPrice) * unit} LDC`;
  }
  rechargeMonths = selectedRechargeMonths();
  rechargePrice.textContent = `${rechargeMonths * unit} LDC · ${rechargeMonths} 个月`;
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
rechargeForm.addEventListener('change', (event) => {
  if (event.target.name !== 'recharge-months') return;
  if (event.target.value === 'custom') rechargeMonthsInput.focus();
  updateRechargePrice();
});
rechargeMonthsInput.addEventListener('input', () => {
  if (rechargeForm.querySelector('input[name="recharge-months"]:checked')?.value === 'custom') updateRechargePrice();
});
rechargeForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const button = event.currentTarget.querySelector('button[type="submit"]');
  if (button.disabled) return;
  button.disabled = true;
  button.textContent = '创建订单…';
  rechargeMessage.dataset.state = '';
  try {
    const response = await fetch('/api/v1/membership/recharge', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ months: selectedRechargeMonths() }), signal: AbortSignal.timeout(20000) });
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
    button.textContent = '✦ 立即开通';
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
  boardShell.hidden = !boardView;
  summaryElement.hidden = !boardView;
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
    button.textContent = '✦ 提交许愿';
  }
});

// ---- 助力（LDC 支付） ----
wishList.addEventListener('click', (event) => {
  const button = event.target.closest('[data-wish-pledge]');
  if (!button) return;
  const wish = wishItems.find((item) => String(item.id) === button.dataset.wishPledge);
  if (!wish) return;
  pledgeTargetId = wish.id;
  pledgeTitle.innerHTML = `<span class="gold-mark" aria-hidden="true">✦</span>为「${escapeHTML(wish.name)}」许愿`;
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
    button.textContent = '✦ 立即助力';
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

async function loadSiteSettings(meta) {
  if (siteSettingsLoaded) return;
  try {
    if (!meta) return;
    siteSettings.membershipMonthlyPriceLdc = parseInt(meta.membershipMonthlyPriceLdc, 10) || 15;
    siteSettings.wishDefaultTargetLdc = parseInt(meta.wishDefaultTargetLdc, 10) || 30;
    siteSettingsLoaded = true;
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
// 登录态就绪后再做首次路由渲染，否则 #customize 刷新会因 currentUser 尚为空而误显登录门禁
loadUser().finally(applyRoute);
{
  const paidOrder = readPaidOrderNo();
  if (paidOrder) pollOrderStatus(paidOrder);
}
setInterval(loadRows, 60000);

/* ---------- 站点公告 ---------- */

let siteAnnouncementCache = {};

async function showSiteAnnouncements(siteId, siteName) {
  // Open sidebar filtered to a specific site
  const titleEl = document.querySelector('.nc-sidebar-title');
  if (titleEl) titleEl.textContent = siteName;
  toggleNCSidebar(true);
  try {
    const resp = await fetch(`/api/v1/public/site-announcements?site_id=${siteId}&limit=20`);
    if (!resp.ok) { announcementContent.innerHTML = '<div class="nc-empty"><div class="nc-empty-icon">📭</div><p class="nc-empty-title">加载失败</p></div>'; return; }
    const data = await resp.json();
    const anns = (data.announcements || []).map(a => ({
      type: 'site', siteId, siteName,
      title: a.title, content: a.content,
      annType: a.annType || 'default',
      time: a.publishedAt ? new Date(a.publishedAt) : null,
    }));
    const prevItems = ncAllItems;
    ncAllItems = anns.sort((a, b) => (b.time || 0) - (a.time || 0));
    ncRange = '30d';
    renderNotificationCenter();
    ncAllItems = prevItems;
  } catch { announcementContent.innerHTML = '<div class="nc-empty"><div class="nc-empty-icon">📭</div><p class="nc-empty-title">加载失败</p></div>'; }
}
