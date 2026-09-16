-- 会员每月免费许愿额度：惰性发放，(user_id, period) 唯一保证每人每月仅一次
CREATE TABLE wish_credits (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    period TEXT NOT NULL,
    granted_ldc INTEGER NOT NULL CHECK (granted_ldc > 0),
    used_ldc INTEGER NOT NULL DEFAULT 0 CHECK (used_ldc >= 0 AND used_ldc <= granted_ldc),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, period)
) WITHOUT ROWID;

-- 标记订单资金来源：ldc = LDC 支付，credit = 每月免费额度
ALTER TABLE ldc_orders ADD COLUMN funding TEXT NOT NULL DEFAULT 'ldc' CHECK (funding IN ('ldc','credit'));
