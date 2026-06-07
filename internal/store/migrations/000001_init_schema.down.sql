-- 初始 schema 迁移（向下）：按依赖逆序删除所有表。
-- 删除分区父表会一并删除其下所有分区（含默认分区）。
-- 子表通过外键依赖父表，因此先删子表再删父表；为稳妥起见统一使用 IF EXISTS。

DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS call_stat;          -- 级联删除其全部分区（含 call_stat_default）
DROP TABLE IF EXISTS tool_cache;
DROP TABLE IF EXISTS api_key_acl;
DROP TABLE IF EXISTS filter_rule_apikey;
DROP TABLE IF EXISTS filter_rule_mcp;
DROP TABLE IF EXISTS alias_rule;
DROP TABLE IF EXISTS api_key;
DROP TABLE IF EXISTS upstream_mcp;
