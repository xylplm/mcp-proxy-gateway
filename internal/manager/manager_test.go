package manager

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 本文件针对任务 9.1 已实现的上游 MCP 增删查改与校验逻辑编写单元测试
// （任务 9.2），覆盖以下需求：
//   - Req 2.2：缺字段/名称越界/格式非法时拒绝创建、不持久化并返回字段级校验错误。
//   - Req 2.6：更新或删除不存在的标识时透传 NOT_FOUND、不持久化任何变更。
//   - Req 2.7：名称与既有上游重复时透传 CONFLICT。
//   - Req 2.8：列表为空时返回空切片而非错误。
//
// 测试仅针对已实现的 CRUD 方法（Create/Update/Delete/List/SetEnabled），
// 不触及 Reorder/GetState/Reconnect 等占位方法。所有替身类型均以 test 前缀命名，
// 避免与实现文件或并行任务可能新增的类型重名。

// sortOrderCall 记录一次 SetSortOrder 调用的入参，用于断言 Reorder 的持久化行为。
type sortOrderCall struct {
	id        string
	sortOrder int
}

// testUpstreamRepo 是 UpstreamRepository 的内存替身。
//
// 它记录各方法的调用次数与最近一次入参，并允许注入受控的返回值与错误，
// 从而在不依赖真实数据库的前提下验证连接管理器的行为与「是否持久化」语义。
type testUpstreamRepo struct {
	// 调用计数，用于断言校验失败时不触达持久层（Req 2.2、2.7）。
	createCalls    int
	updateCalls    int
	deleteCalls    int
	setEnabledCall int
	listCalls      int

	// 最近一次 SetEnabled 收到的入参，用于断言启停透传（Req 3.1、3.2）。
	lastEnabledID string
	lastEnabled   bool

	// setSortOrderCalls 记录每次 SetSortOrder 的入参（id→sortOrder 的有序列表），
	// 用于断言 Reorder 持久化次数与位置即排序的语义（Req 3.4）。
	setSortOrderCalls []sortOrderCall
	// setSortOrderErr 注入 SetSortOrder 的返回错误。
	setSortOrderErr error

	// 最近一次 Create/Update 收到的入参，用于断言加密凭证已透传。
	lastCfg           domain.UpstreamConfig
	lastCredentialEnc []byte

	// 注入的返回行与错误。
	createRow  *store.UpstreamRow
	createErr  error
	updateRow  *store.UpstreamRow
	updateErr  error
	deleteErr  error
	setEnabled error
	listRows   []store.UpstreamRow
	listErr    error
}

func (r *testUpstreamRepo) Create(_ context.Context, cfg domain.UpstreamConfig, credentialEnc []byte) (*store.UpstreamRow, error) {
	r.createCalls++
	r.lastCfg = cfg
	r.lastCredentialEnc = credentialEnc
	if r.createErr != nil {
		return nil, r.createErr
	}
	return r.createRow, nil
}

func (r *testUpstreamRepo) Get(_ context.Context, _ string) (*store.UpstreamRow, error) {
	// CRUD 测试不经由 Manager 暴露的 Get（接口未暴露），保留以满足窄接口。
	return nil, domain.NewError(domain.CodeNotFound, "上游 MCP 不存在")
}

func (r *testUpstreamRepo) List(_ context.Context) ([]store.UpstreamRow, error) {
	r.listCalls++
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.listRows, nil
}

func (r *testUpstreamRepo) Update(_ context.Context, _ string, cfg domain.UpstreamConfig, credentialEnc []byte) (*store.UpstreamRow, error) {
	r.updateCalls++
	r.lastCfg = cfg
	r.lastCredentialEnc = credentialEnc
	if r.updateErr != nil {
		return nil, r.updateErr
	}
	return r.updateRow, nil
}

func (r *testUpstreamRepo) SetEnabled(_ context.Context, id string, enabled bool) error {
	r.setEnabledCall++
	r.lastEnabledID = id
	r.lastEnabled = enabled
	return r.setEnabled
}

