#!/usr/bin/env node
// 排序算法回测脚本：从生产 API 拉取真实数据，用实际发货代码运行排序，验证排名合理性。
// 用法: node scripts/sort_backtest.cjs [api_url]

const { readFileSync } = require('node:fs');
const { join } = require('node:path');

const API_URL = process.argv[2] || 'https://watchbot.cfd/api/v1/public/dashboard';

// ---- 从 dashboard.js 切出实际算法代码 ----
function loadAlgorithms() {
  const source = readFileSync(join(__dirname, '..', 'web', 'public', 'dashboard.js'), 'utf8');
  // priceValue
  const pvStart = source.indexOf('function priceValue(price)');
  const pvEnd = source.indexOf('\nfunction lowestPrice');
  const { priceValue } = Function(`${source.slice(pvStart, pvEnd)}; return { priceValue };`)();
  // compareCards
  const crStart = source.indexOf('function compareRows(a, b)');
  const ccEnd = source.indexOf('\nfunction buildTimeline');
  const { compareCards } = Function(`${source.slice(crStart, ccEnd)}; return { compareCards };`)();
  // sort block
  const sbStart = source.indexOf('// ---- 排序纯函数（供面板与测试共用');
  const sbEnd = source.indexOf('// ---- 排序纯函数结束 ----');
  return Function('priceValue', 'compareCards',
    `${source.slice(sbStart, sbEnd)}\n; return { sortGroupCards, smartSiteScore, sortMedian, timelineStability, priceValueForSort, SORT_OPTIONS, SMART_MODEL_WEIGHTS, SITE_Q_WEIGHTS, SITE_PEAK_WEIGHT, SITE_BREADTH_WEIGHT, SITE_TOP_K, SITE_DISCOUNT, SITE_GOOD_THRESHOLD, SITE_LATENCY_PIVOT };`
  )(priceValue, compareCards);
}

// ---- 简化 buildCards（与 dashboard.js 逻辑一致） ----
function buildCards(rows, buckets) {
  const grouped = new Map();
  for (const row of rows) {
    const key = JSON.stringify([row.siteId, row.rawModelName]);
    if (!grouped.has(key)) {
      grouped.set(key, {
        key, provider: row.provider || '未归类', ruleName: row.ruleName || row.rawModelName,
        siteId: row.siteId, siteName: row.siteName, siteUrl: row.siteUrl,
        rawModelName: row.rawModelName, groups: [],
      });
    }
    grouped.get(key).groups.push(row);
  }

  const historyByCard = new Map();
  for (const bucket of buckets) {
    const key = JSON.stringify([bucket.siteId, bucket.rawModelName]);
    if (!historyByCard.has(key)) historyByCard.set(key, []);
    historyByCard.get(key).push(bucket);
  }

  return [...grouped.values()].map((card) => {
    // Representative: best state → highest ratio → lowest latency
    card.groups.sort((a, b) => {
      const sr = { healthy: 0, degraded: 1, failed: 2, unknown: 3, no_samples: 4 };
      return (sr[a.serviceState] ?? 9) - (sr[b.serviceState] ?? 9)
        || (b.successRatio ?? -1) - (a.successRatio ?? -1)
        || (a.averageLatencyMs ?? Infinity) - (b.averageLatencyMs ?? Infinity);
    });
    const rep = card.groups[0];
    Object.assign(card, rep);
    card.searchText = [card.provider, card.ruleName, card.siteName, card.rawModelName].join(' ').toLowerCase();
    // Timeline
    const end = Math.ceil(Date.now() / 1800000) * 1800000;
    const start = end - 48 * 1800000;
    const states = Array(48).fill('no_samples');
    const cardHistory = historyByCard.get(card.key) || [];
    for (const bucket of cardHistory) {
      const bs = Date.parse(bucket.start), be = Date.parse(bucket.end);
      if (!Number.isFinite(bs) || !Number.isFinite(be) || be <= start || bs >= end) continue;
      const first = Math.max(0, Math.floor((Math.max(bs, start) - start) / 1800000));
      const last = Math.min(47, Math.ceil((Math.min(be, end) - start) / 1800000) - 1);
      const rank = { healthy: 0, degraded: 1, failed: 2, unknown: 3, no_samples: 4 };
      for (let i = first; i <= last; i++) {
        if ((rank[bucket.serviceState] ?? 5) < (rank[states[i]] ?? 5)) states[i] = bucket.serviceState;
      }
    }
    card.timeline = states.map((state) => ({ state }));
    card.hasHistory = cardHistory.length > 0;
    // lowestPrice
    card.lowestPrice = card.groups
      .filter((g) => (g.serviceState === 'healthy' || g.serviceState === 'degraded') && g.price?.available)
      .sort((a, b) => (a.price?.inputPerMillion ?? Infinity) - (b.price?.inputPerMillion ?? Infinity))[0]?.price || null;
    return card;
  });
}

