package apikey

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/risk"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// testAPIKeyRepo 是 APIKeyRepository 窄接口的内存实现，便于在不触碰真实数据库的情况下
// 测试 API Key 管理器的生命周期逻辑。命名采用 test 前缀，避免与未来测试中的类型冲突。
type testAPIKeyRepo struct {
	rows   map[string]store.APIKey // 以 ID 为键存放已持久化的元数据。
	order  []string                // 记录插入顺序，供 List 稳定返回。
	nextID int                     // 自增计数，生成确定性 ID。
	now    time.Time               // 注入的创建时间，便于断言。

	// createErr 用于注入 Create 失败（如名称重复 CONFLICT），验证错误透传。
	createErr error
	// listErr 用于注入 List 失败，验证错误透传。
	listErr error
	// createCalls 记录 Create 被调用的次数，用于验证校验失败时不触达仓储。
	createCalls int
}

// newTestRepo 构造一个空的内存仓储，创建时间固定为可预期的时刻。
func newTestRepo() *testAPIKeyRepo {
	return &testAPIKeyRepo{
		rows: map[string]store.APIKey{},
		now:  time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
	}
}

func (r *testAPIKeyRepo) Create(_ context.Context, key store.APIKey) (store.APIKey, error) {
	r.createCalls++
	if r.createErr != nil {
		return store.APIKey{}, r.createErr
	}
	// 模拟全局唯一约束：名称重复返回 CONFLICT（Req 12.1）。
	for _, id := range r.order {
		if r.rows[id].Name == key.Name {
			return store.APIKey{}, domain.NewError(
				domain.CodeConflict, "API Key 名称已存在："+key.Name)
		}
	}
	r.nextID++
	key.ID = fmt.Sprintf("id-%d", r.nextID)
	key.CreatedAt = r.now
	r.rows[key.ID] = key
	r.order = append(r.order, key.ID)
	return key, nil
}

func (r *testAPIKeyRepo) Get(_ context.Context, id string) (store.APIKey, error) {
	if row, ok := r.rows[id]; ok {
		return row, nil
	}
	return store.APIKey{}, domain.NewError(domain.CodeNotFound, "API Key 不存在")
}

func (r *testAPIKeyRepo) List(_ context.Context) ([]store.APIKey, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	out := make([]store.APIKey, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.rows[id])
	}
	return out, nil
}

func (r *testAPIKeyRepo) SetEnabled(_ context.Context, id string, enabled bool) error {
	row, ok := r.rows[id]
	if !ok {
		return domain.NewError(domain.CodeNotFound, "API Key 不存在")
	}
	row.Enabled = enabled
	r.rows[id] = row
	return nil
}

func (r *testAPIKeyRepo) SetRiskProfile(_ context.Context, id string, profile risk.Profile) error {
	row, ok := r.rows[id]
	if !ok {
		return domain.NewError(domain.CodeNotFound, "API Key 不存在")
	}
	row.RiskProfile = profile
	r.rows[id] = row
	return nil
}

