-- 回滚调用记录 source 列。
DROP INDEX IF EXISTS idx_call_stat_source;
ALTER TABLE call_stat DROP COLUMN IF EXISTS source;
