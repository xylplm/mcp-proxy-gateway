DROP INDEX IF EXISTS idx_call_stat_called_at_id_desc;
ALTER TABLE call_stat DROP COLUMN IF EXISTS error_message;
ALTER TABLE call_stat DROP COLUMN IF EXISTS response_result;
ALTER TABLE call_stat DROP COLUMN IF EXISTS request_args;
