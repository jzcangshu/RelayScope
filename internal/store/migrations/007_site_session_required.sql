ALTER TABLE sites ADD COLUMN session_required INTEGER NOT NULL DEFAULT 0 CHECK (session_required IN (0, 1));

-- These seeded origins cannot provide their configured catalog without login.
UPDATE sites SET session_required = 1
WHERE base_url IN (
  'https://muyuan.do',
  'https://new-api.abrdns.com',
  'https://api.fengwind.com'
);