func (r *testUpstreamRepo) SetSortOrder(_ context.Context, id string, sortOrder int) error {
	if r.setSortOrderErr != nil {
		return r.setSortOrderErr
	}
	r.setSortOrderCalls = append(r.setSortOrderCalls, sortOrderCall{id: id, sortOrder: sortOrder})
	return nil
}

func (r *testUpstreamRepo) Delete(_ context.Context, _ string) error {
	r.deleteCalls++
	return r.deleteErr
}

// testEncryptor 是 CredentialEncryptor 的内存替身。
//
// 默认将明文加上固定前缀模拟「加密」，便于断言写库前确实经过加密；
// 也可注入错误以覆盖加密失败路径。
type testEncryptor struct {
	calls         int
	lastPlaintext []byte
	err           error
}

// testEncPrefix 为模拟加密添加的可识别前缀。
const testEncPrefix = "enc:"

func (e *testEncryptor) Encrypt(plaintext []byte) ([]byte, error) {
	e.calls++
	e.lastPlaintext = plaintext
	if e.err != nil {
		return nil, e.err
	}
	return append([]byte(testEncPrefix), plaintext...), nil
}

// testToolCacheCleaner 是 ToolCacheCleaner 的内存替身，记录删除调用。
type testToolCacheCleaner struct {
	calls  int
	lastID string
	err    error
}

func (c *testToolCacheCleaner) Delete(_ context.Context, upstreamID string) error {
	c.calls++
	c.lastID = upstreamID
	return c.err
}

// testValidConfig 返回一个可通过校验的最小合法上游配置（stdio + command）。
func testValidConfig() domain.UpstreamConfig {
	return domain.UpstreamConfig{
		Name:       "测试上游",
		Transport:  domain.TransportStdio,
		ConnParams: map[string]any{"command": "node"},
		Credential: "secret-token",
		Enabled:    true,
	}
}

// asAPIError 将 err 断言为 *domain.APIError，失败则终止当前测试。
func asAPIError(t *testing.T, err error) *domain.APIError {
	t.Helper()
	if err == nil {
		t.Fatal("期望返回错误，却返回 nil")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望错误类型为 *domain.APIError，实际为 %T：%v", err, err)
	}
	return apiErr
}

// TestCreateNameValidation 验证名称越界（空、仅空白、>100）时返回 VALIDATION、
// 字段级错误含 name，且未触达仓储（不持久化）；边界 100 字符应通过名称校验（Req 2.2）。
func TestCreateNameValidation(t *testing.T) {
	cases := []struct {
		name         string
		upstreamName string
		wantNameErr  bool
	}{
		{name: "名称为空", upstreamName: "", wantNameErr: true},
		{name: "名称仅空白", upstreamName: "   ", wantNameErr: true},
		{name: "名称超过100字符", upstreamName: strings.Repeat("a", 101), wantNameErr: true},
		{name: "名称恰为100字符合法", upstreamName: strings.Repeat("a", 100), wantNameErr: false},
		{name: "名称恰为1字符合法", upstreamName: "x", wantNameErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &testUpstreamRepo{createRow: &store.UpstreamRow{}}
			enc := &testEncryptor{}
			m := New(repo, enc, &testToolCacheCleaner{}, nil, nil)

			cfg := testValidConfig()
			cfg.Name = tc.upstreamName

			_, err := m.Create(context.Background(), cfg)

			if !tc.wantNameErr {
				// 名称合法时（连接参数也合法）不应因 name 而失败。
				if err != nil {
					apiErr := asAPIError(t, err)
					if _, ok := apiErr.Fields["name"]; ok {
						t.Fatalf("合法名称不应产生 name 字段错误，Fields=%v", apiErr.Fields)
					}
				}
				return
			}

			apiErr := asAPIError(t, err)
			if apiErr.Code != domain.CodeValidation {
				t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
			}
			if _, ok := apiErr.Fields["name"]; !ok {
				t.Errorf("期望字段级错误包含 name，实际 Fields=%v", apiErr.Fields)
			}
			// 校验失败必须不持久化（Req 2.2）。
			if repo.createCalls != 0 {
				t.Errorf("校验失败时不应调用仓储 Create，实际调用 %d 次", repo.createCalls)
			}
			if enc.calls != 0 {
				t.Errorf("校验失败时不应进行凭证加密，实际调用 %d 次", enc.calls)
			}
		})
	}
}

