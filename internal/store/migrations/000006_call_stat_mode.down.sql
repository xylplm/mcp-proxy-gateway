-- 回滚调用记录 mode 列。
DROP INDEX IF EXISTS idx_call_stat_mode;
ALTER TABLE call_stat DROP COLUMN IF EXISTS mode;
