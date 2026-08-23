-- Upgrade only untouched initial seeds. Customized site rows are preserved.
UPDATE sites
SET adapter_config = '{"pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status","pricingOptional":true,"pricingRequiresSession":true}',
    updated_at = strftime('%s','now') * 1000
WHERE name = 'new-api abrdns'
  AND base_url = 'https://new-api.abrdns.com'
  AND source_url = 'https://new-api.abrdns.com/status'
  AND adapter_key = 'newapi-probe'
  AND adapter_config = '{}';

UPDATE sites
SET adapter_config = '{"pricingAdapter":"model-market"}',
    updated_at = strftime('%s','now') * 1000
WHERE name = 'fengwind'
  AND base_url = 'https://api.fengwind.com'
  AND source_url = 'https://api.fengwind.com/model-market'
  AND adapter_key = 'model-market'
  AND adapter_config = '{}';

UPDATE sites
SET base_url = 'https://runanytime.hxi.me',
    adapter_config = '{"statusBaseUrl":"https://stat.hxi.me","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status","pricingOptional":true,"pricingRequiresSession":true}',
    updated_at = strftime('%s','now') * 1000
WHERE name = 'HXI AI'
  AND base_url = 'https://stat.hxi.me'
  AND source_url = 'https://stat.hxi.me/status/ai'
  AND adapter_key = 'uptime-kuma'
  AND adapter_config = '{}';