// ---- 主流程 ----
async function main() {
  console.log(`\n=== 排序算法回测 ===\nAPI: ${API_URL}\n`);

  const res = await fetch(API_URL);
  if (!res.ok) throw new Error(`API ${res.status}`);
  const data = await res.json();
  console.log(`数据: ${data.rows.length} 行, ${data.buckets.length} 历史桶, ${data.hours}h 窗口\n`);

  const cards = buildCards(data.rows, data.buckets);
  const algo = loadAlgorithms();

  // ---- 1. 数据审计 ----
  console.log('--- 数据审计 ---');
  const currencies = new Map();
  const latencies = [];
  const ratios = [];
  const reqCounts = [];
  for (const r of data.rows) {
    if (r.price?.currency) currencies.set(r.price.currency, (currencies.get(r.price.currency) || 0) + 1);
    if (r.price?.currencySymbol) currencies.set(r.price.currencySymbol, (currencies.get(r.price.currencySymbol) || 0) + 1);
    if (r.averageLatencyMs != null) latencies.push(r.averageLatencyMs);
    if (r.successRatio != null) ratios.push(r.successRatio);
    if (r.requestCount != null) reqCounts.push(r.requestCount);
  }
  console.log('币种分布:', Object.fromEntries(currencies));
  latencies.sort((a, b) => a - b);
  ratios.sort((a, b) => a - b);
  reqCounts.sort((a, b) => a - b);
  const pct = (arr, p) => arr.length ? arr[Math.floor(arr.length * p)] : null;
  console.log(`延迟 ms: p10=${pct(latencies, 0.1)?.toFixed(0)} p50=${pct(latencies, 0.5)?.toFixed(0)} p90=${pct(latencies, 0.9)?.toFixed(0)} (n=${latencies.length})`);
  console.log(`可用率: p10=${pct(ratios, 0.1)?.toFixed(3)} p50=${pct(ratios, 0.5)?.toFixed(3)} p90=${pct(ratios, 0.9)?.toFixed(3)} (n=${ratios.length})`);
  console.log(`请求量: p10=${pct(reqCounts, 0.1)} p50=${pct(reqCounts, 0.5)} p90=${pct(reqCounts, 0.9)} (n=${reqCounts.length})`);
  console.log(`常量验证: LATENCY_PIVOT=${algo.SITE_LATENCY_PIVOT} GOOD_THRESHOLD=${algo.SITE_GOOD_THRESHOLD} TOP_K=${algo.SITE_TOP_K} DISCOUNT=${algo.SITE_DISCOUNT} PEAK=${algo.SITE_PEAK_WEIGHT} BREADTH=${algo.SITE_BREADTH_WEIGHT}`);
  console.log();

  // ---- 2. 模型视图智能排序（Top-10 大组） ----
  console.log('--- 模型视图智能排序（Top-10 最大模型组） ---');
  const modelGroups = new Map();
  for (const card of cards) {
    const key = card.ruleName || card.rawModelName;
    if (!modelGroups.has(key)) modelGroups.set(key, []);
    modelGroups.get(key).push(card);
  }
  const topModels = [...modelGroups.entries()].sort((a, b) => b[1].length - a[1].length).slice(0, 10);

  for (const [modelName, groupCards] of topModels) {
    const sorted = algo.sortGroupCards(groupCards, 'smart');
    console.log(`\n[${modelName}] (${sorted.length} 站点)`);
    for (let i = 0; i < Math.min(8, sorted.length); i++) {
      const c = sorted[i];
      const ratio = c.successRatio != null ? (c.successRatio * 100).toFixed(1) + '%' : '—';
      const lat = c.averageLatencyMs != null ? Math.round(c.averageLatencyMs) + 'ms' : '—';
      const score = c.__smartScore != null ? c.__smartScore.toFixed(3) : '—';
      console.log(`  ${i + 1}. ${c.siteName.padEnd(20)} ${c.serviceState.padEnd(10)} ${ratio.padStart(6)} ${lat.padStart(8)} score=${score}`);
    }
  }
  console.log();

  // ---- 3. 站点视图智能排序 ----
  console.log('--- 站点视图智能排序（Top-20） ---');
  const siteGroups = new Map();
  for (const card of cards) {
    if (!siteGroups.has(card.siteName)) siteGroups.set(card.siteName, []);
    siteGroups.get(card.siteName).push(card);
  }

  const siteScores = [];
  for (const [siteName, siteCards] of siteGroups) {
    const result = algo.smartSiteScore(siteCards);
    const healthy = siteCards.filter((c) => c.serviceState === 'healthy').length;
    const total = siteCards.length;
    siteScores.push({ siteName, result, healthy, total });
  }
  siteScores.sort((a, b) => {
    if (!a.result && !b.result) return a.siteName.localeCompare(b.siteName);
    if (!a.result) return 1;
    if (!b.result) return -1;
    return b.result.score - a.result.score || b.healthy - a.healthy;
  });

  console.log('排名 | 站点 | score | peak | breadth | goodCount | 健康/总数');
  console.log('-----|------|-------|------|---------|-----------|----------');
  for (let i = 0; i < Math.min(20, siteScores.length); i++) {
    const s = siteScores[i];
    const r = s.result;
    if (!r) {
      console.log(`${String(i + 1).padStart(4)} | ${s.siteName.padEnd(20)} | — (无参与模型)`);
    } else {
      console.log(`${String(i + 1).padStart(4)} | ${s.siteName.padEnd(20)} | ${r.score.toFixed(3)} | ${r.peak.toFixed(3)} | ${r.breadth.toFixed(3)} | ${String(r.goodCount).padStart(9)} | ${s.healthy}/${s.total}`);
    }
  }
  console.log();

  // ---- 4. 基础排序对比 ----
  console.log('--- 站点视图·可用率中位数 Top-10 ---');
  const medianRanks = [];
  for (const [siteName, siteCards] of siteGroups) {
    const ratios = siteCards.filter((c) => c.successRatio != null).map((c) => c.successRatio);
    const med = ratios.length ? algo.sortMedian(ratios) : null;
    medianRanks.push({ siteName, median: med, n: siteCards.length });
  }
  medianRanks.sort((a, b) => (b.median ?? -1) - (a.median ?? -1));
  for (let i = 0; i < Math.min(10, medianRanks.length); i++) {
    const r = medianRanks[i];
    console.log(`  ${i + 1}. ${r.siteName.padEnd(20)} ${(r.median != null ? (r.median * 100).toFixed(1) + '%' : '—').padStart(6)} (${r.n} 模型)`);
  }
  console.log();

  console.log('--- 站点视图·健康模型数 Top-10 ---');
  const countRanks = [...siteGroups.entries()].map(([name, cards]) => ({
    siteName: name, healthy: cards.filter((c) => c.serviceState === 'healthy').length, total: cards.length,
  })).sort((a, b) => b.healthy - a.healthy);
  for (let i = 0; i < Math.min(10, countRanks.length); i++) {
    const r = countRanks[i];
    console.log(`  ${i + 1}. ${r.siteName.padEnd(20)} ${r.healthy} 健康 / ${r.total} 总计`);
  }
  console.log();

  console.log('=== 回测完成 ===');
}

main().catch((err) => { console.error('回测失败:', err.message); process.exit(1); });
