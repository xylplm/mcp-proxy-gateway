package apikey

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// testFilterRepo 是 FilterRepository 窄接口的内存实现，便于在不触碰真实数据库的情况下
// 测试 API Key 级屏蔽规则管理器（FilterManager）的 CRUD 与校验逻辑。命名采用 test 前缀，
// 避免与同包 manager_test.go 中的 testAPIKeyRepo 冲突。
type testFilterRepo struct {
	rows   map[string]store.FilterAPIKeyRow // 以 ID 为键存放已持久化的规则行。
	order  []string                         // 记录插入顺序，便于稳定遍历。
	nextID int                              // 自增计数，生成确定性 ID。

	// createErr 用于注入 Create 失败（如绑定的 API Key 不存在 NOT_FOUND），验证错误透传。
	createErr error
	// countErr 用于注入 CountByAPIKey 失败，验证错误透传。
	countErr error
	// createCalls 记录 Create 被调用的次数，用于验证校验失败时不触达仓储持久化。
	createCalls int
}

// newTestFilterRepo 构造一个空的内存仓储。
func newTestFilterRepo() *testFilterRepo {
	return &testFilterRepo{rows: map[string]store.FilterAPIKeyRow{}}
}

func (r *testFilterRepo) Create(_ context.Context, row store.FilterAPIKeyRow) (store.FilterAPIKeyRow, error) {
	r.createCalls++
	if r.createErr != nil {
		return store.FilterAPIKeyRow{}, r.createErr
	}
	r.nextID++
	row.ID = fmt.Sprintf("filter-%d", r.nextID)
	r.rows[row.ID] = row
	r.order = append(r.order, row.ID)
	return row, nil
}

func (r *testFilterRepo) ListByAPIKey(_ context.Context, apiKeyID string) ([]store.FilterAPIKeyRow, error) {
	out := make([]store.FilterAPIKeyRow, 0)
	for _, id := range r.order {
		if r.rows[id].APIKeyID == apiKeyID {
			out = append(out, r.rows[id])
		}
	}
	// 按 SortOrder 升序返回，模拟仓储的 ORDER BY sort_order ASC 语义。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].SortOrder > out[j].SortOrder; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out, nil
}

func (r *testFilterRepo) CountByAPIKey(_ context.Context, apiKeyID string) (int, error) {
	if r.countErr != nil {
		return 0, r.countErr
	}
	n := 0
	for _, id := range r.order {
		if r.rows[id].APIKeyID == apiKeyID {
			n++
		}
	}
	return n, nil
}

func (r *testFilterRepo) SetEnabled(_ context.Context, id string, enabled bool) error {
	row, ok := r.rows[id]
	if !ok {
		return domain.NewError(domain.CodeNotFound, "API Key 屏蔽规则不存在")
	}
	row.Enabled = enabled
	r.rows[id] = row
	return nil
}

func (r *testFilterRepo) Delete(_ context.Context, id string) error {
	if _, ok := r.rows[id]; !ok {
		return domain.NewError(domain.CodeNotFound, "API Key 屏蔽规则不存在")
	}
	delete(r.rows, id)
	for i, oid := range r.order {
		if oid == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return nil
}

// newTestFilterManager 构造一个以内存仓储与真实领域规则引擎为依赖的 FilterManager。
//
// 校验器刻意使用真实的 domain.NewRuleEngine()（而非 mock），以确保模式长度与正则合法性
// 校验复用领域规则引擎的 ValidateFilter，符合任务 14.3「不重复实现校验逻辑」的要求。
func newTestFilterManager() (*FilterManager, *testFilterRepo) {
	repo := newTestFilterRepo()
	return NewFilterManager(repo, domain.NewRuleEngine()), repo
}

// TestFilterCreateRejectsPatternLengthOutOfRange 验证匹配模式长度越界（空 / 超 200）
// 返回 VALIDATION 且不持久化任何数据（Req 13.4）。
func TestFilterCreateRejectsPatternLengthOutOfRange(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
	}{
		{"模式为空", ""},
		{"模式超 200 字符", strings.Repeat("a", 201)}, // 模式长度上限为 200，201 越界
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mgr, repo := newTestFilterManager()

			_, err := mgr.Create(context.Background(), CreateFilterInput{
				APIKeyID: "key-1",
				Pattern:  c.pattern,
				IsRegex:  false,
				Enabled:  true,
			})
			apiErr := asAPIError(t, err)
			if apiErr.Code != domain.CodeValidation {
				t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
			}
			if _, ok := apiErr.Fields["pattern"]; !ok {
				t.Errorf("期望字段级错误包含 pattern，实际 %v", apiErr.Fields)
			}
			// 校验失败时不应持久化任何数据（Req 13.4）。
			if repo.createCalls != 0 {
				t.Errorf("校验失败时不应调用仓储 Create，实际调用 %d 次", repo.createCalls)
			}
			if len(repo.rows) != 0 {
				t.Error("校验失败时不应持久化任何规则")
			}
		})
	}
}

