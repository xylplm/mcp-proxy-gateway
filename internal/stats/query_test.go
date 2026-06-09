package stats

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 本文件为任务 17.2「实现多维度统计与排行查询」的单元测试，覆盖以下核心行为
// （Req 16.2、16.3、16.4、16.5、16.6、16.7）：
//   - 按上游 MCP / API Key 维度统计区间内调用条数（含成功失败），透传仓储结果；
//   - 工具排行返回条数收敛：默认 10、范围 1-100，limit ≤ 0 取配置默认值；
//   - 闭区间时间过滤与「开始晚于结束返回校验错误」由仓储层保证，本层正确透传；
//   - 无记录时返回空结果而非错误。
//
// 测试以内存 fake（fakeQuerier / fakeQueryCfg）替换仓储与配置，脱离真实基础设施验证逻辑。

// --- 测试替身 ---

// fakeQuerier 是 StatQuerier 的内存实现：记录各方法入参，并可注入返回值与错误。
type fakeQuerier struct {
	upstreamCounts []store.DimensionCount
	apiKeyCounts   []store.DimensionCount
	topTools       []store.ToolRank
	summary        store.StatsSummary
	daily          []store.DailyCount
	topErrors      []store.ToolErrorRank

	upstreamErr error
	apiKeyErr   error
	topErr      error
	summaryErr  error
	dailyErr    error
	errorErr    error

	// 记录最近一次各方法收到的入参，用于断言透传与 limit 收敛。
	lastStart    time.Time
	lastEnd      time.Time
	lastTopLimit int
}

func (q *fakeQuerier) CountByUpstream(_ context.Context, start, end time.Time) ([]store.DimensionCount, error) {
	q.lastStart, q.lastEnd = start, end
	if q.upstreamErr != nil {
		return nil, q.upstreamErr
	}
	return q.upstreamCounts, nil
}

func (q *fakeQuerier) CountByAPIKey(_ context.Context, start, end time.Time) ([]store.DimensionCount, error) {
	q.lastStart, q.lastEnd = start, end
	if q.apiKeyErr != nil {
		return nil, q.apiKeyErr
	}
	return q.apiKeyCounts, nil
}

func (q *fakeQuerier) TopTools(_ context.Context, start, end time.Time, limit int) ([]store.ToolRank, error) {
	q.lastStart, q.lastEnd, q.lastTopLimit = start, end, limit
	if q.topErr != nil {
		return nil, q.topErr
	}
	return q.topTools, nil
}

func (q *fakeQuerier) Summary(_ context.Context, start, end time.Time) (store.StatsSummary, error) {
	q.lastStart, q.lastEnd = start, end
	if q.summaryErr != nil {
		return store.StatsSummary{}, q.summaryErr
	}
	return q.summary, nil
}

func (q *fakeQuerier) Daily(_ context.Context, start, end time.Time) ([]store.DailyCount, error) {
	q.lastStart, q.lastEnd = start, end
	if q.dailyErr != nil {
		return nil, q.dailyErr
	}
	return q.daily, nil
}

func (q *fakeQuerier) TopToolErrors(_ context.Context, start, end time.Time, limit int) ([]store.ToolErrorRank, error) {
	q.lastStart, q.lastEnd, q.lastTopLimit = start, end, limit
	if q.errorErr != nil {
		return nil, q.errorErr
	}
	return q.topErrors, nil
}

func (q *fakeQuerier) ListRecords(_ context.Context, _ int, _ int64, _ time.Time) ([]store.CallRecordView, error) {
	return nil, nil
}

func (q *fakeQuerier) GetRecord(_ context.Context, _ int64) (store.CallRecordView, error) {
	return store.CallRecordView{}, nil
}

// fakeQueryCfg 是 ConfigProvider 的内存实现：返回固定的工具排行默认条数。
type fakeQueryCfg struct {
	topLimitDefault int
}

func (c fakeQueryCfg) Config() config.YAMLConfig {
	var cfg config.YAMLConfig
	cfg.Statistics.TopLimitDefault = c.topLimitDefault
	return cfg
}

// newTestQueryService 构造一个注入了 fake 依赖的查询服务。
func newTestQueryService(t *testing.T, repo StatQuerier, topLimitDefault int) *QueryService {
	t.Helper()
	svc, err := NewQueryService(repo, fakeQueryCfg{topLimitDefault: topLimitDefault})
	if err != nil {
		t.Fatalf("NewQueryService 不应返回错误：%v", err)
	}
	return svc
}

