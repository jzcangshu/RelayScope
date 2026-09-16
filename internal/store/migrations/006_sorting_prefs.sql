-- 定制页排序偏好：模型视图/站点视图各自记住的排序方式
ALTER TABLE user_preferences ADD COLUMN sorting TEXT NOT NULL DEFAULT '';
