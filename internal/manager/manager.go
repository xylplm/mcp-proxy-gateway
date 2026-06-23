package manager

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
	"github.com/myGithub/mcp-proxy-gateway/internal/transport"
)

// 名称长度约束：服务名称需在 1 至 100 个字符之间（Req 2.1、2.2）。
const (
	// minNameLen 为上游 MCP 名称的最小字符数。
	minNameLen = 1
	// maxNameLen 为上游 MCP 名称的最大字符数。
	maxNameLen = 100
	// maxTags 为单个上游允许配置的标签数量上限。
	maxTags = 8
	// maxTagLen 为单个标签的最大字符数。
	maxTagLen = 32
)

// UpstreamRepository 是连接管理器依赖的上游 MCP 仓储窄接口。
//
// 仅声明本组件实际使用的方法，便于在单元测试（任务 9.2）中以 mock 替换，
// 同时使依赖关系一目了然。*store.UpstreamRepo 满足该接口。
type UpstreamRepository interface {
	// Create 持久化上游配置，凭证以明文随 cfg.Credential 存储（可为空）。
	Create(ctx context.Context, cfg domain.UpstreamConfig) (*store.UpstreamRow, error)
	// Get 按标识查询单条上游；不存在返回 NOT_FOUND。
	Get(ctx context.Context, id string) (*store.UpstreamRow, error)
	// List 返回全部上游，无数据返回空切片。
	List(ctx context.Context) ([]store.UpstreamRow, error)
	// Update 更新指定上游的配置（凭证明文随 cfg.Credential）。
	Update(ctx context.Context, id string, cfg domain.UpstreamConfig) (*store.UpstreamRow, error)
	// SetEnabled 仅更新启停状态。
	SetEnabled(ctx context.Context, id string, enabled bool) error
	// Reorder 原子更新全部上游的排序值。
	Reorder(ctx context.Context, orderedIDs []string) error
	// Delete 删除指定上游，其从属规则与缓存持久副本由 DB 外键级联清理。
	Delete(ctx context.Context, id string) error
}

// ToolCacheCleaner 是工具缓存清理窄接口，用于删除上游时级联清理缓存（Req 2.5、6.6）。
//
// *cache.ToolCache 满足该接口。
type ToolCacheCleaner interface {
	// Delete 删除某上游 MCP 的缓存工具列表（Redis 热路径 + PG 持久副本）。
	Delete(ctx context.Context, upstreamID string) error
}

// ConnParamsValidator 校验上游配置的传输类型与连接参数是否齐备且格式合法。
//
// 默认实现为 transport.ValidateConnParams；以函数注入便于测试替换。
type ConnParamsValidator func(cfg domain.UpstreamConfig) error

// Manager 是连接管理器（MCP_Manager）的实现。
//
// 任务 9.1 实现上游 MCP 的增删查改、名称唯一与字段校验；凭证以明文存储，便于编辑回显；
// 任务 9.3 实现启用/停用（SetEnabled）与排序（Reorder，含完整性校验）；
// 任务 9.5 实现连接生命周期状态机与指数退避重试（GetState/Reconnect/连接重建）。
//
// 连接生命周期由每条上游的 *Connection 维护，集中在 lifecycle.go。Manager 持有连接池
// （conns）、退避策略（policy）与拨号器（dialer）。Dialer 为 nil 时连接条目仅作占位
// 登记、不启动真实重试循环，便于在尚未接线传输层时使用本组件（拨号器由装配层注入）。
type Manager struct {
	// repo 为上游 MCP 仓储。
	repo UpstreamRepository
	// cache 为工具缓存清理能力。
	cache ToolCacheCleaner
	// validate 为连接参数校验函数。
	validate ConnParamsValidator
	// logger 用于记录告警/降级路径的诊断信息。
	logger *slog.Logger

	// policy 为连接重试退避策略（Req 5.1-5.6）。
	policy RetryPolicy
	// dialer 为上游连接拨号器；为 nil 时不启动真实重试循环。
	dialer Dialer

	// connsMu 保护连接池。
	connsMu sync.Mutex
	// conns 为每条上游 MCP 的连接生命周期状态机，键为上游标识。
	conns map[string]*Connection

	// baseCtx 为所有连接重试循环的根上下文，Shutdown 时取消。
	baseCtx context.Context
	// baseCancel 取消 baseCtx。
	baseCancel context.CancelFunc
	// loopWG 等待全部后台重试循环退出。
	loopWG sync.WaitGroup
}