// --- 构造校验 ---

// TestNewQueryServiceRejectsNilDeps 验证：依赖为空时构造返回校验错误。
func TestNewQueryServiceRejectsNilDeps(t *testing.T) {
	if _, err := NewQueryService(nil, fakeQueryCfg{}); err == nil {
		t.Error("repo 为空时应返回错误")
	}
	if _, err := NewQueryService(&fakeQuerier{}, nil); err == nil {
		t.Error("cfg 为空时应返回错误")
	}
}

// --- 维度统计 ---

// TestCountByUpstreamPassesThrough 验证：按上游维度统计透传仓储结果与时间区间（Req 16.2、16.5）。
func TestCountByUpstreamPassesThrough(t *testing.T) {
	want := []store.DimensionCount{{ID: "u1", Count: 7}, {ID: "u2", Count: 3}}
	repo := &fakeQuerier{upstreamCounts: want}
	svc := newTestQueryService(t, repo, 10)

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 31, 23, 59, 59, 0, time.UTC)
	got, err := svc.CountByUpstream(context.Background(), start, end)
	if err != nil {
		t.Fatalf("不应返回错误：%v", err)
	}
	if len(got) != 2 || got[0].ID != "u1" || got[0].Count != 7 {
		t.Errorf("结果未透传：%+v", got)
	}
	if !repo.lastStart.Equal(start) || !repo.lastEnd.Equal(end) {
		t.Errorf("时间区间未透传：start=%v end=%v", repo.lastStart, repo.lastEnd)
	}
}

// TestCountByAPIKeyPassesThrough 验证：按 API Key 维度统计透传仓储结果（Req 16.4、16.5）。
func TestCountByAPIKeyPassesThrough(t *testing.T) {
	want := []store.DimensionCount{{ID: "k1", Count: 5}}
	repo := &fakeQuerier{apiKeyCounts: want}
	svc := newTestQueryService(t, repo, 10)

	got, err := svc.CountByAPIKey(context.Background(), time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("不应返回错误：%v", err)
	}
	if len(got) != 1 || got[0].ID != "k1" || got[0].Count != 5 {
		t.Errorf("结果未透传：%+v", got)
	}
}

// TestCountReturnsEmptyWhenNoRecords 验证：无记录时返回空结果而非错误（Req 16.6）。
func TestCountReturnsEmptyWhenNoRecords(t *testing.T) {
	repo := &fakeQuerier{upstreamCounts: []store.DimensionCount{}, apiKeyCounts: []store.DimensionCount{}}
	svc := newTestQueryService(t, repo, 10)

	up, err := svc.CountByUpstream(context.Background(), time.Now().Add(-time.Hour), time.Now())
	if err != nil || len(up) != 0 {
		t.Errorf("无记录应返回空结果：got=%+v err=%v", up, err)
	}
	keys, err := svc.CountByAPIKey(context.Background(), time.Now().Add(-time.Hour), time.Now())
	if err != nil || len(keys) != 0 {
		t.Errorf("无记录应返回空结果：got=%+v err=%v", keys, err)
	}
}

// TestCountByUpstreamPropagatesRangeError 验证：开始晚于结束的范围错误由仓储返回并透传（Req 16.7）。
func TestCountByUpstreamPropagatesRangeError(t *testing.T) {
	rangeErr := domain.NewValidationError("统计时间范围无效：开始时间晚于结束时间", map[string]string{
		"start": "开始时间不得晚于结束时间",
	})
	repo := &fakeQuerier{upstreamErr: rangeErr}
	svc := newTestQueryService(t, repo, 10)

	_, err := svc.CountByUpstream(context.Background(), time.Now(), time.Now().Add(-time.Hour))
	if err == nil {
		t.Fatal("开始晚于结束时应返回错误")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeValidation {
		t.Errorf("应透传校验类错误：%v", err)
	}
}

// --- 工具排行条数收敛 ---

// TestTopToolsLimitResolution 验证：工具排行返回条数按「默认/下界/上界」收敛后下传仓储（Req 16.3）。
func TestTopToolsLimitResolution(t *testing.T) {
	cases := []struct {
		name            string
		topLimitDefault int
		inputLimit      int
		wantLimit       int
	}{
		{"零取配置默认", 10, 0, 10},
		{"负数取配置默认", 25, -5, 25},
		{"配置默认越界回退10", 0, 0, defaultTopLimit},
		{"配置默认上界越界回退10", 999, 0, defaultTopLimit},
		{"合法值原样下传", 10, 50, 50},
		{"下界1原样下传", 10, 1, 1},
		{"上界100原样下传", 10, 100, 100},
		{"超过上界收敛100", 10, 500, maxTopLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeQuerier{}
			svc := newTestQueryService(t, repo, tc.topLimitDefault)
			if _, err := svc.TopTools(context.Background(), time.Now().Add(-time.Hour), time.Now(), tc.inputLimit); err != nil {
				t.Fatalf("不应返回错误：%v", err)
			}
			if repo.lastTopLimit != tc.wantLimit {
				t.Errorf("limit 收敛错误：input=%d default=%d got=%d want=%d",
					tc.inputLimit, tc.topLimitDefault, repo.lastTopLimit, tc.wantLimit)
			}
		})
	}
}

