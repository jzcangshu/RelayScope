ALTER TABLE users ADD COLUMN trust_level INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN membership_expires_at INTEGER;

-- 用户会话落库：只存 token 的 SHA-256，重启后登录态不丢
CREATE TABLE user_sessions (
    token_hash TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX user_sessions_user_idx ON user_sessions(user_id);

-- 定制页云端偏好：一人一行，服务器只校验结构不解释语义
CREATE TABLE user_preferences (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    hidden TEXT NOT NULL DEFAULT '{"sites":[],"providers":[],"models":[]}',
    default_healthy INTEGER NOT NULL DEFAULT 0 CHECK (default_healthy IN (0, 1)),
    tags TEXT NOT NULL DEFAULT '{}',
    updated_at INTEGER NOT NULL
) WITHOUT ROWID;

-- 兑换码：站外售卖、站内兑换延期
CREATE TABLE redeem_codes (
    id INTEGER PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    days INTEGER NOT NULL CHECK (days BETWEEN 1 AND 3650),
    note TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'unused' CHECK (status IN ('unused','used','revoked')),
    redeemed_by INTEGER REFERENCES users(id),
    redeemed_at INTEGER,
    created_at INTEGER NOT NULL
);
CREATE INDEX redeem_codes_status_idx ON redeem_codes(status, id);

-- 许愿池：按归一化域名去重；需邀请码站点 target_ldc 为 NULL 表示"目标尚未确定"
CREATE TABLE wish_sites (
    id INTEGER PRIMARY KEY,
    domain TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    invite_required INTEGER NOT NULL CHECK (invite_required IN (0, 1)),
    target_ldc INTEGER CHECK (target_ldc IS NULL OR target_ldc > 0),
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','reached','rejected','connected')),
    created_by INTEGER NOT NULL REFERENCES users(id),
    created_at INTEGER NOT NULL,
    resolved_at INTEGER
);

-- LDC 订单：会员直充（kind=membership）与许愿助力（kind=wish）共用一张订单表
CREATE TABLE ldc_orders (
    id INTEGER PRIMARY KEY,
    order_no TEXT NOT NULL UNIQUE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('wish','membership')),
    wish_site_id INTEGER REFERENCES wish_sites(id) ON DELETE CASCADE,
    days INTEGER CHECK (days IS NULL OR days BETWEEN 1 AND 3650),
    amount_ldc INTEGER NOT NULL CHECK (amount_ldc > 0),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','paid','refunded','cancelled')),
    platform_trade_no TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    paid_at INTEGER
);
CREATE INDEX ldc_orders_site_idx ON ldc_orders(wish_site_id, status);
CREATE INDEX ldc_orders_user_idx ON ldc_orders(user_id, status);
CREATE INDEX ldc_orders_created_idx ON ldc_orders(created_at);
