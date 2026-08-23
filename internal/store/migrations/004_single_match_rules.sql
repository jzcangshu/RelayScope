-- More-specific monitored models must not also match their base model rule.
UPDATE model_rules
SET excluded_tokens = '["0731"]', updated_at = strftime('%s','now') * 1000
WHERE canonical_name IN ('deepseek-v4-flash', 'deepseek-v4-pro');

UPDATE model_rules
SET excluded_tokens = '["pro"]', updated_at = strftime('%s','now') * 1000
WHERE canonical_name = 'mimo-v2.5';
