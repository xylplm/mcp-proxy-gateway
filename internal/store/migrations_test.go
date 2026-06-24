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
		"tool policy":                 toolPolicyRuleModel{}.TableName(),
		"api key":                     apiKeyModel{}.TableName(),
		"api key filter":              filterRuleAPIKeyModel{}.TableName(),
		"api key acl":                 apiKeyACLModel{}.TableName(),
		"tool cache":                  toolCacheModel{}.TableName(),
		"call stat daily":             callStatDailyModel{}.TableName(),
		"audit log":                   auditLogModel{}.TableName(),
	}
	want := map[string]string{
		"upstream":                    "upstream_mcp",
		"alias":                       "alias_rule",
		"alias upstream binding":      "alias_rule_upstream",
		"mcp filter":                  "filter_rule_mcp",
		"mcp filter upstream binding": "filter_rule_mcp_upstream",
		"tool policy":                 "tool_policy_rule",
		"api key":                     "api_key",
		"api key filter":              "filter_rule_apikey",
		"api key acl":                 "api_key_acl",
		"tool cache":                  "tool_cache",
		"call stat daily":             "call_stat_daily",
		"audit log":                   "audit_log",
	}
	for name, got := range tables {
		if got != want[name] {
			t.Errorf("%s 表名不一致，期望 %s，实际 %s", name, want[name], got)
		}
	}
}

// TestCallStatDailySchemaDDL 验证 Go 代码内维护的 call_stat_daily DDL 保留日聚合关键字段。
func TestCallStatDailySchemaDDL(t *testing.T) {
	checks := []string{
		"CREATE TABLE IF NOT EXISTS call_stat_daily",
		"stat_date                  DATE NOT NULL",
		"upstream_id                VARCHAR(36) NOT NULL DEFAULT ''",
		"api_key_id                 VARCHAR(36) NOT NULL DEFAULT ''",
		"total_calls                BIGINT NOT NULL DEFAULT 0",
		"latency_lt_50              BIGINT NOT NULL DEFAULT 0",
		"latency_gte_3000           BIGINT NOT NULL DEFAULT 0",
		"last_error_message         TEXT NOT NULL DEFAULT ''",
		"PRIMARY KEY (stat_date, source, mode, upstream_id, api_key_id, original_name)",
	}
	for _, check := range checks {
		if !strings.Contains(createCallStatDailyTableSQL, check) {
			t.Errorf("call_stat_daily DDL 缺少关键片段: %s", check)
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