func (r *testAPIKeyRepo) Delete(_ context.Context, id string) error {
	if _, ok := r.rows[id]; !ok {
		return domain.NewError(domain.CodeNotFound, "API Key 不存在")
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

// asAPIError 将 error 断言为 *domain.APIError。
func asAPIError(t *testing.T, err error) *domain.APIError {
	t.Helper()
	if err == nil {
		t.Fatal("期望返回错误，但得到 nil")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望错误类型为 *domain.APIError，实际为 %T：%v", err, err)
	}
	return apiErr
}

// TestCreateReturnsPlaintextAndPersistsIt 验证创建成功返回明文密钥，
// 且仓储同时持久化哈希与明文（自部署场景，便于管理台二次查看）。
func TestCreateReturnsPlaintextAndPersistsIt(t *testing.T) {
	repo := newTestRepo()
	mgr := New(repo)

	created, err := mgr.Create(context.Background(), CreateInput{Name: "ci-runner"})
	if err != nil {
		t.Fatalf("创建不应失败：%v", err)
	}

	// 明文密钥应具备固定前缀与足够长度。
	if !strings.HasPrefix(created.PlaintextKey, keyPlaintextPrefix) {
		t.Errorf("明文密钥应以 %q 开头，实际 %q", keyPlaintextPrefix, created.PlaintextKey)
	}
	if len(created.PlaintextKey) <= keyPrefixLen {
		t.Errorf("明文密钥长度应大于展示前缀长度 %d，实际 %d", keyPrefixLen, len(created.PlaintextKey))
	}

	// 初始应为启用状态，名称与创建时间正确回填（Req 12.1）。
	if !created.Enabled {
		t.Error("新建 API Key 初始应为启用状态")
	}
	if created.Name != "ci-runner" {
		t.Errorf("名称期望 ci-runner，实际 %q", created.Name)
	}
	if !created.CreatedAt.Equal(repo.now) {
		t.Errorf("创建时间期望 %v，实际 %v", repo.now, created.CreatedAt)
	}

	// 元数据视图暴露展示前缀（明文前 12 字符）与完整明文，供二次查看。
	if created.KeyPrefix != created.PlaintextKey[:keyPrefixLen] {
		t.Errorf("展示前缀期望 %q，实际 %q", created.PlaintextKey[:keyPrefixLen], created.KeyPrefix)
	}
	if created.KeyPrefix == created.PlaintextKey {
		t.Error("展示前缀不应等于完整明文密钥")
	}

	// 仓储侧同时持久化明文与哈希：哈希用于鉴权等值查询，明文供管理台二次查看。
	row := repo.rows[created.ID]
	if string(row.KeyHash) == created.PlaintextKey {
		t.Error("哈希字段不应直接存明文")
	}
	wantHash := sha256.Sum256([]byte(created.PlaintextKey))
	if string(row.KeyHash) != string(wantHash[:]) {
		t.Error("哈希字段应存储明文的 SHA-256 摘要")
	}
	if row.KeyPlain != created.PlaintextKey {
		t.Errorf("明文字段应持久化完整明文，期望 %q，实际 %q", created.PlaintextKey, row.KeyPlain)
	}
	if row.KeyPrefix != created.KeyPrefix {
		t.Errorf("仓储前缀期望 %q，实际 %q", created.KeyPrefix, row.KeyPrefix)
	}
}

// TestCreateRejectsInvalidNameLength 验证名称越界（空/超 100）返回 VALIDATION，
// 且不持久化任何元数据（Req 12.8）。
func TestCreateRejectsInvalidNameLength(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"名称为空", ""},
		{"名称超 100 字符", strings.Repeat("a", maxNameLen+1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := newTestRepo()
			mgr := New(repo)

			_, err := mgr.Create(context.Background(), CreateInput{Name: c.in})
			apiErr := asAPIError(t, err)
			if apiErr.Code != domain.CodeValidation {
				t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
			}
			if _, ok := apiErr.Fields["name"]; !ok {
				t.Errorf("期望字段级错误包含 name，实际 %v", apiErr.Fields)
			}
			// 校验失败时不应触达仓储（Req 12.8）。
			if repo.createCalls != 0 {
				t.Errorf("校验失败时不应调用仓储 Create，实际调用 %d 次", repo.createCalls)
			}
			if len(repo.rows) != 0 {
				t.Error("校验失败时不应持久化任何元数据")
			}
		})
	}
}

// TestCreateAcceptsBoundaryNameLengths 验证名称长度边界（1 与 100 字符）被接受（Req 12.1）。
func TestCreateAcceptsBoundaryNameLengths(t *testing.T) {
	cases := []string{
		strings.Repeat("a", minNameLen), // 下界 1
		strings.Repeat("b", maxNameLen), // 上界 100
	}
	for _, name := range cases {
		repo := newTestRepo()
		mgr := New(repo)
		if _, err := mgr.Create(context.Background(), CreateInput{Name: name}); err != nil {
			t.Errorf("边界长度 %d 应被接受，错误：%v", len(name), err)
		}
	}
}

// TestCreatePropagatesConflict 验证名称重复时仓储返回的 CONFLICT 被透传（Req 12.1）。
func TestCreatePropagatesConflict(t *testing.T) {
	repo := newTestRepo()
	mgr := New(repo)

	if _, err := mgr.Create(context.Background(), CreateInput{Name: "dup"}); err != nil {
		t.Fatalf("首次创建不应失败：%v", err)
	}
	_, err := mgr.Create(context.Background(), CreateInput{Name: "dup"})
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeConflict {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeConflict, apiErr.Code)
	}
}

// TestGetPropagatesNotFound 验证查询不存在的 API Key 透传 NOT_FOUND（Req 12.7）。
func TestGetPropagatesNotFound(t *testing.T) {
	mgr := New(newTestRepo())
	_, err := mgr.Get(context.Background(), "missing")
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeNotFound {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeNotFound, apiErr.Code)
	}
}

// TestSetEnabledPropagatesNotFound 验证停用不存在的 API Key 透传 NOT_FOUND（Req 12.7）。
func TestSetEnabledPropagatesNotFound(t *testing.T) {
	mgr := New(newTestRepo())
	err := mgr.SetEnabled(context.Background(), "missing", false)
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeNotFound {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeNotFound, apiErr.Code)
	}
}

// TestDeletePropagatesNotFound 验证删除不存在的 API Key 透传 NOT_FOUND（Req 12.7）。
func TestDeletePropagatesNotFound(t *testing.T) {
	mgr := New(newTestRepo())
	err := mgr.Delete(context.Background(), "missing")
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeNotFound {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeNotFound, apiErr.Code)
	}
}

// TestListEmptyReturnsEmptySlice 验证系统中无任何 API Key 时返回空切片而非 nil/错误（Req 12.9）。
func TestListEmptyReturnsEmptySlice(t *testing.T) {
	mgr := New(newTestRepo())
	got, err := mgr.List(context.Background())
	if err != nil {
		t.Fatalf("列表查询不应失败：%v", err)
	}
	if got == nil {
		t.Error("空列表应返回非 nil 的空切片")
	}
	if len(got) != 0 {
		t.Errorf("期望空列表，实际包含 %d 条", len(got))
	}
}

