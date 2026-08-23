PRAGMA foreign_keys = ON;

CREATE TABLE app_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
) WITHOUT ROWID;

INSERT INTO app_meta(key, value) VALUES
    ('schema_version', '1'),
    ('data_revision', '0');

CREATE TABLE sites (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    base_url TEXT NOT NULL UNIQUE,
    source_url TEXT NOT NULL,
    adapter_key TEXT NOT NULL,
    adapter_config TEXT NOT NULL DEFAULT '{}',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    interval_seconds INTEGER NOT NULL DEFAULT 1200 CHECK (interval_seconds >= 300),
    jitter_seconds INTEGER NOT NULL DEFAULT 120 CHECK (jitter_seconds >= 0),
    acquisition_state TEXT NOT NULL DEFAULT 'stale',
    last_success_at INTEGER,
    next_run_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX sites_schedule_idx ON sites(enabled, next_run_at);

CREATE TABLE adapter_catalog (
    adapter_key TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    config_schema TEXT NOT NULL,
    registered_at INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TABLE model_rules (
    id INTEGER PRIMARY KEY,
    provider TEXT NOT NULL,
    canonical_name TEXT NOT NULL UNIQUE,
    required_tokens TEXT NOT NULL DEFAULT '[]',
    any_tokens TEXT NOT NULL DEFAULT '[]',
    excluded_tokens TEXT NOT NULL DEFAULT '[]',
    aliases TEXT NOT NULL DEFAULT '[]',
    pattern TEXT,
    priority INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    generated INTEGER NOT NULL DEFAULT 0 CHECK (generated IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX model_rules_enabled_idx ON model_rules(enabled, priority DESC, canonical_name);

CREATE TABLE raw_models (
    id INTEGER PRIMARY KEY,
    site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    raw_name TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    provider_hint TEXT NOT NULL DEFAULT '',
    source_extension TEXT,
    first_seen_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    absent_complete_runs INTEGER NOT NULL DEFAULT 0,
    removed_at INTEGER,
    UNIQUE(site_id, raw_name)
);

CREATE INDEX raw_models_site_current_idx ON raw_models(site_id, removed_at, last_seen_at DESC);
CREATE INDEX raw_models_normalized_idx ON raw_models(normalized_name);

CREATE TABLE model_matches (
    raw_model_id INTEGER NOT NULL REFERENCES raw_models(id) ON DELETE CASCADE,
    rule_id INTEGER NOT NULL REFERENCES model_rules(id) ON DELETE CASCADE,
    is_primary INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1)),
    explanation TEXT NOT NULL,
    matched_at INTEGER NOT NULL,
    PRIMARY KEY(raw_model_id, rule_id)
) WITHOUT ROWID;

CREATE INDEX model_matches_rule_idx ON model_matches(rule_id, is_primary DESC, raw_model_id);

CREATE TABLE site_groups (
    id INTEGER PRIMARY KEY,
    raw_model_id INTEGER NOT NULL REFERENCES raw_models(id) ON DELETE CASCADE,
    raw_name TEXT NOT NULL,
    source_extension TEXT,
    first_seen_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    UNIQUE(raw_model_id, raw_name)
);

CREATE TABLE current_snapshots (
    group_id INTEGER PRIMARY KEY REFERENCES site_groups(id) ON DELETE CASCADE,
    run_id INTEGER,
    service_state TEXT NOT NULL,
    observed_at INTEGER NOT NULL,
    collected_at INTEGER NOT NULL,
    request_count INTEGER,
    success_count INTEGER,
    failure_count INTEGER,
    empty_count INTEGER,
    success_ratio REAL,
    average_latency_ms REAL,
    first_token_ms REAL,
    tokens_per_second REAL,
    source_extension TEXT
);

CREATE INDEX current_snapshots_state_idx ON current_snapshots(service_state, collected_at DESC);

CREATE TABLE metric_buckets (
    group_id INTEGER NOT NULL REFERENCES site_groups(id) ON DELETE CASCADE,
    bucket_start INTEGER NOT NULL,
    bucket_end INTEGER NOT NULL,
    resolution_seconds INTEGER NOT NULL,
    request_count INTEGER,
    success_count INTEGER,
    failure_count INTEGER,
    empty_count INTEGER,
    success_ratio REAL,
    average_latency_ms REAL,
    first_token_ms REAL,
    tokens_per_second REAL,
    collected_at INTEGER NOT NULL,
    PRIMARY KEY(group_id, bucket_start, resolution_seconds)
) WITHOUT ROWID;

CREATE INDEX metric_buckets_expiry_idx ON metric_buckets(bucket_start);

CREATE TABLE collection_runs (
    id INTEGER PRIMARY KEY,
    site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    adapter_key TEXT NOT NULL,
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    status TEXT NOT NULL,
    catalog_complete INTEGER NOT NULL DEFAULT 0 CHECK (catalog_complete IN (0, 1)),
    models_seen INTEGER NOT NULL DEFAULT 0,
    groups_seen INTEGER NOT NULL DEFAULT 0,
    error_code TEXT,
    error_message TEXT
);

CREATE INDEX collection_runs_site_time_idx ON collection_runs(site_id, started_at DESC);
CREATE INDEX collection_runs_expiry_idx ON collection_runs(started_at);

CREATE TABLE encrypted_sessions (
    site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL,
    key_version INTEGER NOT NULL,
    nonce BLOB NOT NULL,
    ciphertext BLOB NOT NULL,
    expires_at INTEGER,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY(site_id, purpose)
) WITHOUT ROWID;

CREATE TABLE operation_audit (
    id INTEGER PRIMARY KEY,
    occurred_at INTEGER NOT NULL,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id TEXT NOT NULL,
    outcome TEXT NOT NULL,
    details TEXT
);

CREATE INDEX operation_audit_expiry_idx ON operation_audit(occurred_at);

