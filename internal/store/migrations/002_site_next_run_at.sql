ALTER TABLE sites ADD COLUMN next_run_at INTEGER;

CREATE INDEX sites_schedule_idx ON sites(enabled, next_run_at);

-- Spread existing enabled sites across the next 15 minutes during upgrade.
-- New sites intentionally remain NULL and are collected immediately once.
UPDATE sites
SET next_run_at = CAST(strftime('%s', 'now') AS INTEGER) * 1000 + ((id * 137) % 900000)
WHERE enabled = 1 AND next_run_at IS NULL;
