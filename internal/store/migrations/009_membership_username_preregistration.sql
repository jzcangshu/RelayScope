-- 管理员按 LinuxDO 用户名预登记会员；用户登录后自动匹配并消耗预登记记录。
CREATE TABLE membership_preregistrations (
    provider TEXT NOT NULL,
    username TEXT NOT NULL,
    username_key TEXT NOT NULL,
    membership_expires_at INTEGER NOT NULL CHECK (membership_expires_at > 0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(provider, username_key)
);
CREATE INDEX membership_preregistrations_expiry_idx
    ON membership_preregistrations(membership_expires_at DESC);
