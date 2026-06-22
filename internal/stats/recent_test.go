package stats

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

func TestRecentRecordViewTruncatesLargePayloads(t *testing.T) {
	large := json.RawMessage(`"` + strings.Repeat("x", maxRecentResponseJSONBytes+100) + `"`)
	rec := store.CallStatRecord{
		UpstreamID:     "up-1",
		UpstreamName:   "主上游",
		OriginalName:   "search",
		ExposedName:    "web_search",
		CalledAt:       time.Date(2024, 6, 1, 2, 3, 4, 0, time.UTC),
		LatencyMS:      42,
		Success:        true,
		RequestArgs:    json.RawMessage(`{"q":"hello"}`),
		ResponseResult: large,
		Mode:           "smart",
		Source:         "xiaozhi",
	}

	view := recentRecordView(7, rec, time.Now())
	if view.ID != 7 || view.Status != store.CallStatusSuccess || !view.Success {
		t.Fatalf("基础字段不符：%+v", view)
	}
	if view.Mode != "smart" || view.Source != "xiaozhi" || view.UpstreamName != "主上游" {
		t.Fatalf("展示/来源字段不符：%+v", view)
	}
	var payload map[string]any
	if err := json.Unmarshal(view.ResponseResult, &payload); err != nil {
		t.Fatalf("截断响应应为合法 JSON：%v", err)
	}
	if payload["truncated"] != true {
		t.Fatalf("超限响应应标记截断，实际 %s", view.ResponseResult)
	}
}

func TestSanitizeRecordPayloadsMakesInvalidJSONMarshalable(t *testing.T) {
	rec := sanitizeRecordPayloads(store.CallStatRecord{
		RequestArgs:    json.RawMessage(`{"ok":true}`),
		ResponseResult: json.RawMessage(`not-json`),
	})
	if !json.Valid(rec.RequestArgs) || !json.Valid(rec.ResponseResult) {
		t.Fatalf("清洗后的最近记录载荷必须可 JSON 序列化：request=%s response=%s", rec.RequestArgs, rec.ResponseResult)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.ResponseResult, &payload); err != nil {
		t.Fatalf("非法 JSON 摘要应可解析：%v", err)
	}
	if payload["invalidJSON"] != true {
		t.Fatalf("非法 JSON 应标记 invalidJSON，实际 %s", rec.ResponseResult)
	}
}

func TestFilterRecentCandidatesHonorsCursor(t *testing.T) {
	zs := []redis.Z{
		{Score: 1000, Member: recentMember(8)},
		{Score: 1000, Member: recentMember(7)},
		{Score: 1000, Member: recentMember(5)},
		{Score: 999, Member: recentMember(10)},
	}
	got := filterRecentCandidates(zs, 2, 6, 1000)
	if len(got) != 2 {
		t.Fatalf("应返回 2 条同毫秒新记录，实际 %d", len(got))
	}
	first, _ := parseRecentMember(got[0].Member.(string))
	second, _ := parseRecentMember(got[1].Member.(string))
	if first != 8 || second != 7 {
		t.Fatalf("游标过滤顺序不符：%d %d", first, second)
	}
}

func TestRecentMemberSortsLexicographicallyByID(t *testing.T) {
	if recentMember(9) >= recentMember(10) {
		t.Fatalf("零填充 member 应保持字典序等同数字序：%q >= %q", recentMember(9), recentMember(10))
	}
}
