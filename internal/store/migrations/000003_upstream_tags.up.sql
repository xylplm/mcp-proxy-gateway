-- 上游 MCP 标签，用于管理台分组与识别。
ALTER TABLE upstream_mcp ADD COLUMN tags TEXT[] NOT NULL DEFAULT '{}';
