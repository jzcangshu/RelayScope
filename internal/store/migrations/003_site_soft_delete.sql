ALTER TABLE sites ADD COLUMN deleted_at INTEGER;

CREATE INDEX sites_active_idx ON sites(enabled, deleted_at, id);