// TestFilterCreateAcceptsBoundaryPatternLengths 验证模式长度边界（1 与 200 字符）被接受（Req 13.1）。
func TestFilterCreateAcceptsBoundaryPatternLengths(t *testing.T) {
	cases := []string{
		strings.Repeat("a", 1),   // 下界 1
		strings.Repeat("b", 200), // 上界 200
	}
	for _, pattern := range cases {
		mgr, _ := newTestFilterManager()
		if _, err := mgr.Create(context.Background(), CreateFilterInput{
			APIKeyID: "key-1",
			Pattern:  pattern,
			Enabled:  true,
		}); err != nil {
			t.Errorf("边界长度 %d 应被接受，错误：%v", len(pattern), err)
		}
	}
}

// TestFilterCreateRejectsInvalidRegex 验证非法正则被拒绝返回 VALIDATION 且不持久化（Req 13.4）。
func TestFilterCreateRejectsInvalidRegex(t *testing.T) {
	mgr, repo := newTestFilterManager()

	_, err := mgr.Create(context.Background(), CreateFilterInput{
		APIKeyID: "key-1",
		Pattern:  "(unclosed",
		IsRegex:  true,
		Enabled:  true,
	})
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
	}
	if _, ok := apiErr.Fields["pattern"]; !ok {
		t.Errorf("期望字段级错误包含 pattern，实际 %v", apiErr.Fields)
	}
	if len(repo.rows) != 0 {
		t.Error("非法正则不应持久化任何规则")
	}
}

// TestFilterCreateAcceptsValidRegex 验证合法正则被接受（Req 13.1、13.4）。
func TestFilterCreateAcceptsValidRegex(t *testing.T) {
	mgr, _ := newTestFilterManager()

	got, err := mgr.Create(context.Background(), CreateFilterInput{
		APIKeyID: "key-1",
		Pattern:  "tool_.*",
		IsRegex:  true,
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("合法正则应被接受，错误：%v", err)
	}
	if !got.IsRegex || got.Pattern != "tool_.*" {
		t.Errorf("创建结果与输入不一致：%+v", got)
	}
}

// TestFilterCreateRejectsOverLimit 验证单个 API Key 屏蔽规则数量达到上限 100 后
// 继续新增被拒绝返回 VALIDATION（Req 13.2、13.3）。
func TestFilterCreateRejectsOverLimit(t *testing.T) {
	mgr, repo := newTestFilterManager()
	const apiKeyID = "key-1"

	// 先填满至上限 100 条。
	for i := 0; i < domain.MaxFilterRulesPerScope; i++ {
		if _, err := mgr.Create(context.Background(), CreateFilterInput{
			APIKeyID: apiKeyID,
			Pattern:  fmt.Sprintf("tool-%d", i),
			Enabled:  true,
		}); err != nil {
			t.Fatalf("第 %d 条创建不应失败：%v", i+1, err)
		}
	}
	if got, _ := repo.CountByAPIKey(context.Background(), apiKeyID); got != domain.MaxFilterRulesPerScope {
		t.Fatalf("应已存在 %d 条规则，实际 %d 条", domain.MaxFilterRulesPerScope, got)
	}

	// 第 101 条应被上限校验拒绝（Req 13.3）。
	_, err := mgr.Create(context.Background(), CreateFilterInput{
		APIKeyID: apiKeyID,
		Pattern:  "overflow",
		Enabled:  true,
	})
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("超过上限应返回 %q，实际 %q", domain.CodeValidation, apiErr.Code)
	}
	if got, _ := repo.CountByAPIKey(context.Background(), apiKeyID); got != domain.MaxFilterRulesPerScope {
		t.Errorf("超过上限后规则数仍应为 %d，实际 %d", domain.MaxFilterRulesPerScope, got)
	}
}

