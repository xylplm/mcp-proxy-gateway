-- 回滚 API Key 明文存储：移除 key_plain 列。
ALTER TABLE api_key DROP COLUMN IF EXISTS key_plain;