// TestCreateConnParamsValidation 验证连接参数缺失/非法时返回 VALIDATION 且
// 字段级错误含 connParams.* 前缀，并且不持久化（Req 2.2）。
func TestCreateConnParamsValidation(t *testing.T) {
	cases := []struct {
		name      string
		cfg       domain.UpstreamConfig
		wantField string
	}{
		{
			name: "stdio缺少command",
			cfg: domain.UpstreamConfig{
				Name:       "缺参数上游",
				Transport:  domain.TransportStdio,
				ConnParams: map[string]any{},
			},
			wantField: "connParams.command",
		},
		{
			name: "sse的url非法",
			cfg: domain.UpstreamConfig{
				Name:       "非法URL上游",
				Transport:  domain.TransportSSE,
				ConnParams: map[string]any{"url": "ftp://example.com"},
			},
			wantField: "connParams.url",
		},
		{
			name: "传输类型不受支持",
			cfg: domain.UpstreamConfig{
				Name:       "未知传输上游",
				Transport:  domain.TransportType("grpc"),
				ConnParams: map[string]any{},
			},
			wantField: "transport",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &testUpstreamRepo{}
			m := New(repo, &testEncryptor{}, &testToolCacheCleaner{}, nil, nil)

			_, err := m.Create(context.Background(), tc.cfg)

			apiErr := asAPIError(t, err)
			if apiErr.Code != domain.CodeValidation {
				t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
			}
			if _, ok := apiErr.Fields[tc.wantField]; !ok {
				t.Errorf("期望字段级错误包含 %q，实际 Fields=%v", tc.wantField, apiErr.Fields)
			}
			if repo.createCalls != 0 {
				t.Errorf("校验失败时不应调用仓储 Create，实际调用 %d 次", repo.createCalls)
			}
		})
	}
}

// TestCreateReportsAllInvalidFields 验证当名称与连接参数同时非法时，
// 校验错误一次性标识出每个无效字段（Req 2.2「返回标识每个无效字段的校验错误」），
// 且不触达持久层。
func TestCreateReportsAllInvalidFields(t *testing.T) {
	repo := &testUpstreamRepo{}
	enc := &testEncryptor{}
	m := New(repo, enc, &testToolCacheCleaner{}, nil, nil)

	// 名称为空 + stdio 缺少 command：两类字段错误应同时出现。
	cfg := domain.UpstreamConfig{
		Name:       "",
		Transport:  domain.TransportStdio,
		ConnParams: map[string]any{},
	}

	_, err := m.Create(context.Background(), cfg)

	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
	}
	if _, ok := apiErr.Fields["name"]; !ok {
		t.Errorf("期望字段级错误包含 name，实际 Fields=%v", apiErr.Fields)
	}
	if _, ok := apiErr.Fields["connParams.command"]; !ok {
		t.Errorf("期望字段级错误包含 connParams.command，实际 Fields=%v", apiErr.Fields)
	}
	// 校验失败必须不持久化、不加密（Req 2.2）。
	if repo.createCalls != 0 {
		t.Errorf("校验失败时不应调用仓储 Create，实际调用 %d 次", repo.createCalls)
	}
	if enc.calls != 0 {
		t.Errorf("校验失败时不应进行凭证加密，实际调用 %d 次", enc.calls)
	}
}

