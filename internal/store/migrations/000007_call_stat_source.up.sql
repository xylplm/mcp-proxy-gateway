-- 调用记录增加 source 列，区分调用来源（api/xiaozhi）。
ALTER TABLE call_stat ADD COLUMN source VARCHAR(16) NOT NULL DEFAULT 'api';

-- 索引：按来源维度查询。
CREATE INDEX idx_call_stat_source ON call_stat (source);
