-- 管理台可按 LinuxDO ID（LD 用户 ID）预登记会员；registered_at（注册时间）用于区分预登记和真实登录。
ALTER TABLE users ADD COLUMN registered_at INTEGER;
UPDATE users SET registered_at = created_at WHERE registered_at IS NULL;