// TestCreateNameConflictPassthrough 验证名称冲突时仓储返回的 CONFLICT 被透传（Req 2.7）。
func TestCreateNameConflictPassthrough(t *testing.T) {
	repo := &testUpstreamRepo{
		createErr: domain.NewError(domain.CodeConflict, "上游 MCP 名称已存在：测试上游"),
	}
	m := New(repo, &testEncryptor{}, &testToolCacheCleaner{}, nil, nil)

	_, err := m.Create(context.Background(), testValidConfig())

	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeConflict {
		t.Errorf("期望透传错误码 %q，实际 %q", domain.CodeConflict, apiErr.Code)
	}
	// 通过校验后应已尝试持久化。
	if repo.createCalls != 1 {
		t.Errorf("期望调用仓储 Create 1 次，实际 %d 次", repo.createCalls)
	}
}

// TestCreateSuccessEncryptsAndHidesCredential 验证创建成功路径：
//   - 凭证经 Encrypt 加密后传给仓储（写库前加密，Req 19.1）；
//   - 返回的 Upstream 不含明文 Credential（Req 19.3）。
func TestCreateSuccessEncryptsAndHidesCredential(t *testing.T) {
	cfg := testValidConfig()

	// 构造仓储返回行；仓储层约定 Config.Credential 已置空。
	row := &store.UpstreamRow{}
	row.ID = "up-1"
	row.Config = cfg
	row.Config.Credential = ""

	repo := &testUpstreamRepo{createRow: row}
	enc := &testEncryptor{}
	m := New(repo, enc, &testToolCacheCleaner{}, nil, nil)

	up, err := m.Create(context.Background(), cfg)
	if err != nil {
		t.Fatalf("创建成功路径不应返回错误：%v", err)
	}

	// 凭证应被加密且明文正确传入加密服务。
	if enc.calls != 1 {
		t.Errorf("期望调用加密服务 1 次，实际 %d 次", enc.calls)
	}
	if string(enc.lastPlaintext) != cfg.Credential {
		t.Errorf("期望加密明文为 %q，实际 %q", cfg.Credential, string(enc.lastPlaintext))
	}
	// 传给仓储的应为密文（前缀 + 明文），且不等于明文。
	wantEnc := testEncPrefix + cfg.Credential
	if string(repo.lastCredentialEnc) != wantEnc {
		t.Errorf("期望仓储收到密文 %q，实际 %q", wantEnc, string(repo.lastCredentialEnc))
	}
	if string(repo.lastCredentialEnc) == cfg.Credential {
		t.Error("仓储不应收到明文凭证")
	}
	// 返回对象不得包含明文凭证。
	if up.Config.Credential != "" {
		t.Errorf("返回的 Upstream 不应包含明文凭证，实际 %q", up.Config.Credential)
	}
	if up.ID != "up-1" {
		t.Errorf("期望返回 ID 为 up-1，实际 %q", up.ID)
	}
}

// TestCreateWithoutCredentialSkipsEncryption 验证无凭证时不调用加密、仓储收到 nil 密文。
func TestCreateWithoutCredentialSkipsEncryption(t *testing.T) {
	cfg := testValidConfig()
	cfg.Credential = ""

	row := &store.UpstreamRow{}
	row.ID = "up-2"
	row.Config = cfg

	repo := &testUpstreamRepo{createRow: row}
	enc := &testEncryptor{}
	m := New(repo, enc, &testToolCacheCleaner{}, nil, nil)

	if _, err := m.Create(context.Background(), cfg); err != nil {
		t.Fatalf("无凭证创建不应返回错误：%v", err)
	}
	if enc.calls != 0 {
		t.Errorf("无凭证时不应调用加密服务，实际 %d 次", enc.calls)
	}
	if repo.lastCredentialEnc != nil {
		t.Errorf("无凭证时仓储应收到 nil 密文，实际 %v", repo.lastCredentialEnc)
	}
}