// TestFilterCreateLimitIsPerAPIKey 验证数量上限按 API Key 独立计数：
// 某 Key 达到上限不影响另一个 Key 继续创建（Req 13.2）。
func TestFilterCreateLimitIsPerAPIKey(t *testing.T) {
	mgr, _ := newTestFilterManager()

	for i := 0; i < domain.MaxFilterRulesPerScope; i++ {
		if _, err := mgr.Create(context.Background(), CreateFilterInput{
			APIKeyID: "key-full",
			Pattern:  fmt.Sprintf("t-%d", i),
			Enabled:  true,
		}); err != nil {
			t.Fatalf("填充 key-full 第 %d 条失败：%v", i+1, err)
		}
	}

	// 另一个 Key 应仍可正常创建。
	if _, err := mgr.Create(context.Background(), CreateFilterInput{
		APIKeyID: "key-other",
		Pattern:  "first",
		Enabled:  true,
	}); err != nil {
		t.Errorf("另一个 API Key 的首条规则不应受其他 Key 上限影响：%v", err)
	}
}

// TestFilterCreateAppendsSortOrder 验证创建时按当前规则数自动追加 SortOrder，保证列表稳定升序。
func TestFilterCreateAppendsSortOrder(t *testing.T) {
	mgr, _ := newTestFilterManager()
	const apiKeyID = "key-1"

	for i := 0; i < 3; i++ {
		got, err := mgr.Create(context.Background(), CreateFilterInput{
			APIKeyID: apiKeyID,
			Pattern:  fmt.Sprintf("p-%d", i),
			Enabled:  true,
		})
		if err != nil {
			t.Fatalf("创建第 %d 条失败：%v", i+1, err)
		}
		if got.SortOrder != i {
			t.Errorf("第 %d 条 SortOrder 期望 %d，实际 %d", i+1, i, got.SortOrder)
		}
	}
}

// TestFilterCreatePropagatesNotFound 验证绑定的 API Key 不存在时仓储 NOT_FOUND 被透传。
func TestFilterCreatePropagatesNotFound(t *testing.T) {
	mgr, repo := newTestFilterManager()
	repo.createErr = domain.NewError(domain.CodeNotFound, "绑定的 API Key 不存在")

	_, err := mgr.Create(context.Background(), CreateFilterInput{
		APIKeyID: "missing",
		Pattern:  "valid",
		Enabled:  true,
	})
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeNotFound {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeNotFound, apiErr.Code)
	}
}

// TestFilterCreatePropagatesCountError 验证 CountByAPIKey 失败时错误被透传，且不触达 Create。
func TestFilterCreatePropagatesCountError(t *testing.T) {
	mgr, repo := newTestFilterManager()
	repo.countErr = domain.NewError(domain.CodeValidation, "数据库不可用")

	_, err := mgr.Create(context.Background(), CreateFilterInput{
		APIKeyID: "key-1",
		Pattern:  "valid",
		Enabled:  true,
	})
	if err == nil {
		t.Fatal("CountByAPIKey 失败时应透传错误")
	}
	if repo.createCalls != 0 {
		t.Errorf("上限校验失败时不应调用 Create，实际调用 %d 次", repo.createCalls)
	}
}

// TestFilterSetEnabledUpdatesState 验证启停更新生效，且 List 能读取到更新后的状态（Req 13.8）。
func TestFilterSetEnabledUpdatesState(t *testing.T) {
	mgr, _ := newTestFilterManager()
	const apiKeyID = "key-1"

	created, err := mgr.Create(context.Background(), CreateFilterInput{
		APIKeyID: apiKeyID,
		Pattern:  "tool_a",
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("创建不应失败：%v", err)
	}
	if !created.Enabled {
		t.Fatal("创建时 Enabled 应为 true")
	}

	// 停用后列表应反映新状态（启停即时性，Req 13.8）。
	if err := mgr.SetEnabled(context.Background(), created.ID, false); err != nil {
		t.Fatalf("停用不应失败：%v", err)
	}
	list, err := mgr.List(context.Background(), apiKeyID)
	if err != nil {
		t.Fatalf("列表查询不应失败：%v", err)
	}
	if len(list) != 1 {
		t.Fatalf("期望 1 条规则，实际 %d 条", len(list))
	}
	if list[0].Enabled {
		t.Error("停用后列表中的规则 Enabled 应为 false")
	}

	// 再次启用应恢复。
	if err := mgr.SetEnabled(context.Background(), created.ID, true); err != nil {
		t.Fatalf("启用不应失败：%v", err)
	}
	list, _ = mgr.List(context.Background(), apiKeyID)
	if !list[0].Enabled {
		t.Error("重新启用后列表中的规则 Enabled 应为 true")
	}
}

// TestFilterSetEnabledPropagatesNotFound 验证对不存在的规则启停透传 NOT_FOUND。
func TestFilterSetEnabledPropagatesNotFound(t *testing.T) {
	mgr, _ := newTestFilterManager()
	err := mgr.SetEnabled(context.Background(), "missing", false)
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeNotFound {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeNotFound, apiErr.Code)
	}
}

