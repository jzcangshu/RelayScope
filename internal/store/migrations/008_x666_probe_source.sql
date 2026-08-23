-- The public x666 page embeds its health probe from a separate origin. Match
-- the original seed configuration structurally so administrator changes and
-- the production display name are preserved.
UPDATE sites
SET adapter_key = 'newapi-probe',
    adapter_config = '{"statusBaseUrl":"https://tool.x666.me","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status","pricingOptional":true}',
    updated_at = strftime('%s','now') * 1000
WHERE base_url = 'https://x666.me'
  AND source_url = 'https://x666.me/'
  AND adapter_key = 'newapi-pricing'
  AND adapter_config = '{"skipDetails":true}';