// TestUpdateNotFoundPassthrough 验证更新不存在的标识时透传 NOT_FOUND（Req 2.6）。
func TestUpdateNotFoundPassthrough(t *testing.T) {
	repo := &testUpstreamRepo{
		updateErr: domain.NewError(domain.CodeNotFound, "上游 MCP 不存在"),
	}
	m := New(repo, &testEncryptor{}, &testToolCacheCleaner{}, nil, nil)

	_, err := m.Update(context.Background(), "missing-id", testValidConfig())

	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeNotFound {
		t.Errorf("期望透传错误码 %q，实际 %q", domain.CodeNotFound, apiErr.Code)
	}
}

// TestUpdateValidationDoesNotPersist 验证更新时字段校验失败不触达仓储（Req 2.2）。
func TestUpdateValidationDoesNotPersist(t *testing.T) {
	repo := &testUpstreamRepo{}
	m := New(repo, &testEncryptor{}, &testToolCacheCleaner{}, nil, nil)

	cfg := testValidConfig()
	cfg.Name = "" // 触发名称校验失败

	_, err := m.Update(context.Background(), "any-id", cfg)

	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
	}
	if repo.updateCalls != 0 {
		t.Errorf("校验失败时不应调用仓储 Update，实际调用 %d 次", repo.updateCalls)
	}
}

// TestUpdateNameConflictPassthrough 验证更新时名称重复透传 CONFLICT（Req 2.7）。
func TestUpdateNameConflictPassthrough(t *testing.T) {
	repo := &testUpstreamRepo{
		updateErr: domain.NewError(domain.CodeConflict, "上游 MCP 名称已存在：测试上游"),
	}
	m := New(repo, &testEncryptor{}, &testToolCacheCleaner{}, nil, nil)

	_, err := m.Update(context.Background(), "some-id", testValidConfig())

	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeConflict {
		t.Errorf("期望透传错误码 %q，实际 %q", domain.CodeConflict, apiErr.Code)
	}
}

// TestDeleteNotFoundPassthrough 验证删除不存在的标识时透传 NOT_FOUND，
// 且仓储删除失败后不再清理缓存（Req 2.6）。
func TestDeleteNotFoundPassthrough(t *testing.T) {
	repo := &testUpstreamRepo{
		deleteErr: domain.NewError(domain.CodeNotFound, "上游 MCP 不存在"),
	}
	cache := &testToolCacheCleaner{}
	m := New(repo, &testEncryptor{}, cache, nil, nil)

	err := m.Delete(context.Background(), "missing-id")

	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeNotFound {
		t.Errorf("期望透传错误码 %q，实际 %q", domain.CodeNotFound, apiErr.Code)
	}
	// 删除失败（资源不存在）时 DB 未变更，不应再尝试清理缓存。
	if cache.calls != 0 {
		t.Errorf("删除失败时不应清理工具缓存，实际调用 %d 次", cache.calls)
	}
}

// TestDeleteSuccessCleansCache 验证删除成功后级联清理工具缓存（Req 2.5、6.6）。
func TestDeleteSuccessCleansCache(t *testing.T) {
	repo := &testUpstreamRepo{}
	cache := &testToolCacheCleaner{}
	m := New(repo, &testEncryptor{}, cache, nil, nil)

	if err := m.Delete(context.Background(), "up-9"); err != nil {
		t.Fatalf("删除成功路径不应返回错误：%v", err)
	}
	if repo.deleteCalls != 1 {
		t.Errorf("期望调用仓储 Delete 1 次，实际 %d 次", repo.deleteCalls)
	}
	if cache.calls != 1 || cache.lastID != "up-9" {
		t.Errorf("期望清理 up-9 的工具缓存 1 次，实际 calls=%d lastID=%q", cache.calls, cache.lastID)
	}
}

