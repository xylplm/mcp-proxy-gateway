ALTER TABLE call_stat ADD COLUMN IF NOT EXISTS status VARCHAR(32);
ALTER TABLE call_stat ADD COLUMN IF NOT EXISTS failure_detail JSONB;

UPDATE call_stat
SET status = CASE
    WHEN success THEN 'success'
    WHEN error_message IS NOT NULL AND error_message <> '' THEN 'failed'
    ELSE 'upstream_error'
END
WHERE status IS NULL;

ALTER TABLE call_stat ALTER COLUMN status SET DEFAULT 'success';
ALTER TABLE call_stat ALTER COLUMN status SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_call_stat_status_called_at
    ON call_stat (status, called_at DESC);
