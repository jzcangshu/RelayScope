-- The production AbrDNS seed was renamed before pricing support was added.
-- Match its structural identity and untouched config without changing the name.
UPDATE sites
SET adapter_config = '{"pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status","pricingOptional":true,"pricingRequiresSession":true}',
    updated_at = strftime('%s','now') * 1000
WHERE base_url = 'https://new-api.abrdns.com'
  AND source_url = 'https://new-api.abrdns.com/status'
  AND adapter_key = 'newapi-probe'
  AND adapter_config = '{}';