// TestDeleteCacheCleanupBestEffort 验证缓存清理失败为尽力而为，不令整体删除失败（Req 6.6）。
func TestDeleteCacheCleanupBestEffort(t *testing.T) {
	repo := &testUpstreamRepo{}
	cache := &testToolCacheCleaner{err: errors.New("redis 不可达")}
	m := New(repo, &testEncryptor{}, cache, nil, nil)

	if err := m.Delete(context.Background(), "up-10"); err != nil {
		t.Fatalf("缓存清理失败不应令删除整体失败，却返回错误：%v", err)
	}
	if cache.calls != 1 {
		t.Errorf("期望尝试清理缓存 1 次，实际 %d 次", cache.calls)
	}
}

// TestSetEnabledNotFoundPassthrough 验证启停不存在的标识时透传 NOT_FOUND（Req 2.6）。
func TestSetEnabledNotFoundPassthrough(t *testing.T) {
	repo := &testUpstreamRepo{
		setEnabled: domain.NewError(domain.CodeNotFound, "上游 MCP 不存在"),
	}
	m := New(repo, &testEncryptor{}, &testToolCacheCleaner{}, nil, nil)

	err := m.SetEnabled(context.Background(), "missing-id", true)

	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeNotFound {
		t.Errorf("期望透传错误码 %q，实际 %q", domain.CodeNotFound, apiErr.Code)
	}
}

// TestListEmptyReturnsEmptySlice 验证无数据时返回空切片且无错误（Req 2.8）。
func TestListEmptyReturnsEmptySlice(t *testing.T) {
	repo := &testUpstreamRepo{listRows: []store.UpstreamRow{}}
	m := New(repo, &testEncryptor{}, &testToolCacheCleaner{}, nil, nil)

	out, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("空列表不应返回错误：%v", err)
	}
	if out == nil {
		t.Fatal("空列表应返回非 nil 的空切片")
	}
	if len(out) != 0 {
		t.Errorf("期望空切片，实际长度 %d", len(out))
	}
}

// TestListMapsRowsAndHidesCredential 验证列表正确映射多条记录且不外泄明文凭证（Req 2.3、19.3）。
func TestListMapsRowsAndHidesCredential(t *testing.T) {
	row1 := store.UpstreamRow{}
	row1.ID = "a"
	row1.Config = testValidConfig()
	row1.Config.Credential = "明文不应外泄"

	row2 := store.UpstreamRow{}
	row2.ID = "b"
	row2.Config = testValidConfig()

	repo := &testUpstreamRepo{listRows: []store.UpstreamRow{row1, row2}}
	m := New(repo, &testEncryptor{}, &testToolCacheCleaner{}, nil, nil)

	out, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("列表查询不应返回错误：%v", err)
	}
	if len(out) != 2 {
		t.Fatalf("期望返回 2 条，实际 %d 条", len(out))
	}
	for _, up := range out {
		if up.Config.Credential != "" {
			t.Errorf("列表返回的 Upstream 不应包含明文凭证，ID=%s Credential=%q", up.ID, up.Config.Credential)
		}
	}
}

// 以下针对任务 9.3（启用/停用与排序）补充 Manager 级编排行为的单元测试。
// ValidateReorder 纯函数的「恰好一次排列」校验由属性测试（任务 9.4 / Property 3）覆盖；
// 此处聚焦 Manager.Reorder 的编排语义：先列举已注册标识 → 校验 → 仅在合法时持久化，
// 非法排序在写库前即返回错误且不触达持久层（Req 3.4、3.5），以及 SetEnabled 的启停透传（Req 3.1、3.2）。

// testUpstreamRowWithID 构造一条带指定标识的最小上游行，供 Reorder 列举使用。
func testUpstreamRowWithID(id string) store.UpstreamRow {
	row := store.UpstreamRow{}
	row.ID = id
	row.Config = testValidConfig()
	return row
}

