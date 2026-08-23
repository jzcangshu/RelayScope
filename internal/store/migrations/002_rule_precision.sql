-- Keep seeded rules precise when upgrading an existing runtime database.
UPDATE model_rules
SET pattern = '(?i)(^|[^0-9])glm[^0-9]+5([^0-9.]|$)', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'glm-5';
UPDATE model_rules
SET pattern = '(?i)glm[^0-9]+5[._-]1([^0-9]|$)', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'glm-5.1';
UPDATE model_rules
SET pattern = '(?i)glm[^0-9]+5[._-]2([^0-9]|$)', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'glm-5.2';
UPDATE model_rules
SET pattern = '(?i)claude[^a-z0-9]+opus[^0-9]+4[._-]6([^0-9]|$)', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'claude-opus-4-6';
UPDATE model_rules
SET pattern = '(?i)claude[^a-z0-9]+opus[^0-9]+4[._-]7([^0-9]|$)', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'claude-opus-4-7';
UPDATE model_rules
SET pattern = '(?i)claude[^a-z0-9]+opus[^0-9]+4[._-]8([^0-9]|$)', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'claude-opus-4-8';
UPDATE model_rules
SET pattern = '(?i)claude[^a-z0-9]+opus[^0-9]+5([^0-9]|$)', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'claude-opus-5';
UPDATE model_rules
SET pattern = '(?i)claude[^a-z0-9]+sonnet[^0-9]+4[._-]6([^0-9]|$)', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'claude-sonnet-4-6';
UPDATE model_rules
SET pattern = '(?i)claude[^a-z0-9]+sonnet[^0-9]+4[._-]7([^0-9]|$)', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'claude-sonnet-4-7';
UPDATE model_rules
SET pattern = '(?i)claude[^a-z0-9]+sonnet[^0-9]+4[._-]8([^0-9]|$)', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'claude-sonnet-4-8';
UPDATE model_rules
SET pattern = '(?i)claude[^a-z0-9]+sonnet[^0-9]+5([^0-9]|$)', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'claude-sonnet-5';
UPDATE model_rules
SET pattern = '(?i)claude[^a-z0-9]+haiku[^0-9]+4[._-]6([^0-9]|$)', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'claude-haiku-4-6';
UPDATE model_rules
SET pattern = '(?i)claude[^a-z0-9]+haiku[^0-9]+4[._-]7([^0-9]|$)', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'claude-haiku-4-7';
UPDATE model_rules
SET pattern = '(?i)claude[^a-z0-9]+haiku[^0-9]+4[._-]8([^0-9]|$)', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'claude-haiku-4-8';
UPDATE model_rules
SET pattern = '(?i)claude[^a-z0-9]+haiku[^0-9]+5([^0-9]|$)', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'claude-haiku-5';
