(function (scope) {
  async function prepareBundles(sites, accountCredentials, readBrowserCredentials) {
    const bundles = [];
    const skipped = [];

    for (const site of sites) {
      try {
        const credentials = site.adapterKey === 'model-market'
          ? await readBrowserCredentials(site)
          : accountCredentials[site.origin];
        if (!credentials) {
          throw new Error('在备份中没有精确匹配的访问令牌');
        }
        bundles.push({ siteId: site.id, origin: site.origin, ...credentials });
      } catch (error) {
        skipped.push({ siteId: site.id, name: site.name, reason: error?.message || '读取访问令牌失败' });
      }
    }

    return { bundles, skipped };
  }

  scope.RelayPulseCapture = { prepareBundles };
  if (typeof module !== 'undefined') module.exports = scope.RelayPulseCapture;
})(typeof self !== 'undefined' ? self : globalThis);
