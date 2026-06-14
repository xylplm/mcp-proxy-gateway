package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 本文件以内存 fake 注入 StatsService，验证统计查询端点（按上游/按 API Key/工具排行）
// 的路由装配、时间区间与 limit 参数解析、错误映射，无需接真实仓储。

// fakeStats 是 StatsService 的内存实现，记录最近一次入参以便断言。
type fakeStats struct {
	upstreamCounts []store.DimensionCount
	apiKeyCounts   []store.DimensionCount
	topTools       []store.ToolRank
	summary        store.StatsSummary
	daily          []store.DailyCount
	topErrors      []store.ToolErrorRank
	callRecords    []store.CallRecordView
	callRecord     store.CallRecordView
	err            error

	gotStart    time.Time
	gotEnd      time.Time
	gotTZ       string
	gotLimit    int
	gotAfterID  int64
	gotAfterAt  time.Time
	gotRecordID int64
	cleared     bool
}

func (f *fakeStats) CountByUpstream(_ context.Context, start, end time.Time) ([]store.DimensionCount, error) {
	f.gotStart, f.gotEnd = start, end
	if f.err != nil {
		return nil, f.err
	}
	return f.upstreamCounts, nil
}

func (f *fakeStats) CountByAPIKey(_ context.Context, start, end time.Time) ([]store.DimensionCount, error) {
	f.gotStart, f.gotEnd = start, end
	if f.err != nil {
		return nil, f.err
	}
	return f.apiKeyCounts, nil
}

func (f *fakeStats) TopTools(_ context.Context, start, end time.Time, limit int) ([]store.ToolRank, error) {
	f.gotStart, f.gotEnd, f.gotLimit = start, end, limit
	if f.err != nil {
		return nil, f.err
	}
	return f.topTools, nil
}

func (f *fakeStats) Summary(_ context.Context, start, end time.Time) (store.StatsSummary, error) {
	f.gotStart, f.gotEnd = start, end
	if f.err != nil {
		return store.StatsSummary{}, f.err
	}
	return f.summary, nil
}

func (f *fakeStats) Daily(_ context.Context, start, end time.Time, tz string) ([]store.DailyCount, error) {
	f.gotStart, f.gotEnd, f.gotTZ = start, end, tz
	if f.err != nil {
		return nil, f.err
	}
	return f.daily, nil
}

func (f *fakeStats) TopToolErrors(_ context.Context, start, end time.Time, limit int) ([]store.ToolErrorRank, error) {
	f.gotStart, f.gotEnd, f.gotLimit = start, end, limit
	if f.err != nil {
		return nil, f.err
	}
	return f.topErrors, nil
}

func (f *fakeStats) ListRecords(_ context.Context, limit int, afterID int64, afterAt time.Time) ([]store.CallRecordView, error) {
	f.gotLimit, f.gotAfterID, f.gotAfterAt = limit, afterID, afterAt
	if f.err != nil {
		return nil, f.err
	}
	return f.callRecords, nil
}

func (f *fakeStats) GetRecord(_ context.Context, id int64) (store.CallRecordView, error) {
	f.gotRecordID = id
	if f.err != nil {
		return store.CallRecordView{}, f.err
	}
	return f.callRecord, nil
}

func (f *fakeStats) ClearRecords(_ context.Context) (int64, error) {
	f.cleared = true
	if f.err != nil {
		return 0, f.err
	}
	return 12, nil
}

// TestStatsByUpstream 验证按上游统计端点返回计数并解析时间区间。
func TestStatsByUpstream(t *testing.T) {
	st := &fakeStats{upstreamCounts: []store.DimensionCount{{ID: "up-1", Count: 5}}}
	e := newTestEngine(Deps{Stats: st})

	w := doJSON(e, http.MethodGet, "/api/admin/stats/upstreams?start=2024-01-01T00:00:00Z&end=2024-02-01T00:00:00Z", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	var got struct {
		Counts []store.DimensionCount `json:"counts"`
	}
	unmarshalData(t, w, &got)
	if len(got.Counts) != 1 || got.Counts[0].ID != "up-1" {
		t.Errorf("统计结果不符：%+v", got.Counts)
	}
	if !st.gotStart.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("start 未正确解析：%v", st.gotStart)
	}
}

