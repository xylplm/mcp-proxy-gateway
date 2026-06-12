-- 调用记录增加 mode 列，记录调用使用的 MCP 模式（full/smart）。
ALTER TABLE call_stat ADD COLUMN mode VARCHAR(16) NOT NULL DEFAULT 'full';

-- 索引：按模式维度查询。
CREATE INDEX idx_call_stat_mode ON call_stat (mode);
