package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// newQueryService 构造一个使用内存仓储与指定默认页大小的审计服务，便于分页查询测试。
func newQueryService(t *testing.T, repo *testAuditRepo, pageSizeDefault int) *Service {
	t.Helper()
	svc, err := New(repo, fixedConfig{
		retentionDays:   defaultRetentionDays,
		pageSizeDefault: pageSizeDefault,
	})
	if err != nil {
		t.Fatalf("New 不应返回错误：%v", err)
	}
	svc.now = func() time.Time { return fixedNow }
	return svc
}

// seedRecords 向内存仓储写入 n 条登录审计，发生时间依次递增（第 i 条晚于第 i-1 条），
// 因此倒序排列后最新（OccurredAt 最大、ID 最大）者在前。返回写入后各记录的发生时间切片。
func seedRecords(repo *testAuditRepo, n int) []time.Time {
	times := make([]time.Time, 0, n)
	for i := range n {
		at := fixedNow.Add(time.Duration(i) * time.Minute)
		repo.rows = append(repo.rows, store.AuditRecord{
			ID:         int64(i + 1),
			EventType:  store.AuditEventLogin,
			OccurredAt: at,
		})
		times = append(times, at)
	}
	return times
}

func TestList_ReturnsRecordsInDescendingOrder(t *testing.T) {
	repo := &testAuditRepo{}
	seedRecords(repo, 5)
	svc := newQueryService(t, repo, defaultPageSize)

	res, err := svc.List(context.Background(), 1, 10, Query{})
	if err != nil {
		t.Fatalf("List 不应返回错误：%v", err)
	}
	if len(res.Records) != 5 {
		t.Fatalf("应返回 5 条记录，实际 %d 条", len(res.Records))
	}
	if res.Total != 5 {
		t.Errorf("总数应为 5，实际 %d", res.Total)
	}
	// 校验严格倒序：前一条的发生时间不早于后一条。
	for i := 1; i < len(res.Records); i++ {
		prev, cur := res.Records[i-1], res.Records[i]
		if cur.OccurredAt.After(prev.OccurredAt) {
			t.Fatalf("记录未按发生时间倒序：第 %d 条 %v 晚于第 %d 条 %v",
				i, cur.OccurredAt, i-1, prev.OccurredAt)
		}
	}
	// 最新记录（ID=5）应排在首位。
	if res.Records[0].ID != 5 {
		t.Errorf("倒序首条应为最新记录 ID=5，实际 ID=%d", res.Records[0].ID)
	}
}

func TestList_DefaultPageSizeWhenNonPositive(t *testing.T) {
	repo := &testAuditRepo{}
	seedRecords(repo, 30)
	svc := newQueryService(t, repo, 20)

	for _, size := range []int{0, -5} {
		res, err := svc.List(context.Background(), 1, size, Query{})
		if err != nil {
			t.Fatalf("List 不应返回错误：%v", err)
		}
		if res.PageSize != 20 {
			t.Errorf("pageSize=%d 时应回退配置默认 20，实际 %d", size, res.PageSize)
		}
		if len(res.Records) != 20 {
			t.Errorf("应返回默认 20 条，实际 %d 条", len(res.Records))
		}
	}
}

func TestList_DefaultPageSizeFallsBackWhenConfigOutOfRange(t *testing.T) {
	cases := []struct {
		name            string
		pageSizeDefault int
	}{
		{"配置低于下界", 0},
		{"配置高于上界", maxPageSize + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &testAuditRepo{}
			seedRecords(repo, 25)
			svc := newQueryService(t, repo, tc.pageSizeDefault)

			res, err := svc.List(context.Background(), 1, 0, Query{})
			if err != nil {
				t.Fatalf("List 不应返回错误：%v", err)
			}
			if res.PageSize != defaultPageSize {
				t.Errorf("配置越界时应回退硬编码默认 %d，实际 %d", defaultPageSize, res.PageSize)
			}
		})
	}
}

func TestList_PageSizeClampedToUpperBound(t *testing.T) {
	repo := &testAuditRepo{}
	seedRecords(repo, 5)
	svc := newQueryService(t, repo, defaultPageSize)

	res, err := svc.List(context.Background(), 1, maxPageSize+50, Query{})
	if err != nil {
		t.Fatalf("List 不应返回错误：%v", err)
	}
	if res.PageSize != maxPageSize {
		t.Errorf("超过上界应收敛为 %d，实际 %d", maxPageSize, res.PageSize)
	}
	if repo.lastPageSize != maxPageSize {
		t.Errorf("传入仓储的 pageSize 应收敛为 %d，实际 %d", maxPageSize, repo.lastPageSize)
	}
}

func TestList_PageNormalizedWhenNonPositive(t *testing.T) {
	repo := &testAuditRepo{}
	seedRecords(repo, 5)
	svc := newQueryService(t, repo, defaultPageSize)

	res, err := svc.List(context.Background(), 0, 10, Query{})
	if err != nil {
		t.Fatalf("List 不应返回错误：%v", err)
	}
	if res.Page != 1 {
		t.Errorf("page≤0 应归正为第 1 页，实际 %d", res.Page)
	}
	if repo.lastPage != 1 {
		t.Errorf("传入仓储的 page 应归正为 1，实际 %d", repo.lastPage)
	}
}