// TestListReturnsMetadataWithPlaintext 验证列表返回元数据并携带完整明文密钥，
// 供管理台二次查看/复制（自部署场景）；鉴权仍走哈希。
func TestListReturnsMetadataWithPlaintext(t *testing.T) {
	repo := newTestRepo()
	mgr := New(repo)

	created, err := mgr.Create(context.Background(), CreateInput{Name: "svc"})
	if err != nil {
		t.Fatalf("创建不应失败：%v", err)
	}

	list, err := mgr.List(context.Background())
	if err != nil {
		t.Fatalf("列表查询不应失败：%v", err)
	}
	if len(list) != 1 {
		t.Fatalf("期望 1 条元数据，实际 %d 条", len(list))
	}

	meta := list[0]
	// 列表项携带完整明文密钥（PlaintextKey），与创建时一致。
	if meta.PlaintextKey != created.PlaintextKey {
		t.Errorf("列表明文期望 %q，实际 %q", created.PlaintextKey, meta.PlaintextKey)
	}
	if meta.KeyPrefix == created.PlaintextKey {
		t.Error("列表展示前缀不应等于完整明文密钥")
	}
	if meta.Name == created.PlaintextKey {
		t.Error("列表名称不应包含完整明文密钥")
	}
	if meta.KeyPrefix != created.PlaintextKey[:keyPrefixLen] {
		t.Errorf("列表展示前缀期望 %q，实际 %q", created.PlaintextKey[:keyPrefixLen], meta.KeyPrefix)
	}
}

// TestListPropagatesError 验证仓储 List 错误被透传。
func TestListPropagatesError(t *testing.T) {
	repo := newTestRepo()
	repo.listErr = domain.NewError(domain.CodeValidation, "数据库不可用")
	mgr := New(repo)

	if _, err := mgr.List(context.Background()); err == nil {
		t.Error("仓储 List 失败时应透传错误")
	}
}

// TestUsableRejectsDisabledKey 验证停用后的 API Key 不可用（Req 12.4）。
func TestUsableRejectsDisabledKey(t *testing.T) {
	repo := newTestRepo()
	mgr := New(repo)

	created, err := mgr.Create(context.Background(), CreateInput{Name: "to-disable"})
	if err != nil {
		t.Fatalf("创建不应失败：%v", err)
	}
	now := repo.now

	// 新建即启用，未过期时应可用。
	if !created.Usable(now) {
		t.Error("启用且未过期的 API Key 应可用")
	}

	// 停用后重新查询，应判定为不可用。
	if err := mgr.SetEnabled(context.Background(), created.ID, false); err != nil {
		t.Fatalf("停用不应失败：%v", err)
	}
	meta, err := mgr.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("查询不应失败：%v", err)
	}
	if meta.Enabled {
		t.Error("停用后启用状态应为 false")
	}
	if meta.Usable(now) {
		t.Error("停用后的 API Key 不应可用")
	}
}

// TestExpirySemantics 验证有效期相关的 IsExpired/Usable 判定（Req 12.6）。
func TestExpirySemantics(t *testing.T) {
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	expires := base
	future := base.Add(time.Hour)
	past := base.Add(-time.Hour)

	t.Run("未配置有效期永不过期", func(t *testing.T) {
		m := Metadata{Enabled: true, ExpiresAt: nil}
		if m.IsExpired(base) {
			t.Error("未配置有效期时不应判定为过期")
		}
		if !m.Usable(base) {
			t.Error("启用且未配置有效期的 API Key 应可用")
		}
	})

	t.Run("超过有效期即失效", func(t *testing.T) {
		m := Metadata{Enabled: true, ExpiresAt: &expires}
		if !m.IsExpired(expires.Add(time.Nanosecond)) {
			t.Error("当前时间晚于有效期时应判定为过期")
		}
		if m.Usable(future) {
			t.Error("已过期的 API Key 不应可用")
		}
	})

	t.Run("恰好等于有效期不算过期", func(t *testing.T) {
		m := Metadata{Enabled: true, ExpiresAt: &expires}
		if m.IsExpired(expires) {
			t.Error("当前时间恰好等于有效期时不应判定为过期（边界）")
		}
		if !m.Usable(expires) {
			t.Error("恰好等于有效期且启用时应仍可用（边界）")
		}
	})

	t.Run("未到有效期可用", func(t *testing.T) {
		m := Metadata{Enabled: true, ExpiresAt: &future}
		if m.IsExpired(past) {
			t.Error("当前时间早于有效期时不应判定为过期")
		}
		if !m.Usable(past) {
			t.Error("启用且未过期的 API Key 应可用")
		}
	})

	t.Run("停用即便未过期也不可用", func(t *testing.T) {
		m := Metadata{Enabled: false, ExpiresAt: &future}
		if m.Usable(base) {
			t.Error("停用的 API Key 即便未过期也不应可用")
		}
	})
}
