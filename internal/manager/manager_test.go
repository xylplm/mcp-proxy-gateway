package manager

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// sortOrderCall 记录一次排序写入的入参，用于断言 Reorder 的持久化行为。
type sortOrderCall struct {
	id        string
	sortOrder int
}

type reconnectTestDialer struct {
	mu     sync.Mutex
	conns  []*reconnectTestConn
	dialCh chan *reconnectTestConn
}

func newReconnectTestDialer() *reconnectTestDialer {
	return &reconnectTestDialer{dialCh: make(chan *reconnectTestConn, 4)}
}

func (d *reconnectTestDialer) Dial(ctx context.Context, id string, cfg domain.UpstreamConfig) (Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn := &reconnectTestConn{closed: make(chan struct{})}
	d.mu.Lock()
	d.conns = append(d.conns, conn)
	d.mu.Unlock()
	d.dialCh <- conn
	return conn, nil
}

func (d *reconnectTestDialer) dialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.conns)
}

type reconnectTestConn struct {
	closeOnce sync.Once
	closed    chan struct{}
}

func (c *reconnectTestConn) Wait(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (c *reconnectTestConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

type panicDialer struct {
	calls atomic.Int32
}

func (d *panicDialer) Dial(context.Context, string, domain.UpstreamConfig) (Conn, error) {
	d.calls.Add(1)
	panic("dial detail")
}

func waitReconnectDial(t *testing.T, d *reconnectTestDialer, label string) *reconnectTestConn {
	t.Helper()
	select {
	case conn := <-d.dialCh:
		return conn
	case <-time.After(2 * time.Second):
		t.Fatalf("等待 %s 拨号超时", label)
		return nil
	}
}

func waitReconnectClosed(t *testing.T, conn *reconnectTestConn, label string) {
	t.Helper()
	select {
	case <-conn.closed:
	case <-time.After(2 * time.Second):
		t.Fatalf("等待 %s 连接关闭超时", label)
	}
}

func waitManagerState(t *testing.T, m *Manager, id string, want domain.ConnState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := m.GetState(id)
		if state == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	state, lastErr := m.GetState(id)
	t.Fatalf("等待上游 %s 状态变为 %s 超时，实际 state=%s lastErr=%q", id, want, state, lastErr)
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

	// setSortOrderCalls 记录 Reorder 原子排序请求内的 id→sortOrder 有序列表，
	// 用于断言 Reorder 持久化次数与位置即排序的语义（Req 3.4）。
	setSortOrderCalls []sortOrderCall
	// reorderErr 注入 Reorder 的返回错误。
	reorderErr error

	// 最近一次 Create/Update 收到的入参，用于断言凭证已明文透传。
	lastCfg domain.UpstreamConfig

	// 注入的返回行与错误。
	createRow  *store.UpstreamRow
	createErr  error
	getRow     *store.UpstreamRow
	getErr     error
	updateRow  *store.UpstreamRow
	updateErr  error
	deleteErr  error
	setEnabled error
	listRows   []store.UpstreamRow
	listErr    error
}

func (r *testUpstreamRepo) Create(_ context.Context, cfg domain.UpstreamConfig) (*store.UpstreamRow, error) {
	r.createCalls++
	r.lastCfg = cfg
	if r.createErr != nil {
		return nil, r.createErr
	}
	return r.createRow, nil
}

func (r *testUpstreamRepo) Get(_ context.Context, _ string) (*store.UpstreamRow, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.getRow != nil {
		return r.getRow, nil
	}
	return nil, domain.NewError(domain.CodeNotFound, "上游 MCP 不存在")
}

func (r *testUpstreamRepo) List(_ context.Context) ([]store.UpstreamRow, error) {
	r.listCalls++
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.listRows, nil
}

func (r *testUpstreamRepo) Update(_ context.Context, _ string, cfg domain.UpstreamConfig) (*store.UpstreamRow, error) {
	r.updateCalls++
	r.lastCfg = cfg
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

func (r *testUpstreamRepo) Reorder(_ context.Context, orderedIDs []string) error {
	if r.reorderErr != nil {
		return r.reorderErr
	}
	for sortOrder, id := range orderedIDs {
		r.setSortOrderCalls = append(r.setSortOrderCalls, sortOrderCall{id: id, sortOrder: sortOrder})
	}
	return nil
}

func (r *testUpstreamRepo) Delete(_ context.Context, _ string) error {
	r.deleteCalls++
	return r.deleteErr
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
			m := New(repo, &testToolCacheCleaner{}, nil, nil)

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
			m := New(repo, &testToolCacheCleaner{}, nil, nil)

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
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

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
	// 校验失败必须不持久化（Req 2.2）。
	if repo.createCalls != 0 {
		t.Errorf("校验失败时不应调用仓储 Create，实际调用 %d 次", repo.createCalls)
	}
}

// TestCreateNameConflictPassthrough 验证名称冲突时仓储返回的 CONFLICT 被透传（Req 2.7）。
func TestCreateNameConflictPassthrough(t *testing.T) {
	repo := &testUpstreamRepo{
		createErr: domain.NewError(domain.CodeConflict, "上游 MCP 名称已存在：测试上游"),
	}
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

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

// TestCreateSuccessPersistsPlaintextCredential 验证创建成功路径：
//   - 凭证以明文随 cfg.Credential 整体透传给仓储（明文存储）；
//   - 返回的 Upstream 保留明文 Credential，供前端编辑回显。
func TestCreateSuccessPersistsPlaintextCredential(t *testing.T) {
	cfg := testValidConfig()

	// 构造仓储返回行（凭证明文回写）。
	row := &store.UpstreamRow{}
	row.ID = "up-1"
	row.Config = cfg

	repo := &testUpstreamRepo{createRow: row}
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

	up, err := m.Create(context.Background(), cfg)
	if err != nil {
		t.Fatalf("创建成功路径不应返回错误：%v", err)
	}

	// 仓储应收到含明文凭证的完整配置。
	if repo.lastCfg.Credential != cfg.Credential {
		t.Errorf("期望仓储收到明文凭证 %q，实际 %q", cfg.Credential, repo.lastCfg.Credential)
	}
	// 返回对象应保留明文凭证，便于前端编辑回显。
	if up.Config.Credential != cfg.Credential {
		t.Errorf("返回的 Upstream 应包含明文凭证，实际 %q", up.Config.Credential)
	}
	if up.ID != "up-1" {
		t.Errorf("期望返回 ID 为 up-1，实际 %q", up.ID)
	}
}

func TestCreateAppendsSortOrderAfterExistingUpstreams(t *testing.T) {
	cfg := testValidConfig()
	cfg.SortOrder = 0
	row := &store.UpstreamRow{}
	row.ID = "up-sort-new"
	row.Config = cfg

	existingA := testUpstreamRowWithID("up-sort-a")
	existingA.Config.SortOrder = 0
	existingB := testUpstreamRowWithID("up-sort-b")
	existingB.Config.SortOrder = 9
	existingC := testUpstreamRowWithID("up-sort-c")
	existingC.Config.SortOrder = 4

	repo := &testUpstreamRepo{
		createRow: row,
		listRows:  []store.UpstreamRow{existingA, existingB, existingC},
	}
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

	if _, err := m.Create(context.Background(), cfg); err != nil {
		t.Fatalf("create should not fail: %v", err)
	}
	if repo.lastCfg.SortOrder != 10 {
		t.Fatalf("new upstream should append after max sort order, got=%d want=10", repo.lastCfg.SortOrder)
	}
	if repo.createCalls != 1 {
		t.Fatalf("create should be persisted once, got=%d", repo.createCalls)
	}
}

func TestCreateListErrorDoesNotPersist(t *testing.T) {
	repo := &testUpstreamRepo{listErr: errors.New("list failed")}
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

	err := func() error {
		_, err := m.Create(context.Background(), testValidConfig())
		return err
	}()

	if err == nil {
		t.Fatal("create should return list error")
	}
	if repo.createCalls != 0 {
		t.Fatalf("create should not persist when sort order lookup fails, got createCalls=%d", repo.createCalls)
	}
}

// TestCreateWithoutCredentialPersistsEmpty 验证无凭证时仓储收到空凭证的配置。
func TestCreateWithoutCredentialPersistsEmpty(t *testing.T) {
	cfg := testValidConfig()
	cfg.Credential = ""

	row := &store.UpstreamRow{}
	row.ID = "up-2"
	row.Config = cfg

	repo := &testUpstreamRepo{createRow: row}
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

	if _, err := m.Create(context.Background(), cfg); err != nil {
		t.Fatalf("无凭证创建不应返回错误：%v", err)
	}
	if repo.lastCfg.Credential != "" {
		t.Errorf("无凭证时仓储应收到空凭证，实际 %q", repo.lastCfg.Credential)
	}
}

func TestCreateNormalizesTags(t *testing.T) {
	cfg := testValidConfig()
	cfg.Tags = []string{"  生产  ", "搜索", "生产", "Search", "search", ""}

	row := &store.UpstreamRow{}
	row.ID = "up-tags"
	row.Config = cfg

	repo := &testUpstreamRepo{createRow: row}
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

	if _, err := m.Create(context.Background(), cfg); err != nil {
		t.Fatalf("带标签创建不应返回错误：%v", err)
	}

	want := []string{"生产", "搜索", "Search"}
	if !reflect.DeepEqual(repo.lastCfg.Tags, want) {
		t.Fatalf("期望仓储收到归一化标签 %v，实际 %v", want, repo.lastCfg.Tags)
	}
}

func TestCreateRejectsInvalidTags(t *testing.T) {
	cases := []struct {
		name string
		tags []string
	}{
		{name: "标签数量超限", tags: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}},
		{name: "单个标签过长", tags: []string{strings.Repeat("标", maxTagLen+1)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &testUpstreamRepo{}
			m := New(repo, &testToolCacheCleaner{}, nil, nil)

			cfg := testValidConfig()
			cfg.Tags = tc.tags

			_, err := m.Create(context.Background(), cfg)

			apiErr := asAPIError(t, err)
			if apiErr.Code != domain.CodeValidation {
				t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
			}
			if _, ok := apiErr.Fields["tags"]; !ok {
				t.Errorf("期望字段级错误包含 tags，实际 Fields=%v", apiErr.Fields)
			}
			if repo.createCalls != 0 {
				t.Errorf("标签校验失败时不应调用仓储 Create，实际调用 %d 次", repo.createCalls)
			}
		})
	}
}

func TestCreateIgnoresDisabledRateLimitStaleValues(t *testing.T) {
	cfg := testValidConfig()
	cfg.RateLimits = domain.UpstreamRateLimits{
		Enabled:   false,
		PerMinute: -1,
		PerMonth:  1000,
		Timezone:  "Not/AZone",
	}
	row := &store.UpstreamRow{}
	row.ID = "up-rate-limit-disabled"
	row.Config = cfg

	repo := &testUpstreamRepo{createRow: row}
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

	if _, err := m.Create(context.Background(), cfg); err != nil {
		t.Fatalf("关闭限额时不应因隐藏的旧值或非法时区保存失败：%v", err)
	}
	want := domain.UpstreamRateLimits{Timezone: "UTC"}
	if !reflect.DeepEqual(repo.lastCfg.RateLimits, want) {
		t.Fatalf("关闭限额应归一化为零额度和 UTC 时区，got=%+v want=%+v", repo.lastCfg.RateLimits, want)
	}
}

func TestCreateRejectsInvalidEnabledRateLimitsWithFieldErrors(t *testing.T) {
	cfg := testValidConfig()
	cfg.RateLimits = domain.UpstreamRateLimits{
		Enabled:   true,
		PerMinute: -1,
		Timezone:  "Not/AZone",
	}
	repo := &testUpstreamRepo{}
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

	_, err := m.Create(context.Background(), cfg)

	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Fatalf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
	}
	for _, field := range []string{"rateLimits.timezone", "rateLimits.perMinute"} {
		if _, ok := apiErr.Fields[field]; !ok {
			t.Fatalf("期望字段级错误包含 %q，实际 Fields=%v", field, apiErr.Fields)
		}
	}
	if _, ok := apiErr.Fields["rateLimits"]; ok {
		t.Fatalf("限额错误应定位到具体字段，不应只返回 rateLimits：Fields=%v", apiErr.Fields)
	}
	if repo.createCalls != 0 {
		t.Fatalf("限额校验失败时不应调用仓储 Create，实际调用 %d 次", repo.createCalls)
	}
}

// TestUpdateNotFoundPassthrough 验证更新不存在的标识时透传 NOT_FOUND（Req 2.6）。
func TestUpdateNotFoundPassthrough(t *testing.T) {
	repo := &testUpstreamRepo{
		updateErr: domain.NewError(domain.CodeNotFound, "上游 MCP 不存在"),
	}
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

	_, err := m.Update(context.Background(), "missing-id", testValidConfig())

	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeNotFound {
		t.Errorf("期望透传错误码 %q，实际 %q", domain.CodeNotFound, apiErr.Code)
	}
}

// TestUpdateValidationDoesNotPersist 验证更新时字段校验失败不触达仓储（Req 2.2）。
func TestUpdateValidationDoesNotPersist(t *testing.T) {
	repo := &testUpstreamRepo{}
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

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
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

	_, err := m.Update(context.Background(), "some-id", testValidConfig())

	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeConflict {
		t.Errorf("期望透传错误码 %q，实际 %q", domain.CodeConflict, apiErr.Code)
	}
}

// TestUpdatePersistsPlaintextCredential 验证更新时凭证以明文随 cfg.Credential 整体覆盖存储，
// 不再区分 keep/replace/clear 三态。
func TestUpdatePersistsPlaintextCredential(t *testing.T) {
	cfg := testValidConfig()
	cfg.Credential = "new-token"
	row := &store.UpstreamRow{}
	row.ID = "up-update"
	row.Config = cfg

	repo := &testUpstreamRepo{updateRow: row}
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

	up, err := m.Update(context.Background(), "up-update", cfg)
	if err != nil {
		t.Fatalf("更新不应失败：%v", err)
	}
	// 仓储应收到含新明文凭证的完整配置。
	if repo.lastCfg.Credential != "new-token" {
		t.Fatalf("期望仓储收到明文凭证 %q，实际 %q", "new-token", repo.lastCfg.Credential)
	}
	// 返回对象保留明文凭证，便于前端编辑回显。
	if up.Config.Credential != "new-token" {
		t.Fatalf("返回的 Upstream 应包含明文凭证，实际 %q", up.Config.Credential)
	}
}

// TestDeleteNotFoundPassthrough 验证删除不存在的标识时透传 NOT_FOUND，
// 且仓储删除失败后不再清理缓存（Req 2.6）。
func TestDeleteNotFoundPassthrough(t *testing.T) {
	repo := &testUpstreamRepo{
		deleteErr: domain.NewError(domain.CodeNotFound, "上游 MCP 不存在"),
	}
	cache := &testToolCacheCleaner{}
	m := New(repo, cache, nil, nil)

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
	m := New(repo, cache, nil, nil)

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
	m := New(repo, cache, nil, nil)

	if err := m.Delete(context.Background(), "up-10"); err != nil {
		t.Fatalf("缓存清理失败不应令删除整体失败，却返回错误：%v", err)
	}
	if cache.calls != 1 {
		t.Errorf("期望尝试清理缓存 1 次，实际 %d 次", cache.calls)
	}
}

func TestDeletePersistErrorKeepsRunningConnection(t *testing.T) {
	cfg := testValidConfig()
	row := store.UpstreamRow{}
	row.ID = "up-delete-keep-conn"
	row.Config = cfg
	repo := &testUpstreamRepo{createRow: &row}
	dialer := newReconnectTestDialer()
	m := New(repo, &testToolCacheCleaner{}, nil, nil, WithDialer(dialer))
	defer m.Shutdown()

	if _, err := m.Create(context.Background(), cfg); err != nil {
		t.Fatalf("创建上游不应失败：%v", err)
	}
	conn := waitReconnectDial(t, dialer, "删除前")
	waitManagerState(t, m, "up-delete-keep-conn", domain.ConnAvailable)

	repo.deleteErr = errors.New("数据库删除失败")
	if err := m.Delete(context.Background(), "up-delete-keep-conn"); err == nil {
		t.Fatal("删除持久化失败时应返回错误")
	}
	select {
	case <-conn.closed:
		t.Fatal("删除持久化失败时不应关闭仍存在的上游连接")
	case <-time.After(50 * time.Millisecond):
	}
	state, _ := m.GetState("up-delete-keep-conn")
	if state != domain.ConnAvailable {
		t.Fatalf("删除失败后连接状态应保持可用，got=%s", state)
	}
}

// TestSetEnabledNotFoundPassthrough 验证启停不存在的标识时透传 NOT_FOUND（Req 2.6）。
func TestSetEnabledNotFoundPassthrough(t *testing.T) {
	repo := &testUpstreamRepo{
		getErr: domain.NewError(domain.CodeNotFound, "上游 MCP 不存在"),
	}
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

	err := m.SetEnabled(context.Background(), "missing-id", true)

	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeNotFound {
		t.Errorf("期望透传错误码 %q，实际 %q", domain.CodeNotFound, apiErr.Code)
	}
	if repo.setEnabledCall != 0 {
		t.Fatalf("未找到持久化配置时不应写入启停状态，实际调用 %d 次", repo.setEnabledCall)
	}
}

// TestListEmptyReturnsEmptySlice 验证无数据时返回空切片且无错误（Req 2.8）。
func TestListEmptyReturnsEmptySlice(t *testing.T) {
	repo := &testUpstreamRepo{listRows: []store.UpstreamRow{}}
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

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

// TestListMapsRowsAndReturnsCredential 验证列表正确映射多条记录，且凭证明文随配置原样回显，
// 便于前端编辑时回填（Req 2.3）。
func TestListMapsRowsAndReturnsCredential(t *testing.T) {
	row1 := store.UpstreamRow{}
	row1.ID = "a"
	row1.Config = testValidConfig()
	row1.Config.Credential = "明文凭证应回显"

	row2 := store.UpstreamRow{}
	row2.ID = "b"
	row2.Config = testValidConfig()

	repo := &testUpstreamRepo{listRows: []store.UpstreamRow{row1, row2}}
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

	out, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("列表查询不应返回错误：%v", err)
	}
	if len(out) != 2 {
		t.Fatalf("期望返回 2 条，实际 %d 条", len(out))
	}
	// 凭证应随配置原样回显（含已设置与未设置两种情况）。
	creds := map[string]string{out[0].ID: out[0].Config.Credential, out[1].ID: out[1].Config.Credential}
	if creds["a"] != "明文凭证应回显" {
		t.Errorf("上游 a 的明文凭证应回显，实际 %q", creds["a"])
	}
	if creds["b"] != testValidConfig().Credential {
		t.Errorf("上游 b 的明文凭证应回显默认值，实际 %q", creds["b"])
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
			row := testUpstreamRowWithID("up-x")
			repo := &testUpstreamRepo{getRow: &row}
			m := New(repo, &testToolCacheCleaner{}, nil, nil)

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
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

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
			m := New(repo, &testToolCacheCleaner{}, nil, nil)

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
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

	err := m.Reorder(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("列举失败时应返回错误")
	}
	if len(repo.setSortOrderCalls) != 0 {
		t.Errorf("列举失败时不应持久化排序，实际 %d 次", len(repo.setSortOrderCalls))
	}
}

func TestReorderPersistErrorReturnsWithoutPartialManagerWrites(t *testing.T) {
	repo := &testUpstreamRepo{
		listRows: []store.UpstreamRow{
			testUpstreamRowWithID("a"),
			testUpstreamRowWithID("b"),
			testUpstreamRowWithID("c"),
		},
		reorderErr: errors.New("事务提交失败"),
	}
	m := New(repo, &testToolCacheCleaner{}, nil, nil)

	err := m.Reorder(context.Background(), []string{"c", "a", "b"})

	if err == nil {
		t.Fatal("排序持久化失败时应返回错误")
	}
	if len(repo.setSortOrderCalls) != 0 {
		t.Fatalf("Manager 不应逐条写入排序，避免失败半状态；实际写入 %d 次", len(repo.setSortOrderCalls))
	}
}

func TestReconnectRestartsRunningConnection(t *testing.T) {
	cfg := testValidConfig()
	row := store.UpstreamRow{}
	row.ID = "up-reconnect"
	row.Config = cfg
	row.Config.Credential = ""
	repo := &testUpstreamRepo{createRow: &row}
	dialer := newReconnectTestDialer()
	m := New(repo, &testToolCacheCleaner{}, nil, nil, WithDialer(dialer))
	defer m.Shutdown()

	if _, err := m.Create(context.Background(), cfg); err != nil {
		t.Fatalf("创建上游不应失败：%v", err)
	}
	first := waitReconnectDial(t, dialer, "首次")
	waitManagerState(t, m, "up-reconnect", domain.ConnAvailable)

	if err := m.Reconnect(context.Background(), "up-reconnect"); err != nil {
		t.Fatalf("重连不应失败：%v", err)
	}
	waitReconnectClosed(t, first, "旧")
	second := waitReconnectDial(t, dialer, "重连后")
	if second == first {
		t.Fatal("重连后应创建新连接，而不是复用旧连接")
	}
	waitManagerState(t, m, "up-reconnect", domain.ConnAvailable)
	if dialer.dialCount() != 2 {
		t.Fatalf("期望重连前后共拨号 2 次，实际 %d 次", dialer.dialCount())
	}
}

func TestRestoreConnectionsRegistersPersistedUpstreams(t *testing.T) {
	cfg := testValidConfig()
	row := testUpstreamRowWithID("up-restore")
	row.Config = cfg
	row.Config.Credential = "stored-token"
	repo := &testUpstreamRepo{listRows: []store.UpstreamRow{row}}
	dialer := newReconnectTestDialer()
	m := New(repo, &testToolCacheCleaner{}, nil, nil, WithDialer(dialer))
	defer m.Shutdown()

	if err := m.RestoreConnections(context.Background()); err != nil {
		t.Fatalf("恢复已有连接不应失败：%v", err)
	}
	waitReconnectDial(t, dialer, "恢复")
	waitManagerState(t, m, "up-restore", domain.ConnAvailable)

	m.connsMu.Lock()
	conn := m.conns["up-restore"]
	m.connsMu.Unlock()
	if conn == nil {
		t.Fatal("恢复后应登记连接条目")
	}
	if got := conn.config().Credential; got != "stored-token" {
		t.Fatalf("恢复连接应携带明文凭证，实际 %q", got)
	}
}

func TestSetEnabledRestoresMissingConnectionFromStore(t *testing.T) {
	cfg := testValidConfig()
	cfg.Enabled = false
	row := testUpstreamRowWithID("up-enable")
	row.Config = cfg
	row.Config.Credential = "stored-token"
	repo := &testUpstreamRepo{getRow: &row}
	dialer := newReconnectTestDialer()
	m := New(repo, &testToolCacheCleaner{}, nil, nil, WithDialer(dialer))
	defer m.Shutdown()

	if err := m.SetEnabled(context.Background(), "up-enable", true); err != nil {
		t.Fatalf("启用未登记连接应从仓储恢复，不应失败：%v", err)
	}
	waitReconnectDial(t, dialer, "启用恢复")
	waitManagerState(t, m, "up-enable", domain.ConnAvailable)
	if repo.setEnabledCall != 1 || repo.lastEnabledID != "up-enable" || !repo.lastEnabled {
		t.Fatalf("启用状态应先持久化，calls=%d id=%q enabled=%v", repo.setEnabledCall, repo.lastEnabledID, repo.lastEnabled)
	}
}

func TestReconnectRestoresMissingConnectionFromStore(t *testing.T) {
	cfg := testValidConfig()
	row := testUpstreamRowWithID("up-recover-reconnect")
	row.Config = cfg
	row.Config.Credential = "stored-token"
	repo := &testUpstreamRepo{getRow: &row}
	dialer := newReconnectTestDialer()
	m := New(repo, &testToolCacheCleaner{}, nil, nil, WithDialer(dialer))
	defer m.Shutdown()

	if err := m.Reconnect(context.Background(), "up-recover-reconnect"); err != nil {
		t.Fatalf("重连未登记但已持久化的上游应从仓储恢复，不应失败：%v", err)
	}
	waitReconnectDial(t, dialer, "重连恢复")
	waitManagerState(t, m, "up-recover-reconnect", domain.ConnAvailable)
}

func TestConnectionLoopRecoversDialPanic(t *testing.T) {
	cfg := testValidConfig()
	cfg.Enabled = true
	row := testUpstreamRowWithID("up-panic-dial")
	row.Config = cfg
	repo := &testUpstreamRepo{listRows: []store.UpstreamRow{row}}
	dialer := &panicDialer{}
	m := New(repo, &testToolCacheCleaner{}, nil, nil,
		WithDialer(dialer),
		WithRetryPolicy(RetryPolicy{InitialBackoff: time.Hour, MaxBackoff: time.Hour, Multiplier: 2, FailureThreshold: 1}),
	)
	defer m.Shutdown()

	if err := m.RestoreConnections(context.Background()); err != nil {
		t.Fatalf("恢复连接不应返回错误：%v", err)
	}
	waitManagerState(t, m, "up-panic-dial", domain.ConnSuspended)
	if dialer.calls.Load() != 1 {
		t.Fatalf("panic 拨号应记录一次失败并进入 suspended，实际拨号次数=%d", dialer.calls.Load())
	}
	_, lastErr := m.GetState("up-panic-dial")
	if !strings.Contains(lastErr, "拨号异常") {
		t.Fatalf("panic 应记录为连接失败原因，实际=%q", lastErr)
	}
}

// recoveryDialer 可先按失败序列拨号，随后成功创建受控连接；用于验证长期不可用
// 时仍低频探测，以及多个调用等待同一连接循环的单飞语义。
type recoveryDialer struct {
	mu       sync.Mutex
	failures int
	calls    int
	dialCh   chan struct{}
	conn     *reconnectTestConn
}

func newRecoveryDialer(failures int) *recoveryDialer {
	return &recoveryDialer{failures: failures, dialCh: make(chan struct{}, 32)}
}

func (d *recoveryDialer) Dial(ctx context.Context, _ string, _ domain.UpstreamConfig) (Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.calls++
	call := d.calls
	if d.failures > 0 {
		d.failures--
		d.mu.Unlock()
		d.dialCh <- struct{}{}
		return nil, errors.New("上游暂不可用")
	}
	conn := &reconnectTestConn{closed: make(chan struct{})}
	d.conn = conn
	d.mu.Unlock()
	d.dialCh <- struct{}{}
	_ = call
	return conn, nil
}

func (d *recoveryDialer) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func TestSuspendedConnectionKeepsLowFrequencyProbing(t *testing.T) {
	cfg := testValidConfig()
	row := testUpstreamRowWithID("up-sustained-recovery")
	row.Config = cfg
	repo := &testUpstreamRepo{listRows: []store.UpstreamRow{row}}
	dialer := newRecoveryDialer(2)
	m := New(repo, &testToolCacheCleaner{}, nil, nil,
		WithDialer(dialer),
		WithRetryPolicy(RetryPolicy{
			InitialBackoff:          10 * time.Millisecond,
			MaxBackoff:              20 * time.Millisecond,
			Multiplier:              2,
			FailureThreshold:        1,
			DemandReconnectCooldown: time.Second,
			DemandReconnectWait:     50 * time.Millisecond,
		}),
	)
	defer m.Shutdown()

	if err := m.RestoreConnections(context.Background()); err != nil {
		t.Fatalf("恢复连接不应失败：%v", err)
	}
	waitManagerState(t, m, "up-sustained-recovery", domain.ConnSuspended)

	// 阈值为 1 后仍会在 MaxBackoff 到期时继续第二次探测，而不是永久等待人工重连。
	select {
	case <-dialer.dialCh:
	case <-time.After(time.Second):
		t.Fatal("等待首次失败拨号超时")
	}
	select {
	case <-dialer.dialCh:
	case <-time.After(time.Second):
		t.Fatal("suspended 状态应在最大退避后继续低频探测")
	}
	waitManagerState(t, m, "up-sustained-recovery", domain.ConnSuspended)

	// 第三轮拨号成功，上游无需任何人工操作即可恢复可用。
	select {
	case <-dialer.dialCh:
	case <-time.After(time.Second):
		t.Fatal("等待自动恢复拨号超时")
	}
	waitManagerState(t, m, "up-sustained-recovery", domain.ConnAvailable)
	if got := dialer.callCount(); got != 3 {
		t.Fatalf("期望持续探测共拨号 3 次，实际 %d", got)
	}
}

func TestWaitForAvailableCoalescesConcurrentDemandReconnect(t *testing.T) {
	cfg := testValidConfig()
	row := testUpstreamRowWithID("up-demand-singleflight")
	row.Config = cfg
	repo := &testUpstreamRepo{listRows: []store.UpstreamRow{row}}
	dialer := newRecoveryDialer(1)
	m := New(repo, &testToolCacheCleaner{}, nil, nil,
		WithDialer(dialer),
		WithRetryPolicy(RetryPolicy{
			InitialBackoff:          time.Hour,
			MaxBackoff:              time.Hour,
			Multiplier:              2,
			FailureThreshold:        10,
			DemandReconnectCooldown: time.Second,
			DemandReconnectWait:     time.Second,
		}),
	)
	defer m.Shutdown()

	if err := m.RestoreConnections(context.Background()); err != nil {
		t.Fatalf("恢复连接不应失败：%v", err)
	}
	select {
	case <-dialer.dialCh:
	case <-time.After(time.Second):
		t.Fatal("等待首次失败拨号超时")
	}
	waitManagerState(t, m, "up-demand-singleflight", domain.ConnUnavailable)

	const waiters = 32
	results := make(chan error, waiters)
	for i := 0; i < waiters; i++ {
		go func() {
			results <- m.WaitForAvailable(context.Background(), "up-demand-singleflight")
		}()
	}
	for i := 0; i < waiters; i++ {
		if err := <-results; err != nil {
			t.Fatalf("并发等待自动恢复不应失败：%v", err)
		}
	}
	waitManagerState(t, m, "up-demand-singleflight", domain.ConnAvailable)
	if got := dialer.callCount(); got != 2 {
		t.Fatalf("并发按需恢复应合并为一次额外拨号，实际拨号 %d 次", got)
	}
}

func TestWaitForAvailableRespectsWaitBudget(t *testing.T) {
	cfg := testValidConfig()
	row := testUpstreamRowWithID("up-demand-timeout")
	row.Config = cfg
	repo := &testUpstreamRepo{listRows: []store.UpstreamRow{row}}
	dialer := newRecoveryDialer(100)
	m := New(repo, &testToolCacheCleaner{}, nil, nil,
		WithDialer(dialer),
		WithRetryPolicy(RetryPolicy{
			InitialBackoff:          time.Hour,
			MaxBackoff:              time.Hour,
			Multiplier:              2,
			FailureThreshold:        10,
			DemandReconnectCooldown: time.Second,
			DemandReconnectWait:     20 * time.Millisecond,
		}),
	)
	defer m.Shutdown()

	if err := m.RestoreConnections(context.Background()); err != nil {
		t.Fatalf("恢复连接不应失败：%v", err)
	}
	select {
	case <-dialer.dialCh:
	case <-time.After(time.Second):
		t.Fatal("等待首次失败拨号超时")
	}
	waitManagerState(t, m, "up-demand-timeout", domain.ConnUnavailable)

	started := time.Now()
	err := m.WaitForAvailable(context.Background(), "up-demand-timeout")
	if err == nil {
		t.Fatal("持续不可用时按需等待应返回错误")
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("按需等待不应长期堆积，实际等待 %v", elapsed)
	}
}

// RetryUnavailable 只唤醒失败中的已启用上游：正在服务的连接不能被打断，
// 停用的上游也不该被悄悄拉起。
func TestRetryUnavailableOnlyWakesFailedEnabledUpstreams(t *testing.T) {
	m := New(&testUpstreamRepo{}, &testToolCacheCleaner{}, nil, nil)
	defer m.Shutdown()

	cases := []struct {
		id        string
		state     domain.ConnState
		enabled   bool
		wantWoken bool
	}{
		{id: "suspended", state: domain.ConnSuspended, enabled: true, wantWoken: true},
		{id: "unavailable", state: domain.ConnUnavailable, enabled: true, wantWoken: true},
		{id: "available", state: domain.ConnAvailable, enabled: true, wantWoken: false},
		{id: "connecting", state: domain.ConnConnecting, enabled: true, wantWoken: false},
		{id: "disabled", state: domain.ConnSuspended, enabled: false, wantWoken: false},
	}
	for _, tc := range cases {
		c := newConnection(tc.id)
		c.state = tc.state
		c.cfg = domain.UpstreamConfig{Enabled: tc.enabled}
		m.conns[tc.id] = c
	}

	wantCount := 0
	for _, tc := range cases {
		if tc.wantWoken {
			wantCount++
		}
	}
	if got := m.RetryUnavailable(); got != wantCount {
		t.Fatalf("唤醒数量 got=%d want=%d", got, wantCount)
	}
	for _, tc := range cases {
		c := m.conns[tc.id]
		woken := len(c.wake) == 1
		if woken != tc.wantWoken {
			t.Fatalf("%s: woken=%v want=%v", tc.id, woken, tc.wantWoken)
		}
	}
}
