package syslog

import (
	"testing"
	"time"
)

func TestStoreKeepsBoundedRingAndIncrementalList(t *testing.T) {
	store := NewStore(2)
	store.Add("info", "one", time.Unix(1, 0), nil)
	second := store.Add("warn", "two", time.Unix(2, 0), nil)
	store.Add("error", "three", time.Unix(3, 0), nil)

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
	store.Add("info", "one", time.Now(), nil)
	if deleted := store.Clear(); deleted != 1 {
		t.Fatalf("删除条数应为 1，实际 %d", deleted)
	}
	if logs := store.List(0, "", 10); len(logs) != 0 {
		t.Fatalf("清空后不应再有日志：%+v", logs)
	}
}
