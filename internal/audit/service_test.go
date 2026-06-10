package audit

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// testAuditRepo 是 AuditRepository 窄接口的内存实现，便于在不触碰真实数据库的情况下
// 验证审计服务的事件记录与保留期清理逻辑。
type testAuditRepo struct {
	rows   []store.AuditRecord // 已写入的审计记录，按写入顺序保存。
	nextID int64               // 自增计数，生成确定性 ID。

	// insertErr 用于注入 Insert 失败，验证错误透传。
	insertErr error
	// deleteErr 用于注入 DeleteOlderThan 失败，验证错误透传。
	deleteErr error
	// lastCutoff 记录最近一次清理传入的截止时间，供断言保留期边界。
	lastCutoff time.Time
	// deleteCalls 记录 DeleteOlderThan 被调用的次数。
	deleteCalls int

	// listErr 用于注入 List 失败，验证错误透传。
	listErr error
	// countErr 用于注入 Count 失败，验证错误透传。
	countErr error
	// listCalls / countCalls 记录分页查询方法的调用次数。
	listCalls  int
	countCalls int
	// lastPage / lastPageSize 记录最近一次 List 传入的分页参数，供断言收敛行为。
	lastPage     int
	lastPageSize int
	lastQuery    Query
}

func (r *testAuditRepo) Insert(_ context.Context, rec store.AuditRecord) (store.AuditRecord, error) {
	if r.insertErr != nil {
		return store.AuditRecord{}, r.insertErr
	}
	r.nextID++
	rec.ID = r.nextID
	if rec.OccurredAt.IsZero() {
		rec.OccurredAt = time.Unix(0, 0).UTC()
	}
	r.rows = append(r.rows, rec)
	return rec, nil
}

func (r *testAuditRepo) DeleteOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	r.deleteCalls++
	r.lastCutoff = cutoff
	if r.deleteErr != nil {
		return 0, r.deleteErr
	}
	kept := r.rows[:0]
	var deleted int64
	for _, rec := range r.rows {
		if rec.OccurredAt.Before(cutoff) {
			deleted++
			continue
		}
		kept = append(kept, rec)
	}
	r.rows = kept
	return deleted, nil
}

// List 模拟仓储的倒序分页查询：按 OccurredAt 倒序、相同则按 ID 倒序，再套用偏移与限长。
// 该实现与 *store.AuditRepo.List 的排序/分页语义一致，便于在内存中验证审计服务的分页逻辑。
func (r *testAuditRepo) List(_ context.Context, page, pageSize int, query Query) ([]store.AuditRecord, error) {
	r.listCalls++
	r.lastPage = page
	r.lastPageSize = pageSize
	r.lastQuery = query
	if r.listErr != nil {
		return nil, r.listErr
	}
	if pageSize <= 0 {
		pageSize = 1
	}
	if page <= 0 {
		page = 1
	}

	sorted := r.filtered(query)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].OccurredAt.Equal(sorted[j].OccurredAt) {
			return sorted[i].ID > sorted[j].ID
		}
		return sorted[i].OccurredAt.After(sorted[j].OccurredAt)
	})

	offset := (page - 1) * pageSize
	if offset >= len(sorted) {
		return []store.AuditRecord{}, nil
	}
	end := offset + pageSize
	if end > len(sorted) {
		end = len(sorted)
	}
	return append([]store.AuditRecord{}, sorted[offset:end]...), nil
}

// Count 返回内存中审计记录总数。
func (r *testAuditRepo) Count(_ context.Context, query Query) (int64, error) {
	r.countCalls++
	r.lastQuery = query
	if r.countErr != nil {
		return 0, r.countErr
	}
	return int64(len(r.filtered(query))), nil
}

func (r *testAuditRepo) filtered(query Query) []store.AuditRecord {
	rows := make([]store.AuditRecord, 0, len(r.rows))
	for _, rec := range r.rows {
		if query.EventType != "" && rec.EventType != query.EventType {
			continue
		}
		if !query.Start.IsZero() && rec.OccurredAt.Before(query.Start) {
			continue
		}
		if !query.End.IsZero() && rec.OccurredAt.After(query.End) {
			continue
		}
		rows = append(rows, rec)
	}
	return rows
}

// fixedConfig 是 ConfigProvider 窄接口的内存实现，返回固定的保留期与分页配置。
type fixedConfig struct {
	retentionDays   int
	pageSizeDefault int
}

func (c fixedConfig) Config() config.YAMLConfig {
	return config.YAMLConfig{
		Audit: config.AuditConfig{
			RetentionDays:   c.retentionDays,
			PageSizeDefault: c.pageSizeDefault,
		},
	}
}

