-- History coverage is adapter-declared evidence and cannot be reconstructed
-- safely from existing buckets, so legacy rows intentionally remain NULL.
ALTER TABLE raw_models ADD COLUMN history_coverage_start INTEGER;
ALTER TABLE raw_models ADD COLUMN history_coverage_end INTEGER;