// TestStatsByAPIKey 验证按 API Key 统计端点返回计数。
func TestStatsByAPIKey(t *testing.T) {
	st := &fakeStats{apiKeyCounts: []store.DimensionCount{{ID: "key-1", Count: 9}}}
	e := newTestEngine(Deps{Stats: st})

	w := doJSON(e, http.MethodGet, "/api/admin/stats/apikeys", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	var got struct {
		Counts []store.DimensionCount `json:"counts"`
	}
	unmarshalData(t, w, &got)
	if len(got.Counts) != 1 || got.Counts[0].Count != 9 {
		t.Errorf("统计结果不符：%+v", got.Counts)
	}
}

// TestStatsTopToolsParsesLimit 验证工具排行端点解析 limit 并透传给统计服务（Req 16.3）。
func TestStatsTopToolsParsesLimit(t *testing.T) {
	st := &fakeStats{topTools: []store.ToolRank{{UpstreamID: "up", OriginalName: "t1", Count: 3}}}
	e := newTestEngine(Deps{Stats: st})

	w := doJSON(e, http.MethodGet, "/api/admin/stats/tools?limit=5", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	if st.gotLimit != 5 {
		t.Errorf("期望 limit=5 透传给服务，实际 %d", st.gotLimit)
	}
	var got struct {
		Tools []store.ToolRank `json:"tools"`
	}
	unmarshalData(t, w, &got)
	if len(got.Tools) != 1 {
		t.Errorf("排行结果不符：%+v", got.Tools)
	}
}

// TestStatsTopToolsDefaultLimit 验证缺省 limit 时以 0 透传（由服务取默认值）。
func TestStatsTopToolsDefaultLimit(t *testing.T) {
	st := &fakeStats{}
	e := newTestEngine(Deps{Stats: st})

	w := doJSON(e, http.MethodGet, "/api/admin/stats/tools", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	if st.gotLimit != 0 {
		t.Errorf("缺省 limit 期望以 0 透传，实际 %d", st.gotLimit)
	}
}

func TestStatsSummary(t *testing.T) {
	st := &fakeStats{summary: store.StatsSummary{TotalCalls: 12, FailureCalls: 2}}
	e := newTestEngine(Deps{Stats: st})

	w := doJSON(e, http.MethodGet, "/api/admin/stats/summary", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	var got struct {
		Summary store.StatsSummary `json:"summary"`
	}
	unmarshalData(t, w, &got)
	if got.Summary.TotalCalls != 12 || got.Summary.FailureCalls != 2 {
		t.Errorf("概览结果不符：%+v", got.Summary)
	}
}

func TestStatsDaily(t *testing.T) {
	day := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	st := &fakeStats{daily: []store.DailyCount{{Day: day, TotalCalls: 7}}}
	e := newTestEngine(Deps{Stats: st})

	w := doJSON(e, http.MethodGet, "/api/admin/stats/daily", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	var got struct {
		Days []store.DailyCount `json:"days"`
	}
	unmarshalData(t, w, &got)
	if len(got.Days) != 1 || got.Days[0].TotalCalls != 7 {
		t.Errorf("每日趋势结果不符：%+v", got.Days)
	}
	// tz 缺省时透传空串，后端回退 UTC，保持向后兼容。
	if st.gotTZ != "" {
		t.Errorf("缺省 tz 期望透传空串，实际 %q", st.gotTZ)
	}
}

// TestStatsDailyForwardsTimezone 验证 tz 查询参数透传到统计服务，供后端按本地时区分组。
func TestStatsDailyForwardsTimezone(t *testing.T) {
	st := &fakeStats{daily: []store.DailyCount{}}
	e := newTestEngine(Deps{Stats: st})

	w := doJSON(e, http.MethodGet, "/api/admin/stats/daily?tz=Asia%2FShanghai", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	if st.gotTZ != "Asia/Shanghai" {
		t.Errorf("tz 未透传：期望 Asia/Shanghai，实际 %q", st.gotTZ)
	}
}

func TestStatsTopToolErrorsParsesLimit(t *testing.T) {
	st := &fakeStats{topErrors: []store.ToolErrorRank{{UpstreamID: "up", OriginalName: "t1", FailureCalls: 3}}}
	e := newTestEngine(Deps{Stats: st})

	w := doJSON(e, http.MethodGet, "/api/admin/stats/tool-errors?limit=8", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	if st.gotLimit != 8 {
		t.Errorf("期望 limit=8 透传给服务，实际 %d", st.gotLimit)
	}
	var got struct {
		Tools []store.ToolErrorRank `json:"tools"`
	}
	unmarshalData(t, w, &got)
	if len(got.Tools) != 1 || got.Tools[0].FailureCalls != 3 {
		t.Errorf("错误排行结果不符：%+v", got.Tools)
	}
}

func TestStatsCallRecordsParsesRealtimeCursor(t *testing.T) {
	afterAt := "2024-05-01T10:00:00Z"
	calledAt := time.Date(2024, 5, 1, 10, 1, 0, 0, time.UTC)
	st := &fakeStats{callRecords: []store.CallRecordView{{ID: 12, UpstreamName: "mcp", ExposedName: "tool", CalledAt: calledAt}}}
	e := newTestEngine(Deps{Stats: st})

	w := doJSON(e, http.MethodGet, "/api/admin/stats/calls?limit=11&afterId=9&afterAt="+afterAt, "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if st.gotLimit != 11 || st.gotAfterID != 9 {
		t.Fatalf("调用记录游标参数未透传：limit=%d afterID=%d", st.gotLimit, st.gotAfterID)
	}
	wantAfterAt, _ := time.Parse(time.RFC3339, afterAt)
	if !st.gotAfterAt.Equal(wantAfterAt) {
		t.Fatalf("afterAt 未正确解析：got=%v want=%v", st.gotAfterAt, wantAfterAt)
	}
	var got struct {
		Records []store.CallRecordView `json:"records"`
	}
	unmarshalData(t, w, &got)
	if len(got.Records) != 1 || got.Records[0].ID != 12 {
		t.Fatalf("调用记录结果不符：%+v", got.Records)
	}
}

func TestStatsCallRecordDetailParsesID(t *testing.T) {
	st := &fakeStats{callRecord: store.CallRecordView{ID: 42, ExposedName: "search"}}
	e := newTestEngine(Deps{Stats: st})

	w := doJSON(e, http.MethodGet, "/api/admin/stats/calls/42", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if st.gotRecordID != 42 {
		t.Fatalf("详情 ID 未透传：%d", st.gotRecordID)
	}
	var got struct {
		Record store.CallRecordView `json:"record"`
	}
	unmarshalData(t, w, &got)
	if got.Record.ID != 42 || got.Record.ExposedName != "search" {
		t.Fatalf("调用详情结果不符：%+v", got.Record)
	}
}

func TestStatsCallRecordsCanBeCleared(t *testing.T) {
	st := &fakeStats{}
	e := newTestEngine(Deps{Stats: st})

	w := doJSON(e, http.MethodDelete, "/api/admin/stats/calls", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if !st.cleared {
		t.Fatal("应调用 ClearRecords")
	}
	var got struct {
		Deleted int64 `json:"deleted"`
	}
	unmarshalData(t, w, &got)
	if got.Deleted != 12 {
		t.Fatalf("删除条数未回填，got=%d", got.Deleted)
	}
}

// TestStatsInvalidTimeMapsTo400 验证非法时间参数返回字段级 400。
func TestStatsInvalidTimeMapsTo400(t *testing.T) {
	st := &fakeStats{}
	e := newTestEngine(Deps{Stats: st})

	w := doJSON(e, http.MethodGet, "/api/admin/stats/upstreams?start=not-a-time", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法时间期望 HTTP 400，实际 %d", w.Code)
	}
}

// TestStatsInvalidLimitMapsTo400 验证非整数 limit 返回字段级 400。
func TestStatsInvalidLimitMapsTo400(t *testing.T) {
	st := &fakeStats{}
	e := newTestEngine(Deps{Stats: st})

	w := doJSON(e, http.MethodGet, "/api/admin/stats/tools?limit=abc", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 limit 期望 HTTP 400，实际 %d", w.Code)
	}
}

// TestStatsRangeValidationFromService 验证下层「开始晚于结束」VALIDATION 被映射为 400（Req 16.7）。
func TestStatsRangeValidationFromService(t *testing.T) {
	st := &fakeStats{err: domain.NewError(domain.CodeValidation, "开始时间晚于结束时间")}
	e := newTestEngine(Deps{Stats: st})

	w := doJSON(e, http.MethodGet, "/api/admin/stats/upstreams?start=2024-02-01T00:00:00Z&end=2024-01-01T00:00:00Z", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法区间期望 HTTP 400，实际 %d", w.Code)
	}
}

// TestStatsServiceUnavailable 验证依赖未接线时返回 503。
func TestStatsServiceUnavailable(t *testing.T) {
	e := newTestEngine(Deps{})

	w := doJSON(e, http.MethodGet, "/api/admin/stats/tools", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("依赖未接线期望 HTTP 503，实际 %d", w.Code)
	}
}
