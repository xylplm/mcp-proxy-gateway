package store

import (
	"io/fs"
	"strings"
	"testing"
)

// TestMigrationsFSContainsScripts 验证内嵌文件系统能读取到迁移脚本，
// 且每个 *.up.sql 都有成对的 *.down.sql（golang-migrate 版本化迁移要求成对存在）。
func TestMigrationsFSContainsScripts(t *testing.T) {
	entries, err := fs.ReadDir(MigrationsFS(), "migrations")
	if err != nil {
		t.Fatalf("读取内嵌 migrations 目录失败: %v", err)
	}

	ups := map[string]bool{}
	downs := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".up.sql"):
			ups[strings.TrimSuffix(name, ".up.sql")] = true
		case strings.HasSuffix(name, ".down.sql"):
			downs[strings.TrimSuffix(name, ".down.sql")] = true
		}
	}

	if len(ups) == 0 {
		t.Fatal("未发现任何 *.up.sql 迁移脚本")
	}

	for version := range ups {
		if !downs[version] {
			t.Errorf("迁移 %q 缺少对应的 .down.sql 脚本", version)
		}
	}
	for version := range downs {
		if !ups[version] {
			t.Errorf("迁移 %q 缺少对应的 .up.sql 脚本", version)
		}
	}
}

// TestInitMigrationDefinesCoreTables 验证初始向上迁移包含设计文档要求的全部核心表、
// call_stat 的时间分区声明以及关键索引（Req 23.3、16.10）。
func TestInitMigrationDefinesCoreTables(t *testing.T) {
	data, err := fs.ReadFile(MigrationsFS(), "migrations/000001_init_schema.up.sql")
	if err != nil {
		t.Fatalf("读取初始迁移脚本失败: %v", err)
	}
	sql := string(data)

	tables := []string{
		"upstream_mcp", "alias_rule", "filter_rule_mcp", "api_key",
		"filter_rule_apikey", "api_key_acl", "tool_cache", "call_stat", "audit_log",
	}
	for _, tbl := range tables {
		if !strings.Contains(sql, "CREATE TABLE "+tbl) {
			t.Errorf("初始迁移缺少表定义: %s", tbl)
		}
	}

	// call_stat 必须按 called_at 时间范围分区，且建有默认分区以保证迁移后即可写入。
	if !strings.Contains(sql, "PARTITION BY RANGE (called_at)") {
		t.Error("call_stat 缺少按 called_at 的范围分区声明")
	}
	if !strings.Contains(sql, "PARTITION OF call_stat DEFAULT") {
		t.Error("call_stat 缺少默认分区，迁移后可能无法写入统计记录")
	}

	// design.md 标注的 call_stat 关键索引。
	indexCols := []string{
		"(called_at)",
		"(upstream_id, called_at)",
		"(api_key_id, called_at)",
		"(upstream_id, original_name)",
	}
	for _, cols := range indexCols {
		if !strings.Contains(sql, cols) {
			t.Errorf("call_stat 缺少关键索引列组合: %s", cols)
		}
	}

	// 级联删除外键约束。
	if !strings.Contains(sql, "ON DELETE CASCADE") {
		t.Error("缺少 ON DELETE CASCADE 外键约束")
	}
}