// TestFilterListReturnsRulesForKeyOnly 验证 List 仅返回目标 API Key 的规则、按 SortOrder 升序，
// 且其形态可被聚合管线第 6 阶段直接消费（Pattern/IsRegex/Enabled 等字段完整，Req 13.7）。
func TestFilterListReturnsRulesForKeyOnly(t *testing.T) {
	mgr, _ := newTestFilterManager()

	// key-1 下创建两条，key-2 下创建一条，验证 List 不串号。
	if _, err := mgr.Create(context.Background(), CreateFilterInput{APIKeyID: "key-1", Pattern: "alpha", IsRegex: false, Enabled: true}); err != nil {
		t.Fatalf("创建失败：%v", err)
	}
	if _, err := mgr.Create(context.Background(), CreateFilterInput{APIKeyID: "key-1", Pattern: "beta.*", IsRegex: true, Enabled: false}); err != nil {
		t.Fatalf("创建失败：%v", err)
	}
	if _, err := mgr.Create(context.Background(), CreateFilterInput{APIKeyID: "key-2", Pattern: "gamma", Enabled: true}); err != nil {
		t.Fatalf("创建失败：%v", err)
	}

	list, err := mgr.List(context.Background(), "key-1")
	if err != nil {
		t.Fatalf("列表查询不应失败：%v", err)
	}
	if len(list) != 2 {
		t.Fatalf("key-1 期望 2 条规则，实际 %d 条", len(list))
	}
	// 按 SortOrder 升序：第一条 alpha（order 0），第二条 beta.*（order 1）。
	if list[0].Pattern != "alpha" || list[0].SortOrder != 0 {
		t.Errorf("首条规则期望 alpha(order 0)，实际 %+v", list[0])
	}
	if list[1].Pattern != "beta.*" || list[1].SortOrder != 1 {
		t.Errorf("次条规则期望 beta.*(order 1)，实际 %+v", list[1])
	}
	// 字段完整性：聚合管线据此匹配工具 OriginalName，需保留 IsRegex 与 Enabled。
	if !list[1].IsRegex || list[1].Enabled {
		t.Errorf("规则字段未正确保留：%+v", list[1])
	}
	if list[0].APIKeyID != "key-1" {
		t.Errorf("规则应绑定 key-1，实际 %q", list[0].APIKeyID)
	}
}

// TestFilterListEmptyReturnsEmptySlice 验证某 API Key 无规则时返回非 nil 的空切片而非错误。
func TestFilterListEmptyReturnsEmptySlice(t *testing.T) {
	mgr, _ := newTestFilterManager()
	got, err := mgr.List(context.Background(), "no-rules")
	if err != nil {
		t.Fatalf("列表查询不应失败：%v", err)
	}
	if got == nil {
		t.Error("无规则时应返回非 nil 的空切片")
	}
	if len(got) != 0 {
		t.Errorf("期望空列表，实际 %d 条", len(got))
	}
}

// TestFilterDeleteRemovesRule 验证删除后规则不再出现在列表中。
func TestFilterDeleteRemovesRule(t *testing.T) {
	mgr, _ := newTestFilterManager()
	const apiKeyID = "key-1"

	created, err := mgr.Create(context.Background(), CreateFilterInput{APIKeyID: apiKeyID, Pattern: "p", Enabled: true})
	if err != nil {
		t.Fatalf("创建不应失败：%v", err)
	}
	if err := mgr.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("删除不应失败：%v", err)
	}
	list, _ := mgr.List(context.Background(), apiKeyID)
	if len(list) != 0 {
		t.Errorf("删除后列表应为空，实际 %d 条", len(list))
	}
}

// TestFilterDeletePropagatesNotFound 验证删除不存在的规则透传 NOT_FOUND。
func TestFilterDeletePropagatesNotFound(t *testing.T) {
	mgr, _ := newTestFilterManager()
	err := mgr.Delete(context.Background(), "missing")
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeNotFound {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeNotFound, apiErr.Code)
	}
}
