package store

import (
	"strings"
	"testing"
)

// TestGormModelsKeepCoreTableNames 验证 GORM model 显式绑定当前 schema 的核心表名。
func TestGormModelsKeepCoreTableNames(t *testing.T) {
	tables := map[string]string{
		"upstream":                    upstreamMCPModel{}.TableName(),
		"alias":                       aliasRuleModel{}.TableName(),
		"alias upstream binding":      aliasRuleUpstreamModel{}.TableName(),
		"mcp filter":                  filterRuleMCPModel{}.TableName(),
		"mcp filter upstream binding": filterRuleMCPUpstreamModel{}.TableName(),
		"api key":                     apiKeyModel{}.TableName(),
		"api key filter":              filterRuleAPIKeyModel{}.TableName(),
		"api key acl":                 apiKeyACLModel{}.TableName(),
		"tool cache":                  toolCacheModel{}.TableName(),
		"call stat":                   callStatModel{}.TableName(),
		"audit log":                   auditLogModel{}.TableName(),
	}
	want := map[string]string{
		"upstream":                    "upstream_mcp",
		"alias":                       "alias_rule",
		"alias upstream binding":      "alias_rule_upstream",
		"mcp filter":                  "filter_rule_mcp",
		"mcp filter upstream binding": "filter_rule_mcp_upstream",
		"api key":                     "api_key",
		"api key filter":              "filter_rule_apikey",
		"api key acl":                 "api_key_acl",
		"tool cache":                  "tool_cache",
		"call stat":                   "call_stat",
		"audit log":                   "audit_log",
	}
	for name, got := range tables {
		if got != want[name] {
			t.Errorf("%s 表名不一致，期望 %s，实际 %s", name, want[name], got)
		}
	}
}

// TestCallStatSchemaDDLKeepsPartitioning 验证 Go 代码内维护的 call_stat DDL 保留时间分区和关键字段。
func TestCallStatSchemaDDLKeepsPartitioning(t *testing.T) {
	checks := []string{
		"CREATE TABLE IF NOT EXISTS call_stat",
		"PARTITION BY RANGE (called_at)",
		"id              BIGSERIAL",
		"upstream_id     UUID",
		"request_args    JSONB",
		"response_result JSONB",
		"failure_detail  JSONB",
		"mode            VARCHAR(16) NOT NULL DEFAULT 'full'",
		"source          VARCHAR(16) NOT NULL DEFAULT 'api'",
		"PRIMARY KEY (id, called_at)",
	}
	for _, check := range checks {
		if !strings.Contains(createCallStatTableSQL, check) {
			t.Errorf("call_stat DDL 缺少关键片段: %s", check)
		}
	}
}

// TestSchemaExtrasKeepCascadeConstraints 验证额外 schema 初始化仍包含级联删除外键约束。
func TestSchemaExtrasKeepCascadeConstraints(t *testing.T) {
	constraints := []string{
		"alias_rule_upstream_rule_id_fkey",
		"alias_rule_upstream_upstream_id_fkey",
		"filter_rule_mcp_upstream_rule_id_fkey",
		"filter_rule_mcp_upstream_upstream_id_fkey",
		"filter_rule_apikey_api_key_id_fkey",
		"api_key_acl_api_key_id_fkey",
		"tool_cache_upstream_id_fkey",
	}
	for _, name := range constraints {
		found := false
		for _, constraint := range cascadeConstraintDefinitions() {
			if constraint.Name == name && strings.Contains(constraint.SQL, "ON DELETE CASCADE") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("缺少级联删除约束定义: %s", name)
		}
	}
}
