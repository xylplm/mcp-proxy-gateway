DROP INDEX IF EXISTS idx_call_stat_status_called_at;
ALTER TABLE call_stat DROP COLUMN IF EXISTS failure_detail;
ALTER TABLE call_stat DROP COLUMN IF EXISTS status;