// fixedClock 是一个可注入的固定时钟，使时间戳与清理边界可断言。
var fixedNow = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

// newTestService 构造一个使用内存仓储、固定保留期与固定时钟的审计服务。
func newTestService(t *testing.T, repo *testAuditRepo, retentionDays int) *Service {
	t.Helper()
	svc, err := New(repo, fixedConfig{retentionDays: retentionDays})
	if err != nil {
		t.Fatalf("New 不应返回错误：%v", err)
	}
	svc.now = func() time.Time { return fixedNow }
	return svc
}

func TestNew_RejectsNilDependencies(t *testing.T) {
	if _, err := New(nil, fixedConfig{}); err == nil {
		t.Fatal("仓储为空时 New 应返回错误")
	}
	if _, err := New(&testAuditRepo{}, nil); err == nil {
		t.Fatal("配置存储为空时 New 应返回错误")
	}
}

func TestRecordLogin_WritesEventWithResultAndTimestamp(t *testing.T) {
	repo := &testAuditRepo{}
	svc := newTestService(t, repo, defaultRetentionDays)

	for _, success := range []bool{true, false} {
		if err := svc.RecordLogin(context.Background(), "admin", success); err != nil {
			t.Fatalf("RecordLogin 不应返回错误：%v", err)
		}
	}

	if len(repo.rows) != 2 {
		t.Fatalf("应写入 2 条登录审计，实际 %d 条", len(repo.rows))
	}
	for i, success := range []bool{true, false} {
		rec := repo.rows[i]
		if rec.EventType != store.AuditEventLogin {
			t.Errorf("事件类型应为 %q，实际 %q", store.AuditEventLogin, rec.EventType)
		}
		if rec.Target != "admin" {
			t.Errorf("目标应为 admin，实际 %q", rec.Target)
		}
		if !rec.OccurredAt.Equal(fixedNow) {
			t.Errorf("时间戳应为注入时钟 %v，实际 %v", fixedNow, rec.OccurredAt)
		}
		var detail map[string]any
		if err := json.Unmarshal(rec.Detail, &detail); err != nil {
			t.Fatalf("明细应为合法 JSON：%v", err)
		}
		if detail["success"] != success {
			t.Errorf("明细 success 应为 %v，实际 %v", success, detail["success"])
		}
	}
}

func TestRecordChange_WritesTypeTargetAndResource(t *testing.T) {
	cases := []struct {
		name     string
		call     func(svc *Service) error
		wantType string
		wantKind ResourceKind
		wantTgt  string
	}{
		{
			name:     "创建上游",
			call:     func(svc *Service) error { return svc.RecordCreate(context.Background(), ResourceUpstream, "上游A") },
			wantType: store.AuditEventCreate,
			wantKind: ResourceUpstream,
			wantTgt:  "上游A",
		},
		{
			name:     "更新规则",
			call:     func(svc *Service) error { return svc.RecordUpdate(context.Background(), ResourceRule, "rule-1") },
			wantType: store.AuditEventUpdate,
			wantKind: ResourceRule,
			wantTgt:  "rule-1",
		},
		{
			name:     "删除APIKey",
			call:     func(svc *Service) error { return svc.RecordDelete(context.Background(), ResourceAPIKey, "key-1") },
			wantType: store.AuditEventDelete,
			wantKind: ResourceAPIKey,
			wantTgt:  "key-1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &testAuditRepo{}
			svc := newTestService(t, repo, defaultRetentionDays)
			if err := tc.call(svc); err != nil {
				t.Fatalf("记录配置变更不应返回错误：%v", err)
			}
			if len(repo.rows) != 1 {
				t.Fatalf("应写入 1 条审计，实际 %d 条", len(repo.rows))
			}
			rec := repo.rows[0]
			if rec.EventType != tc.wantType {
				t.Errorf("事件类型应为 %q，实际 %q", tc.wantType, rec.EventType)
			}
			if rec.Target != tc.wantTgt {
				t.Errorf("目标应为 %q，实际 %q", tc.wantTgt, rec.Target)
			}
			if !rec.OccurredAt.Equal(fixedNow) {
				t.Errorf("时间戳应为注入时钟 %v，实际 %v", fixedNow, rec.OccurredAt)
			}
			var detail map[string]any
			if err := json.Unmarshal(rec.Detail, &detail); err != nil {
				t.Fatalf("明细应为合法 JSON：%v", err)
			}
			if detail["resource"] != string(tc.wantKind) {
				t.Errorf("明细 resource 应为 %q，实际 %v", tc.wantKind, detail["resource"])
			}
		})
	}
}

