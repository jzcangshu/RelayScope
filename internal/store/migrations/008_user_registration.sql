-- registered_at（注册时间）用于标记真实 LD 登录；新版用户名预登记存储在独立表中。
ALTER TABLE users ADD COLUMN registered_at INTEGER;
UPDATE users SET registered_at = created_at WHERE registered_at IS NULL;
