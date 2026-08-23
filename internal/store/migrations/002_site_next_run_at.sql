ALTER TABLE sites ADD COLUMN next_run_at INTEGER;

CREATE INDEX sites_schedule_idx ON sites(enabled, next_run_at);
