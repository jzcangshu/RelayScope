-- Apply the final normal collection interval to sites that still use the old default.
UPDATE sites SET interval_seconds = 900, updated_at = strftime('%s','now') * 1000
WHERE interval_seconds = 1200;