func TestRecordAccessDenied_WithAndWithoutReason(t *testing.T) {
	repo := &testAuditRepo{}
	svc := newTestService(t, repo, defaultRetentionDays)

	if err := svc.RecordAccessDenied(context.Background(), "/api/admin/upstreams", "令牌无效"); err != nil {
		t.Fatalf("RecordAccessDenied 不应返回错误：%v", err)
	}
	if err := svc.RecordAccessDenied(context.Background(), "/api/admin/keys", ""); err != nil {
		t.Fatalf("RecordAccessDenied 不应返回错误：%v", err)
	}

	if len(repo.rows) != 2 {
		t.Fatalf("应写入 2 条被拒访问审计，实际 %d 条", len(repo.rows))
	}

	withReason := repo.rows[0]
	if withReason.EventType != store.AuditEventAccessDenied {
		t.Errorf("事件类型应为 %q，实际 %q", store.AuditEventAccessDenied, withReason.EventType)
	}
	if withReason.Target != "/api/admin/upstreams" {
		t.Errorf("目标应为请求路径，实际 %q", withReason.Target)
	}
	var detail map[string]any
	if err := json.Unmarshal(withReason.Detail, &detail); err != nil {
		t.Fatalf("明细应为合法 JSON：%v", err)
	}
	if detail["reason"] != "令牌无效" {
		t.Errorf("明细 reason 应为 令牌无效，实际 %v", detail["reason"])
	}

	withoutReason := repo.rows[1]
	if len(withoutReason.Detail) != 0 {
		t.Errorf("原因为空时不应写入明细，实际 %s", withoutReason.Detail)
	}
}

func TestRecord_PropagatesRepoError(t *testing.T) {
	wantErr := errors.New("仓储写入失败")
	repo := &testAuditRepo{insertErr: wantErr}
	svc := newTestService(t, repo, defaultRetentionDays)

	if err := svc.RecordLogin(context.Background(), "admin", true); !errors.Is(err, wantErr) {
		t.Fatalf("应透传仓储错误，实际 %v", err)
	}
}

func TestCleanup_UsesConfiguredRetention(t *testing.T) {
	repo := &testAuditRepo{}
	svc := newTestService(t, repo, 30)

	// 写入两条记录：一条已超出 30 天保留期，一条在保留期内。
	old := store.AuditRecord{EventType: store.AuditEventLogin, OccurredAt: fixedNow.AddDate(0, 0, -31)}
	fresh := store.AuditRecord{EventType: store.AuditEventLogin, OccurredAt: fixedNow.AddDate(0, 0, -1)}
	repo.rows = append(repo.rows, old, fresh)

	deleted, err := svc.Cleanup(context.Background())
	if err != nil {
		t.Fatalf("Cleanup 不应返回错误：%v", err)
	}
	if deleted != 1 {
		t.Fatalf("应删除 1 条超期记录，实际 %d 条", deleted)
	}
	if repo.deleteCalls != 1 {
		t.Fatalf("应调用 1 次 DeleteOlderThan，实际 %d 次", repo.deleteCalls)
	}
	wantCutoff := fixedNow.AddDate(0, 0, -30)
	if !repo.lastCutoff.Equal(wantCutoff) {
		t.Errorf("清理截止时间应为 %v，实际 %v", wantCutoff, repo.lastCutoff)
	}
	if len(repo.rows) != 1 || !repo.rows[0].OccurredAt.Equal(fresh.OccurredAt) {
		t.Errorf("保留期内的记录应被保留")
	}
}

func TestCleanup_FallsBackToDefaultRetentionWhenOutOfRange(t *testing.T) {
	cases := []struct {
		name          string
		retentionDays int
	}{
		{"低于下界", 0},
		{"高于上界", maxRetentionDays + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &testAuditRepo{}
			svc := newTestService(t, repo, tc.retentionDays)
			if _, err := svc.Cleanup(context.Background()); err != nil {
				t.Fatalf("Cleanup 不应返回错误：%v", err)
			}
			wantCutoff := fixedNow.AddDate(0, 0, -defaultRetentionDays)
			if !repo.lastCutoff.Equal(wantCutoff) {
				t.Errorf("越界保留期应回退默认 %d 天，截止时间应为 %v，实际 %v",
					defaultRetentionDays, wantCutoff, repo.lastCutoff)
			}
		})
	}
}

func TestCleanup_PropagatesRepoError(t *testing.T) {
	wantErr := errors.New("仓储删除失败")
	repo := &testAuditRepo{deleteErr: wantErr}
	svc := newTestService(t, repo, defaultRetentionDays)

	if _, err := svc.Cleanup(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("应透传仓储错误，实际 %v", err)
	}
}
