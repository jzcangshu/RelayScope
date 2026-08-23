-- Before session_required existed, every stored site session was applied.
-- Preserve that behavior and mark the known login-dependent pricing origins.
UPDATE sites
SET session_required = 1,
    updated_at = strftime('%s','now') * 1000
WHERE EXISTS (
  SELECT 1 FROM encrypted_sessions
  WHERE encrypted_sessions.site_id = sites.id
    AND encrypted_sessions.purpose = 'site-http'
)
OR base_url IN (
  'https://api.42w.shop',
  'https://52ccl.net',
  'https://ai.121628.xyz',
  'https://v-api.de5.net'
);
