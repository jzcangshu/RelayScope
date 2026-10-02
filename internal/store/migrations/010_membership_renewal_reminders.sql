-- 会员续费提醒去重表：同一 (user, 到期时间) 只提醒一次。
-- expires_at 记录的是提醒所针对的到期时间戳（毫秒）；用户续费后到期时间变化，会自然产生新的一次提醒。
CREATE TABLE IF NOT EXISTS membership_renewal_reminders (
    user_id    INTEGER NOT NULL REFERENCES users(id),
    expires_at INTEGER NOT NULL,
    sent_at    INTEGER NOT NULL,
    PRIMARY KEY (user_id, expires_at)
);