// 编译期断言：Manager 必须满足 domain.MCP_Manager 接口契约。
var _ domain.MCP_Manager = (*Manager)(nil)

// Option 为 Manager 的可选配置项（函数式选项）。
type Option func(*Manager)

// WithRetryPolicy 注入连接重试退避策略（Req 5.1-5.6）。
//
// 装配层（任务 27.2）负责把 config.ConnectionConfig 映射为 RetryPolicy 并经此注入。
func WithRetryPolicy(policy RetryPolicy) Option {
	return func(m *Manager) {
		m.policy = policy.normalize()
	}
}

// WithDialer 注入上游连接拨号器，启用真实的连接建立与重试退避循环。
//
// 未注入时连接条目仅作占位登记，不启动后台重试循环。
func WithDialer(d Dialer) Option {
	return func(m *Manager) {
		m.dialer = d
	}
}

// New 构造连接管理器。
//
// validate 为 nil 时回退到 transport.ValidateConnParams；logger 为 nil 时回退到
// slog.Default()。repo、cache 为必需依赖。退避策略默认取 DefaultRetryPolicy，
// 可经 WithRetryPolicy 覆盖；拨号器经 WithDialer 注入。
func New(
	repo UpstreamRepository,
	cache ToolCacheCleaner,
	validate ConnParamsValidator,
	logger *slog.Logger,
	opts ...Option,
) *Manager {
	if validate == nil {
		validate = transport.ValidateConnParams
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		repo:       repo,
		cache:      cache,
		validate:   validate,
		logger:     logger,
		policy:     DefaultRetryPolicy(),
		conns:      make(map[string]*Connection),
		baseCtx:    ctx,
		baseCancel: cancel,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Create 创建上游 MCP 服务（Req 2.1、2.2、2.7）。
//
// 流程：字段校验 → 持久化。任一字段非法时返回标识每个无效字段的
// 校验错误且不持久化（Req 2.2）；名称与既有上游重复时仓储层返回 CONFLICT（Req 2.7）。
// 凭证以明文随 cfg.Credential 存储，返回的 Upstream 含明文凭证以便前端编辑回显。
func (m *Manager) Create(ctx context.Context, cfg domain.UpstreamConfig) (domain.Upstream, error) {
	var err error
	cfg, err = m.validateConfig(cfg)
	if err != nil {
		return domain.Upstream{}, err
	}
	cfg.SortOrder, err = m.nextSortOrder(ctx)
	if err != nil {
		return domain.Upstream{}, err
	}

	row, err := m.repo.Create(ctx, cfg)
	if err != nil {
		return domain.Upstream{}, err
	}

	// 登记连接并按启停状态启动重试退避循环（启用）或置为不可用（停用）（Req 5.1、5.4）。
	// 连接持有完整配置（含明文凭证，仅内存），供拨号与后续重建使用。
	cfg.SortOrder = row.Config.SortOrder
	m.rebuildConnection(row.ID, cfg)
	return m.toUpstream(row), nil
}

func (m *Manager) nextSortOrder(ctx context.Context) (int, error) {
	rows, err := m.repo.List(ctx)
	if err != nil {
		return 0, err
	}
	maxOrder := -1
	for i := range rows {
		if rows[i].Config.SortOrder > maxOrder {
			maxOrder = rows[i].Config.SortOrder
		}
	}
	return maxOrder + 1, nil
}

// Update 更新某个已存在的上游 MCP 服务配置（Req 2.4、2.6、2.7）。
//
// 流程：字段校验 → 持久化 → 按新配置重建连接。标识不存在返回 NOT_FOUND
// （Req 2.6），名称与其他上游重复返回 CONFLICT（Req 2.7）。
//
// 连接重建（Req 2.4 后半句）：停止该上游既有的重试循环、写入新配置，再按新配置的
// 启停状态重新拨号建立连接（或在停用时置为不可用），从而使更新后的连接参数立即生效。
// 凭证以明文随 cfg.Credential 整体覆盖，不再区分 keep/replace/clear。
func (m *Manager) Update(ctx context.Context, id string, cfg domain.UpstreamConfig) (domain.Upstream, error) {
	var err error
	cfg, err = m.validateConfig(cfg)
	if err != nil {
		return domain.Upstream{}, err
	}

	row, err := m.repo.Update(ctx, id, cfg)
	if err != nil {
		return domain.Upstream{}, err
	}

	// 按新配置重建对应连接（Req 2.4）。
	cfg.SortOrder = row.Config.SortOrder
	m.rebuildConnection(id, cfg)
	return m.toUpstream(row), nil
}

// RestoreConnections 从持久化仓储恢复全部上游的连接状态机登记。
//
// 该方法用于进程启动后把数据库中已有的上游重新放回内存连接池；配置中的明文凭证
// 仅保存在内存中供连接拨号使用。已启用上游会启动连接循环，停用上游仅登记
// 为 unavailable，便于后续启用/重连时复用同一条恢复路径。
func (m *Manager) RestoreConnections(ctx context.Context) error {
	rows, err := m.repo.List(ctx)
	if err != nil {
		return err
	}
	for i := range rows {
		m.rebuildConnection(rows[i].ID, rows[i].Config)
	}
	return nil
}

func (m *Manager) restoreConnectionFromStore(ctx context.Context, id string, enabled bool) error {
	row, err := m.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	cfg := row.Config
	cfg.Enabled = enabled
	m.rebuildConnection(id, cfg)
	return nil
}

// Delete 删除某个已存在的上游 MCP 服务并级联清理（Req 2.5、2.6、6.6）。
//
// 流程：删除上游记录（其从属别名/屏蔽规则、ACL 与工具缓存 PG 持久副本由 DB 外键
// ON DELETE CASCADE 级联清理）→ 关闭并移除连接（停止重试循环、断开活跃连接）→
// 清理工具缓存（清除 Redis 热路径键）。标识不存在返回 NOT_FOUND（Req 2.6）。
//
// 工具缓存清理为尽力而为：DB 删除是真相来源，Redis 仅为热路径，清理失败仅记录
// 告警而不令整体删除失败。
func (m *Manager) Delete(ctx context.Context, id string) error {
	// 删除上游记录；不存在返回 NOT_FOUND，从属规则由 DB 级联清理。
	if err := m.repo.Delete(ctx, id); err != nil {
		return err
	}

	// DB 删除成功后再关闭运行态连接，避免删除失败时留下“配置仍存在但连接已停”的半状态。
	m.removeConnection(id)

	// 级联清理工具缓存（主要用于清除 Redis 热路径键）。
	if err := m.cache.Delete(ctx, id); err != nil {
		m.logger.Warn("删除上游后清理工具缓存失败（DB 已删除，缓存为尽力而为）",
			"upstreamID", id, "error", err)
	}
	return nil
}

// SetEnabled 启用或停用某个上游 MCP 服务（Req 3.1、3.2）。
//
// 将该上游标记为启用/停用并持久化。启停对聚合「即时生效」无需额外处理：聚合管线
// 每次实时从工具缓存读取 enabled=true 的上游构建可见集合（见设计「为何不缓存聚合
// 结果」），因此该次更新之后接收的聚合请求自然按最新启停状态纳入或排除其工具
// （Req 3.1、3.2、3.3）。标识不存在返回 NOT_FOUND。
//
// 同时同步连接生命周期：启用时启动重试退避循环建立连接，停用时停止循环并置为不可用。
func (m *Manager) SetEnabled(ctx context.Context, id string, enabled bool) error {
	m.connsMu.Lock()
	c := m.conns[id]
	m.connsMu.Unlock()

	var cfg domain.UpstreamConfig
	if c != nil {
		// 同步连接状态：复用连接已持有的配置（含凭证），仅切换启停。
		cfg = c.config()
	} else {
		// 内存连接缺失时先读取持久化配置，避免启停写入成功后因回查失败留下半状态。
		row, err := m.repo.Get(ctx, id)
		if err != nil {
			return err
		}
		cfg = row.Config
	}
	cfg.Enabled = enabled

	if err := m.repo.SetEnabled(ctx, id, enabled); err != nil {
		return err
	}

	m.rebuildConnection(id, cfg)
	return nil
}

// List 返回所有已配置的上游 MCP 服务及其当前连接状态（Req 2.3、2.8）。
//
// 无数据时返回空切片而非错误（Req 2.8）。每条记录的连接状态由 GetState 提供，
// 反映连接生命周期状态机维护的实时状态。
func (m *Manager) List(ctx context.Context) ([]domain.Upstream, error) {
	rows, err := m.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Upstream, 0, len(rows))
	for i := range rows {
		out = append(out, m.toUpstream(&rows[i]))
	}
	return out, nil
}

// Reorder 提交新的排序顺序（Req 3.4、3.5）。
//
// 流程：先读取全部已注册上游标识，再校验 orderedIDs 为其「恰好一次排列」
// （ValidateReorder，拒绝未注册/缺失/重复标识）。仅当校验通过时才按 orderedIDs
// 的位置逐个持久化各上游的排序值（位置即 sort_order，由前到后递增）；校验不通过
// 则在写库前即返回携带具体原因的 VALIDATION 错误，已持久化的排序状态保持不变
// （Req 3.5）。
//
// 持久化采用「校验后原子写入」次序：合法排序是已注册标识的双射，仓储层以事务一次性
// 更新全部 sort_order，避免部分写入成功后返回错误导致管理台状态与用户反馈不一致；
// 排序生效后聚合管线据 sort_order 由前到后合并（Req 3.4、10.1）。
func (m *Manager) Reorder(ctx context.Context, orderedIDs []string) error {
	rows, err := m.repo.List(ctx)
	if err != nil {
		return err
	}

	registered := make([]string, 0, len(rows))
	for i := range rows {
		registered = append(registered, rows[i].ID)
	}

	// 完整性校验：非法（未注册/缺失/重复）时直接返回错误，不触达持久层，
	// 从而保持当前已持久化的排序不变（Req 3.5）。
	if err := ValidateReorder(registered, orderedIDs); err != nil {
		return err
	}

	// 校验通过：交由仓储层事务更新全部排序值，保证成功整体生效、失败整体不变。
	return m.repo.Reorder(ctx, orderedIDs)
}

// GetState 返回连接状态与最近一次失败原因（Req 5.4）。
//
// 由连接生命周期状态机维护：返回该上游当前的 connecting/available/unavailable/
// suspended 状态及最近一次失败原因。当前生效的退避上限可通过 ConnStatus 查询（Req 5.3）。
// 未登记的上游返回默认 unavailable 状态与空失败原因。
func (m *Manager) GetState(id string) (domain.ConnState, string) {
	m.connsMu.Lock()
	c := m.conns[id]
	m.connsMu.Unlock()
	if c == nil {
		return domain.ConnUnavailable, ""
	}
	state, lastErr, _ := c.snapshot()
	return state, lastErr
}

// Reconnect 由管理员手动发起重连（Req 5.6）。
//
// 行为：按连接持有的最新配置停止旧循环并重新启动，使可用、退避中、挂起中的连接
// 都能真正重新拨号，而不是仅修改状态或唤醒等待。
//
// 标识不存在返回 NOT_FOUND。
func (m *Manager) Reconnect(ctx context.Context, id string) error {
	m.connsMu.Lock()
	c := m.conns[id]
	m.connsMu.Unlock()
	if c == nil {
		return m.restoreConnectionFromStore(ctx, id, true)
	}

	cfg := c.config()
	cfg.Enabled = true
	m.rebuildConnection(id, cfg)
	return nil
}

// validateConfig 校验上游配置的必填字段与格式（Req 2.2）。
//
// 校验项：
//   - 名称长度需在 1 至 100 个字符之间，且不可为空白；
//   - 传输类型与连接参数由 transport.ValidateConnParams 校验。
//
// 任一项不通过即返回携带字段级说明的 VALIDATION 错误，由调用方据此拒绝写入、
// 不持久化任何变更。
func (m *Manager) validateConfig(cfg domain.UpstreamConfig) (domain.UpstreamConfig, error) {
	fields := make(map[string]string)

	// 名称必填且长度 1-100（按 Unicode 字符计数）。
	if strings.TrimSpace(cfg.Name) == "" {
		fields["name"] = "名称为必填项，长度需在 1 至 100 个字符之间"
	} else if n := utf8.RuneCountInString(cfg.Name); n < minNameLen || n > maxNameLen {
		fields["name"] = "名称长度需在 1 至 100 个字符之间"
	}

	// 传输类型与连接参数校验，合并字段级错误。
	if err := m.validate(cfg); err != nil {
		mergeFields(fields, err)
	}

	if normalized, err := normalizeTags(cfg.Tags); err != nil {
		fields["tags"] = err.Error()
	} else {
		cfg.Tags = normalized
	}
	if normalized, rateLimitFields := normalizeRateLimits(cfg.RateLimits); len(rateLimitFields) > 0 {
		for k, v := range rateLimitFields {
			fields[k] = v
		}
	} else {
		cfg.RateLimits = normalized
	}

	if len(fields) > 0 {
		return domain.UpstreamConfig{}, domain.NewValidationError("上游 MCP 配置校验失败", fields)
	}
	return cfg, nil
}

func normalizeRateLimits(limits domain.UpstreamRateLimits) (domain.UpstreamRateLimits, map[string]string) {
	limits.Timezone = strings.TrimSpace(limits.Timezone)
	if !limits.Enabled {
		return domain.UpstreamRateLimits{Timezone: "UTC"}, nil
	}
	if limits.Timezone == "" {
		limits.Timezone = "UTC"
	}
	fields := make(map[string]string)
	if _, err := time.LoadLocation(limits.Timezone); err != nil {
		fields["rateLimits.timezone"] = "限流时区不是合法 IANA 时区"
	}
	values := []struct {
		field string
		name  string
		value int
	}{
		{"rateLimits.perSecond", "每秒调用上限", limits.PerSecond},
		{"rateLimits.perMinute", "每分钟调用上限", limits.PerMinute},
		{"rateLimits.perHour", "每小时调用上限", limits.PerHour},
		{"rateLimits.perDay", "每日调用上限", limits.PerDay},
		{"rateLimits.perWeek", "每周调用上限", limits.PerWeek},
		{"rateLimits.perMonth", "每月调用上限", limits.PerMonth},
	}
	for _, item := range values {
		if item.value < 0 {
			fields[item.field] = fmt.Sprintf("%s不能为负数", item.name)
		}
	}
	if len(fields) > 0 {
		return domain.UpstreamRateLimits{}, fields
	}
	return limits, nil
}

func normalizeTags(tags []string) ([]string, error) {
	if len(tags) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		t := strings.TrimSpace(tag)
		if t == "" {
			continue
		}
		if n := utf8.RuneCountInString(t); n > maxTagLen {
			return nil, fmt.Errorf("标签长度不能超过 %d 个字符", maxTagLen)
		}
		key := strings.ToLower(t)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, t)
	}
	if len(out) > maxTags {
		return nil, fmt.Errorf("标签数量不能超过 %d 个", maxTags)
	}
	return out, nil
}

// toUpstream 将仓储行映射为对外返回的领域对象。
//
// 凭证以明文随 Config.Credential 返回，供前端编辑时直接回显；此处仅补充当前
// 连接状态与最近失败原因（由连接生命周期状态机维护，Req 5.4）。
func (m *Manager) toUpstream(row *store.UpstreamRow) domain.Upstream {
	up := row.Upstream
	state, lastErr := m.GetState(row.ID)
	up.State = state
	up.LastError = lastErr
	return up
}

// mergeFields 将来自校验错误的字段级说明合并进目标 fields。
//
// 当 err 为携带 Fields 的 *domain.APIError 时逐项合并；否则以通用键记录其错误信息，
// 确保字段级校验错误不丢失（Req 2.2）。
func mergeFields(fields map[string]string, err error) {
	if apiErr, ok := err.(*domain.APIError); ok && len(apiErr.Fields) > 0 {
		for k, v := range apiErr.Fields {
			fields[k] = v
		}
		return
	}
	fields["connParams"] = err.Error()
}