// TestTopToolsPassesThrough 验证：工具排行结果与时间区间正确透传（Req 16.3、16.5）。
func TestTopToolsPassesThrough(t *testing.T) {
	want := []store.ToolRank{
		{UpstreamID: "u1", OriginalName: "search", Count: 100},
		{UpstreamID: "u2", OriginalName: "fetch", Count: 40},
	}
	repo := &fakeQuerier{topTools: want}
	svc := newTestQueryService(t, repo, 10)

	start := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC)
	got, err := svc.TopTools(context.Background(), start, end, 10)
	if err != nil {
		t.Fatalf("不应返回错误：%v", err)
	}
	if len(got) != 2 || got[0].OriginalName != "search" || got[0].Count != 100 {
		t.Errorf("结果未透传：%+v", got)
	}
	if !repo.lastStart.Equal(start) || !repo.lastEnd.Equal(end) {
		t.Errorf("时间区间未透传：start=%v end=%v", repo.lastStart, repo.lastEnd)
	}
}

// TestTopToolsReturnsEmptyWhenNoRecords 验证：无记录时排行返回空结果而非错误（Req 16.6）。
func TestTopToolsReturnsEmptyWhenNoRecords(t *testing.T) {
	repo := &fakeQuerier{topTools: []store.ToolRank{}}
	svc := newTestQueryService(t, repo, 10)

	got, err := svc.TopTools(context.Background(), time.Now().Add(-time.Hour), time.Now(), 10)
	if err != nil || len(got) != 0 {
		t.Errorf("无记录应返回空结果：got=%+v err=%v", got, err)
	}
}

// TestTopToolsPropagatesRangeError 验证：开始晚于结束的范围错误由仓储返回并透传（Req 16.7）。
func TestTopToolsPropagatesRangeError(t *testing.T) {
	repo := &fakeQuerier{topErr: domain.NewValidationError("统计时间范围无效", nil)}
	svc := newTestQueryService(t, repo, 10)

	if _, err := svc.TopTools(context.Background(), time.Now(), time.Now().Add(-time.Hour), 10); err == nil {
		t.Error("开始晚于结束时应返回错误")
	}
}

func TestSummaryDailyAndToolErrorsPassThrough(t *testing.T) {
	start := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 4, 30, 0, 0, 0, 0, time.UTC)
	repo := &fakeQuerier{
		summary:   store.StatsSummary{TotalCalls: 12, FailureCalls: 2},
		daily:     []store.DailyCount{{Day: start, TotalCalls: 3}},
		topErrors: []store.ToolErrorRank{{UpstreamID: "u1", OriginalName: "search", FailureCalls: 2}},
	}
	svc := newTestQueryService(t, repo, 10)

	summary, err := svc.Summary(context.Background(), start, end)
	if err != nil || summary.TotalCalls != 12 || summary.FailureCalls != 2 {
		t.Fatalf("Summary 未透传：summary=%+v err=%v", summary, err)
	}
	daily, err := svc.Daily(context.Background(), start, end)
	if err != nil || len(daily) != 1 || daily[0].TotalCalls != 3 {
		t.Fatalf("Daily 未透传：daily=%+v err=%v", daily, err)
	}
	errors, err := svc.TopToolErrors(context.Background(), start, end, 0)
	if err != nil || len(errors) != 1 || errors[0].FailureCalls != 2 {
		t.Fatalf("TopToolErrors 未透传：errors=%+v err=%v", errors, err)
	}
	if repo.lastTopLimit != 10 {
		t.Fatalf("TopToolErrors 应复用排行默认条数，实际 %d", repo.lastTopLimit)
	}
}
