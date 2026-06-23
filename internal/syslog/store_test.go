package syslog

import (
	"testing"
	"time"
)

func TestStoreKeepsBoundedRingAndIncrementalList(t *testing.T) {
	store := NewStore(2)
	store.Add("info", "one", time.Unix(1, 0), "", nil)
	second := store.Add("warn", "two", time.Unix(2, 0), "", nil)
	store.Add("error", "three", time.Unix(3, 0), "", nil)

	all := store.List(0, "", 10)
	if len(all) != 2 || all[0].Message != "two" || all[1].Message != "three" {
		t.Fatalf("环形缓冲应只保留最近两条，got=%+v", all)
	}
	next := store.List(second.ID, "error", 10)
	if len(next) != 1 || next[0].Message != "three" {
		t.Fatalf("增量过滤结果不符合预期：%+v", next)
	}
}

func TestStoreClear(t *testing.T) {
	store := NewStore(2)
	store.Add("info", "one", time.Now(), "", nil)
	if deleted := store.Clear(); deleted != 1 {
		t.Fatalf("删除条数应为 1，实际 %d", deleted)
	}
	if logs := store.List(0, "", 10); len(logs) != 0 {
		t.Fatalf("清空后不应再有日志：%+v", logs)
	}
}

func TestStoreExportFiltersAllBufferedEntries(t *testing.T) {
	store := NewStore(3)
	store.Add("info", "one", time.Unix(1, 0), "", nil)
	store.Add("warn", "two", time.Unix(2, 0), "", nil)
	store.Add("warn", "three", time.Unix(3, 0), "", map[string]any{"code": "w3"})

	got := store.Export("warn")
	if len(got) != 2 || got[0].Message != "two" || got[1].Message != "three" {
		t.Fatalf("导出级别过滤结果不符合预期：%+v", got)
	}
	got[1].Attrs["code"] = "changed"
	again := store.Export("warn")
	if again[1].Attrs["code"] != "w3" {
		t.Fatalf("导出结果应为副本，实际 attrs=%+v", again[1].Attrs)
	}
}

func TestStorePreservesSource(t *testing.T) {
	store := NewStore(10)
	store.Add("info", "one", time.Unix(1, 0), "service/subscribe_service.go:196", nil)
	store.Add("info", "two", time.Unix(2, 0), "", nil)

	all := store.List(0, "", 10)
	if len(all) != 2 {
		t.Fatalf("期望 2 条，实际 %d", len(all))
	}
	if all[0].Source != "service/subscribe_service.go:196" {
		t.Errorf("第一条 source 应保留，实际 %q", all[0].Source)
	}
	if all[1].Source != "" {
		t.Errorf("第二条 source 应为空，实际 %q", all[1].Source)
	}
}