// TestSetEnabledPassesThrough 验证启用/停用将标识与状态透传给仓储（Req 3.1、3.2）。
func TestSetEnabledPassesThrough(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
	}{
		{name: "启用", enabled: true},
		{name: "停用", enabled: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &testUpstreamRepo{}
			m := New(repo, &testEncryptor{}, &testToolCacheCleaner{}, nil, nil)

			if err := m.SetEnabled(context.Background(), "up-x", tc.enabled); err != nil {
				t.Fatalf("SetEnabled 不应返回错误：%v", err)
			}
			if repo.setEnabledCall != 1 {
				t.Errorf("期望调用仓储 SetEnabled 1 次，实际 %d 次", repo.setEnabledCall)
			}
			if repo.lastEnabledID != "up-x" || repo.lastEnabled != tc.enabled {
				t.Errorf("期望透传 (up-x, %v)，实际 (%q, %v)", tc.enabled, repo.lastEnabledID, repo.lastEnabled)
			}
		})
	}
}

// TestReorderValidPersistsByPosition 验证合法排序按提交顺序逐个持久化，
// 且位置即排序值（由前到后递增，Req 3.4）。
func TestReorderValidPersistsByPosition(t *testing.T) {
	repo := &testUpstreamRepo{
		listRows: []store.UpstreamRow{
			testUpstreamRowWithID("a"),
			testUpstreamRowWithID("b"),
			testUpstreamRowWithID("c"),
		},
	}
	m := New(repo, &testEncryptor{}, &testToolCacheCleaner{}, nil, nil)

	// 提交一个合法重排（c, a, b）。
	ordered := []string{"c", "a", "b"}
	if err := m.Reorder(context.Background(), ordered); err != nil {
		t.Fatalf("合法排序不应返回错误：%v", err)
	}

	if len(repo.setSortOrderCalls) != 3 {
		t.Fatalf("期望持久化 3 次排序，实际 %d 次", len(repo.setSortOrderCalls))
	}
	for i, call := range repo.setSortOrderCalls {
		if call.id != ordered[i] {
			t.Errorf("第 %d 次持久化期望标识 %q，实际 %q", i, ordered[i], call.id)
		}
		if call.sortOrder != i {
			t.Errorf("标识 %q 期望排序值 %d（位置即排序），实际 %d", call.id, i, call.sortOrder)
		}
	}
}

// TestReorderInvalidDoesNotPersist 验证非法排序（未注册/缺失/重复）在写库前即被拒绝、
// 不触达持久层，从而保持当前已持久化的排序不变（Req 3.5）。
func TestReorderInvalidDoesNotPersist(t *testing.T) {
	registered := []store.UpstreamRow{
		testUpstreamRowWithID("a"),
		testUpstreamRowWithID("b"),
		testUpstreamRowWithID("c"),
	}
	cases := []struct {
		name    string
		ordered []string
	}{
		{name: "含未注册标识", ordered: []string{"a", "b", "z"}},
		{name: "缺失已注册标识", ordered: []string{"a", "b"}},
		{name: "含重复标识", ordered: []string{"a", "a", "b"}},
		{name: "空排序但存在已注册标识", ordered: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &testUpstreamRepo{listRows: registered}
			m := New(repo, &testEncryptor{}, &testToolCacheCleaner{}, nil, nil)

			err := m.Reorder(context.Background(), tc.ordered)

			apiErr := asAPIError(t, err)
			if apiErr.Code != domain.CodeValidation {
				t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
			}
			// 非法排序不得持久化任何排序值（Req 3.5）。
			if len(repo.setSortOrderCalls) != 0 {
				t.Errorf("非法排序不应持久化，实际持久化 %d 次", len(repo.setSortOrderCalls))
			}
		})
	}
}

// TestReorderListErrorPassthrough 验证列举已注册标识失败时透传错误且不持久化。
func TestReorderListErrorPassthrough(t *testing.T) {
	repo := &testUpstreamRepo{listErr: errors.New("数据库不可达")}
	m := New(repo, &testEncryptor{}, &testToolCacheCleaner{}, nil, nil)

	err := m.Reorder(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("列举失败时应返回错误")
	}
	if len(repo.setSortOrderCalls) != 0 {
		t.Errorf("列举失败时不应持久化排序，实际 %d 次", len(repo.setSortOrderCalls))
	}
}