func TestList_EmptyResult(t *testing.T) {
	repo := &testAuditRepo{}
	svc := newQueryService(t, repo, defaultPageSize)

	res, err := svc.List(context.Background(), 1, 20, Query{})
	if err != nil {
		t.Fatalf("List 不应返回错误：%v", err)
	}
	if res.Records == nil {
		t.Error("空结果应返回非 nil 空切片")
	}
	if len(res.Records) != 0 {
		t.Errorf("空结果应返回 0 条，实际 %d 条", len(res.Records))
	}
	if res.Total != 0 {
		t.Errorf("空结果总数应为 0，实际 %d", res.Total)
	}
}

func TestList_PaginationOffset(t *testing.T) {
	repo := &testAuditRepo{}
	times := seedRecords(repo, 7) // ID 1..7，发生时间递增；倒序后 ID 7 在最前。
	svc := newQueryService(t, repo, defaultPageSize)

	// 第 1 页（每页 3 条）：应为 ID 7,6,5。
	page1, err := svc.List(context.Background(), 1, 3, Query{})
	if err != nil {
		t.Fatalf("List 第 1 页不应返回错误：%v", err)
	}
	wantPage1 := []int64{7, 6, 5}
	assertIDs(t, "第 1 页", page1.Records, wantPage1)

	// 第 2 页：应为 ID 4,3,2。
	page2, err := svc.List(context.Background(), 2, 3, Query{})
	if err != nil {
		t.Fatalf("List 第 2 页不应返回错误：%v", err)
	}
	assertIDs(t, "第 2 页", page2.Records, []int64{4, 3, 2})

	// 第 3 页：仅剩 ID 1（不足一页）。
	page3, err := svc.List(context.Background(), 3, 3, Query{})
	if err != nil {
		t.Fatalf("List 第 3 页不应返回错误：%v", err)
	}
	assertIDs(t, "第 3 页", page3.Records, []int64{1})

	// 第 4 页：越过末尾，返回空页且不报错。
	page4, err := svc.List(context.Background(), 4, 3, Query{})
	if err != nil {
		t.Fatalf("List 第 4 页不应返回错误：%v", err)
	}
	if len(page4.Records) != 0 {
		t.Errorf("越界页应返回空，实际 %d 条", len(page4.Records))
	}

	// 各页之间不重叠、不遗漏：合并三页应恰好覆盖全部 7 条，按倒序排列。
	merged := append(append(append([]store.AuditRecord{}, page1.Records...), page2.Records...), page3.Records...)
	if len(merged) != len(times) {
		t.Fatalf("分页合并应覆盖全部 %d 条，实际 %d 条", len(times), len(merged))
	}
	assertIDs(t, "合并全部页", merged, []int64{7, 6, 5, 4, 3, 2, 1})
}

func TestList_FiltersByTypeAndTimeRange(t *testing.T) {
	repo := &testAuditRepo{rows: []store.AuditRecord{
		{ID: 1, EventType: store.AuditEventLogin, OccurredAt: fixedNow.Add(-3 * time.Hour)},
		{ID: 2, EventType: store.AuditEventUpdate, OccurredAt: fixedNow.Add(-2 * time.Hour)},
		{ID: 3, EventType: store.AuditEventUpdate, OccurredAt: fixedNow.Add(-1 * time.Hour)},
		{ID: 4, EventType: store.AuditEventDelete, OccurredAt: fixedNow},
	}}
	svc := newQueryService(t, repo, defaultPageSize)

	res, err := svc.List(context.Background(), 1, 20, Query{
		EventType: store.AuditEventUpdate,
		Start:     fixedNow.Add(-150 * time.Minute),
		End:       fixedNow.Add(-30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("List 不应返回错误：%v", err)
	}
	if res.Total != 2 {
		t.Fatalf("过滤后的总数应为 2，实际 %d", res.Total)
	}
	assertIDs(t, "过滤结果", res.Records, []int64{3, 2})
	if repo.lastQuery.EventType != store.AuditEventUpdate {
		t.Fatalf("过滤条件未透传：%+v", repo.lastQuery)
	}
}

func TestList_PropagatesCountError(t *testing.T) {
	wantErr := errors.New("统计失败")
	repo := &testAuditRepo{countErr: wantErr}
	svc := newQueryService(t, repo, defaultPageSize)

	if _, err := svc.List(context.Background(), 1, 20, Query{}); !errors.Is(err, wantErr) {
		t.Fatalf("应透传 Count 错误，实际 %v", err)
	}
}

func TestList_PropagatesListError(t *testing.T) {
	wantErr := errors.New("查询失败")
	repo := &testAuditRepo{listErr: wantErr}
	svc := newQueryService(t, repo, defaultPageSize)

	if _, err := svc.List(context.Background(), 1, 20, Query{}); !errors.Is(err, wantErr) {
		t.Fatalf("应透传 List 错误，实际 %v", err)
	}
}

// assertIDs 断言记录序列的 ID 与期望一致（含顺序）。
func assertIDs(t *testing.T, label string, recs []store.AuditRecord, want []int64) {
	t.Helper()
	if len(recs) != len(want) {
		t.Fatalf("%s 应有 %d 条记录，实际 %d 条", label, len(want), len(recs))
	}
	for i, id := range want {
		if recs[i].ID != id {
			t.Errorf("%s 第 %d 条 ID 应为 %d，实际 %d", label, i, id, recs[i].ID)
		}
	}
}
