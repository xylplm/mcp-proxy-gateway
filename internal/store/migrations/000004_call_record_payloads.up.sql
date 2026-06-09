-- 调用记录详情字段：用于管理台查看单次工具调用的入参与出参。
ALTER TABLE call_stat ADD COLUMN IF NOT EXISTS request_args JSONB;
ALTER TABLE call_stat ADD COLUMN IF NOT EXISTS response_result JSONB;
ALTER TABLE call_stat ADD COLUMN IF NOT EXISTS error_message TEXT;

-- 支持调用记录页按最新时间倒序与增量拉取。
CREATE INDEX IF NOT EXISTS idx_call_stat_called_at_id_desc ON call_stat (called_at DESC, id DESC);
