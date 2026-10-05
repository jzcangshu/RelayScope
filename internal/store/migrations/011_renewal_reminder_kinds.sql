-- 续费提醒分两档：expiring（到期前 3 天内）与 expiry_day（到期当天）。
-- kind 并入主键，同一 (user, 到期时间, 档位) 只发一次；用户续费改变到期时间后仍会自然产生新一轮提醒。
CREATE TABLE IF NOT EXISTS membership_renewal_reminders_new (
    user_id    INTEGER NOT NULL REFERENCES users(id),
    expires_at INTEGER NOT NULL,
    sent_at    INTEGER NOT NULL,
    kind       TEXT NOT NULL DEFAULT 'expiring',
    PRIMARY KEY (user_id, expires_at, kind)
);
INSERT INTO membership_renewal_reminders_new (user_id, expires_at, sent_at, kind)
    SELECT user_id, expires_at, sent_at, 'expiring' FROM membership_renewal_reminders;
DROP TABLE membership_renewal_reminders;
ALTER TABLE membership_renewal_reminders_new RENAME TO membership_renewal_reminders;
