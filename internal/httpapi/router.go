package httpapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/apikey"
	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
	"github.com/myGithub/mcp-proxy-gateway/internal/syslog"
	"github.com/myGithub/mcp-proxy-gateway/internal/template"
)

// 本文件（任务 19.1）定义管理 REST API 的依赖契约与路由装配入口，覆盖上游 MCP、
// 别名/屏蔽规则、API Key 及其屏蔽规则/ACL/限流配置三大类管理端点，全部挂载在
// 管理员 JWT 中间件之下、统一前缀 /api/admin（设计「路由分面」，Req 17.5）。
//
// 架构定位：httpapi 是组合层（composition layer）。它不持有业务逻辑，仅把请求解析、
// 校验委派、应用服务/仓储调用与统一错误响应接线起来。各依赖以窄接口注入，既贴合本
// 项目其余包「窄接口 + 函数式选项」的风格，也便于在不接真实数据库的前提下做处理器单测。
//
// 依赖说明：
//   - 上游 MCP 的 CRUD/启停/排序/重连由连接管理器（*manager.Manager）提供，手动刷新
//     由同步服务的刷新器（*syncsvc.Refresher）提供。
//   - 别名规则与 MCP 级屏蔽规则尚无独立应用服务，故由本层直接接线对应仓储
//     （*store.AliasRepo、*store.FilterMCPRepo）并复用领域规则引擎做保存前字段校验，
//     与设计「规则引擎为纯函数式」一致。
//   - API Key 生命周期、API Key 级屏蔽规则分别由 *apikey.Manager、*apikey.FilterManager
//     提供；ACL 白名单与限流配置无独立应用服务，由本层接线对应仓储完成。

// UpstreamService 是上游 MCP 管理依赖的应用服务窄接口（Req 2.1、3.1、3.4、5.6、6.4）。
//
// *manager.Manager 满足该接口。相较 domain.MCP_Manager，此处额外要求 List（管理列表
// 需返回各上游配置与连接状态，Req 2.3、2.8），并省略仅供内部使用的 GetState。
type UpstreamService interface {
	// Create 创建上游 MCP 服务。
	Create(ctx context.Context, cfg domain.UpstreamConfig) (domain.Upstream, error)
	// Update 更新上游 MCP 配置并按新配置重建连接。
	Update(ctx context.Context, id string, cfg domain.UpstreamConfig) (domain.Upstream, error)
	// Delete 删除上游 MCP 服务并级联清理工具缓存与规则。
	Delete(ctx context.Context, id string) error
	// List 返回全部上游 MCP 及其当前连接状态。
	List(ctx context.Context) ([]domain.Upstream, error)
	// SetEnabled 启用或停用某个上游 MCP 服务。
	SetEnabled(ctx context.Context, id string, enabled bool) error
	// Reorder 提交新的排序顺序（校验为已注册标识的恰好一次排列）。
	Reorder(ctx context.Context, orderedIDs []string) error
	// Reconnect 由管理员手动发起重连。
	Reconnect(ctx context.Context, id string) error
}

// ToolRefresher 是手动刷新某上游 MCP 工具列表的窄接口（Req 6.4、6.5）。
//
// *syncsvc.Refresher 满足该接口。
type ToolRefresher interface {
	// Refresh 手动刷新指定上游 MCP 的工具列表，成功返回最新列表。
	Refresh(ctx context.Context, upstreamID string) ([]domain.ToolDef, error)
}

// ToolCacheStore 是管理台读取已缓存工具列表的窄接口。
type ToolCacheStore interface {
	Get(ctx context.Context, upstreamID string) (tools []domain.ToolDef, updatedAt time.Time, found bool)
}

// ToolCacheEnsurer 是缓存缺失时按需补拉某上游工具列表的窄接口。
//
// *syncsvc.PeriodicSyncer 满足该接口。
type ToolCacheEnsurer interface {
	// EnsureCached 在指定上游缺失工具缓存时触发一次拉取。
	EnsureCached(ctx context.Context, upstreamID string) (ran bool, err error)
}

// UpstreamTester 是上游配置保存前的临时连通性测试依赖。
type UpstreamTester interface {
	Test(ctx context.Context, cfg domain.UpstreamConfig) (domain.UpstreamTestResult, error)
}

// AggregationToolService 是管理台读取聚合后工具列表的窄接口。
type AggregationToolService interface {
	BuildToolSet(ctx context.Context, apiKeyID string) ([]domain.ToolDef, error)
	BuildToolDetails(ctx context.Context, apiKeyID string) ([]domain.ToolDetail, error)
	InvokeTool(ctx context.Context, apiKeyID, exposedName string, args json.RawMessage) (domain.ToolResult, error)
}

// RuleValidator 是保存前校验别名/屏蔽规则的窄接口（Req 8.9、9.7、9.8、13.4）。
//
// *domain.engine（经 domain.NewRuleEngine 构造）满足该接口。
type RuleValidator interface {
	// ValidateAlias 校验别名规则（正则合法性、模式长度、目标字段非空等）。
	ValidateAlias(r domain.AliasRule) error
	// ValidateFilter 校验屏蔽规则（正则合法性、模式长度 1-200）。
	ValidateFilter(r domain.FilterRule) error
	// ValidateToolPolicy 校验工具策略规则。
	ValidateToolPolicy(r domain.ToolPolicyRule) error
}

// AliasStore 是别名规则管理依赖的仓储窄接口（Req 8.1）。
//
// *store.AliasRepo 满足该接口。
type AliasStore interface {
	Create(ctx context.Context, rule domain.AliasRule) (domain.AliasRule, error)
	Get(ctx context.Context, id string) (domain.AliasRule, error)
	List(ctx context.Context) ([]domain.AliasRule, error)
	ListByUpstream(ctx context.Context, upstreamID string) ([]domain.AliasRule, error)
	Update(ctx context.Context, rule domain.AliasRule) (domain.AliasRule, error)
	Delete(ctx context.Context, id string) error
}

// FilterMCPStore 是 MCP 级屏蔽规则管理依赖的仓储窄接口（Req 9.1、9.2、9.9、9.11）。
//
// *store.FilterMCPRepo 满足该接口。
type FilterMCPStore interface {
	Create(ctx context.Context, row store.FilterMCPRow) (store.FilterMCPRow, error)
	Get(ctx context.Context, id string) (store.FilterMCPRow, error)
	List(ctx context.Context) ([]store.FilterMCPRow, error)
	ListByUpstream(ctx context.Context, upstreamID string) ([]store.FilterMCPRow, error)
	Count(ctx context.Context) (int, error)
	Update(ctx context.Context, row store.FilterMCPRow) (store.FilterMCPRow, error)
	SetEnabled(ctx context.Context, id string, enabled bool) error
	Delete(ctx context.Context, id string) error
}

// ToolPolicyStore 是工具策略规则管理依赖的仓储窄接口。
type ToolPolicyStore interface {
	Create(ctx context.Context, rule domain.ToolPolicyRule) (domain.ToolPolicyRule, error)
	Get(ctx context.Context, id string) (domain.ToolPolicyRule, error)
	List(ctx context.Context) ([]domain.ToolPolicyRule, error)
	Count(ctx context.Context) (int, error)
	Update(ctx context.Context, rule domain.ToolPolicyRule) (domain.ToolPolicyRule, error)
	SetEnabled(ctx context.Context, id string, enabled bool) error
	Delete(ctx context.Context, id string) error
}

// APIKeyService 是 API Key 生命周期管理依赖的应用服务窄接口（Req 12.1）。
//
// *apikey.Manager 满足该接口。
type APIKeyService interface {
	Create(ctx context.Context, in apikey.CreateInput) (apikey.Created, error)
	Get(ctx context.Context, id string) (apikey.Metadata, error)
	List(ctx context.Context) ([]apikey.Metadata, error)
	SetEnabled(ctx context.Context, id string, enabled bool) error
	Delete(ctx context.Context, id string) error
}

// APIKeyFilterService 是 API Key 级屏蔽规则管理依赖的应用服务窄接口（Req 13.1）。
//
// *apikey.FilterManager 满足该接口。
type APIKeyFilterService interface {
	Create(ctx context.Context, in apikey.CreateFilterInput) (apikey.Filter, error)
	List(ctx context.Context, apiKeyID string) ([]apikey.Filter, error)
	SetEnabled(ctx context.Context, id string, enabled bool) error
	Delete(ctx context.Context, id string) error
}

// ACLStore 是 API Key 来源白名单管理依赖的仓储窄接口（Req 13.9）。
//
// *store.ACLRepo 满足该接口。
type ACLStore interface {
	Create(ctx context.Context, entry store.ACLEntry) (store.ACLEntry, error)
	ListByAPIKey(ctx context.Context, apiKeyID string) ([]store.ACLEntry, error)
	Delete(ctx context.Context, id string) error
}

// RateLimitStore 是 API Key 限流配置读写依赖的仓储窄接口（Req 21）。
//
// 限流配置（速率上限与窗口）属 api_key 表元数据字段，无独立应用服务，故经此接口
// 直接读取后回写。*store.APIKeyRepo 满足该接口。
type RateLimitStore interface {
	Get(ctx context.Context, id string) (store.APIKey, error)
	Update(ctx context.Context, key store.APIKey) (store.APIKey, error)
}

// TemplateService 是模板市场（Template_Market）只读查询依赖的应用服务窄接口
// （Req 14.1-14.7、14.13）。
//
// *template.Market 满足该接口。各查询方法返回深拷贝且无匹配时返回非 nil 空列表，
// Get/Prefill 在模板不存在时返回 NOT_FOUND 错误。
type TemplateService interface {
	// List 返回模板市场中的全部模板。
	List() []template.Template
	// ListByCategory 返回指定分类下的模板（无匹配返回空列表）。
	ListByCategory(c template.Category) []template.Template
	// Search 按关键字检索名称或简介命中的模板（关键字为空视为不限定）。
	Search(keyword string) []template.Template
	// ListByCategories 返回按分类组织的模板视图，用于分类导航。
	ListByCategories() []template.CategoryView
	// Get 按标识返回模板详情；不存在返回 NOT_FOUND。
	Get(id string) (template.Template, error)
	// Prefill 返回基于模板的表单预填充数据；模板不存在返回 NOT_FOUND。
	Prefill(templateID string) (template.PrefillForm, error)
}

// AuthService 是管理员认证依赖的应用服务窄接口（Req 1）。
//
// *auth.Service 满足该接口。注册/登录为公开端点（无需 JWT），改密为受保护端点。
type AuthService interface {
	// IsInitialized 报告是否已存在管理员账号（Req 1.1）。
	IsInitialized() bool
	// Register 注册唯一管理员账号并完成首次初始化（Req 1.2、1.3、1.9）。
	Register(username, password string) error
	// Login 校验凭证，匹配则签发会话令牌及其过期时刻（Req 1.4、1.5）。
	Login(username, password string) (token string, expiresAt time.Time, err error)
	// ChangePassword 校验当前密码与新密码后更新密码哈希（Req 1.8、1.10）。
	ChangePassword(currentPassword, newPassword string) error
}

// SettingsService 是系统设置读写依赖的配置管理窄接口（Req 18.4、7.3）。
//
// *config.Manager 满足该接口。读取返回 YAML 常规配置快照，写入前由配置校验与
// cron 校验把关（非法返回 VALIDATION）。
type SettingsService interface {
	// Config 返回当前 YAML 常规配置的快照副本。
	Config() config.YAMLConfig
	// Save 校验并将给定 YAML 配置回写持久化，成功后更新内存快照（Req 18.4）。
	Save(cfg config.YAMLConfig) error
}

// SettingsRuntimeApplier 将已保存的配置应用到当前进程内的运行时组件。
//
// 例如对外 MCP 模式、小智接入开关这类配置需要在不重启进程的情况下即时影响新连接
// 或后台连接生命周期；具体实现由 app 装配层注入，本层只负责在设置落盘成功后调用。
type SettingsRuntimeApplier interface {
	ApplySettings(cfg config.YAMLConfig) error
	RuntimeServerConfig() config.ServerConfig
	RequestRestart()
}

// BackupService 是配置备份导入导出依赖的窄接口。
//
// *backup.Service 满足该接口；预览只需解析文件内容，由 httpapi 组合层直接复用
// backup.ParseAndValidate，不把预览职责沉入应用服务。
type BackupService interface {
	Export(ctx context.Context) ([]byte, error)
	Import(ctx context.Context, data []byte) error
}

// CronValidator 是同步 cron 表达式保存前校验的窄接口（Req 7.3、7.4）。
//
// syncsvc.ValidateCron 满足该函数签名（以函数值注入，避免本包依赖 sync 包）。
type CronValidator func(expr string) error

// StatsService 是统计查询依赖的应用服务窄接口（Req 16.2、16.3、16.4）。
//
// *stats.QueryService 满足该接口。时间区间非法（开始晚于结束）由下层返回 VALIDATION。
type StatsService interface {
	// CountByUpstream 统计闭区间内各上游 MCP 的调用条数。
	CountByUpstream(ctx context.Context, start, end time.Time) ([]store.DimensionCount, error)
	// CountByAPIKey 统计闭区间内各 API Key 的调用条数。
	CountByAPIKey(ctx context.Context, start, end time.Time) ([]store.DimensionCount, error)
	// TopTools 返回闭区间内按调用次数降序的工具排行，至多 limit 条。
	TopTools(ctx context.Context, start, end time.Time, limit int) ([]store.ToolRank, error)
	// Summary 返回闭区间内调用概览。
	Summary(ctx context.Context, start, end time.Time) (store.StatsSummary, error)
	// Daily 返回闭区间内按指定时区（IANA 名，空串回退 UTC）自然日聚合的调用趋势。
	Daily(ctx context.Context, start, end time.Time, tz string) ([]store.DailyCount, error)
	// TopToolErrors 返回闭区间内按失败次数降序的工具错误排行。
	TopToolErrors(ctx context.Context, start, end time.Time, limit int) ([]store.ToolErrorRank, error)
	// APIKeyUsageProfile 返回单个 API Key 在闭区间内的使用画像。
	APIKeyUsageProfile(ctx context.Context, apiKeyID string, start, end time.Time, limit int) (store.APIKeyUsageProfile, error)
	// Health 返回最近窗口内的调用健康概览。
	Health(ctx context.Context, window string, now time.Time) (store.CallHealth, error)
	// ListRecords 按最新时间倒序分页返回调用记录。
	ListRecords(ctx context.Context, query store.CallRecordQuery) ([]store.CallRecordView, error)
	// GetRecord 按 ID 返回单条调用记录详情。
	GetRecord(ctx context.Context, id int64) (store.CallRecordView, error)
	// ClearRecords 清空调用记录，返回删除条数。
	ClearRecords(ctx context.Context) (int64, error)
}

// AuditService 是审计日志分页查询依赖的应用服务窄接口（Req 22.4）。
//
// *audit.Service 满足该接口。返回按发生时间倒序的分页结果。
type AuditService interface {
	// List 按发生时间倒序分页返回审计记录及总数（入参越界由下层收敛）。
	List(ctx context.Context, page, pageSize int, query audit.Query) (audit.PageResult, error)
}

// AuditRecorder 是审计事件异步写入依赖的窄接口（Req 22.1、22.2、22.3）。
//
// *audit.Recorder 满足该接口。各 Record* 方法在调用线程完成明细组装与时间戳标注后，
// 以非阻塞方式入队，由后台 worker 异步落库；写入失败静默丢弃、不向调用方报错（审计旁路）。
// handler 在主操作成功后调用对应方法记录审计，无需关心其错误返回（恒为 nil）。
type AuditRecorder interface {
	// RecordLogin 异步记录一次管理员登录事件及其结果与时间戳（Req 22.1）。
	RecordLogin(ctx context.Context, username string, success bool) error
	// RecordCreate 异步记录一次资源创建事件（Req 22.2）。
	RecordCreate(ctx context.Context, kind audit.ResourceKind, target string) error
	// RecordUpdate 异步记录一次资源更新事件（Req 22.2）。
	// 语义上覆盖更新及近似的写操作（启停、重排序、设置保存等），通过 detail.resource 区分。
	RecordUpdate(ctx context.Context, kind audit.ResourceKind, target string) error
	// RecordDelete 异步记录一次资源删除事件（Req 22.2）。
	RecordDelete(ctx context.Context, kind audit.ResourceKind, target string) error
	// RecordAccessDenied 异步记录一次因鉴权失败被拒绝的访问尝试（Req 22.3）。
	RecordAccessDenied(ctx context.Context, target, reason string) error
}

// SecurityService 是安全中心依赖的应用服务窄接口。
type SecurityService interface {
	Summary(ctx context.Context) (store.SecuritySummary, error)
	ListEvents(ctx context.Context, query store.SecurityEventQuery) ([]store.SecurityEvent, error)
	ListBlocks(ctx context.Context, query store.SecurityBlockQuery) ([]store.SecurityBlock, error)
	ReleaseBlock(ctx context.Context, id string) (store.SecurityBlock, error)
}

// SystemLogService 是进程运行日志查询依赖的窄接口。
type SystemLogService interface {
	List(afterID int64, level string, limit int) []syslog.Entry
	Export(level string) []syslog.Entry
	Clear() int
}

// Router 汇集管理 REST API 的全部依赖，并提供路由装配方法。
//
// 各依赖均为窄接口，由装配层（任务 27.2）注入具体实现。允许部分依赖为 nil：对应
// 端点在缺失依赖时将以服务未就绪的方式拒绝（见各处理器的防御性判断），便于渐进接线。
type Router struct {
	// upstream 为上游 MCP 管理应用服务。
	upstream UpstreamService
	// refresher 为上游工具列表手动刷新器。
	refresher ToolRefresher
	// toolCache 为管理台读取上游已缓存工具列表的仓储。
	toolCache ToolCacheStore
	// cacheEnsurer 为工具缓存缺失时的按需补拉器。
	cacheEnsurer ToolCacheEnsurer
	// upstreamTester 为保存前临时测试上游连接的服务。
	upstreamTester UpstreamTester
	// aggregation 为管理台读取聚合后真实工具列表的服务。
	aggregation AggregationToolService
	// ruleValidator 为别名/屏蔽规则的保存前校验器（领域规则引擎）。
	ruleValidator RuleValidator
	// aliasStore 为别名规则仓储。
	aliasStore AliasStore
	// filterMCPStore 为 MCP 级屏蔽规则仓储。
	filterMCPStore FilterMCPStore
	// toolPolicyStore 为工具策略规则仓储。
	toolPolicyStore ToolPolicyStore
	// apiKeys 为 API Key 生命周期管理应用服务。
	apiKeys APIKeyService
	// apiKeyFilters 为 API Key 级屏蔽规则管理应用服务。
	apiKeyFilters APIKeyFilterService
	// aclStore 为 API Key 来源白名单仓储。
	aclStore ACLStore
	// rateLimitStore 为 API Key 限流配置仓储。
	rateLimitStore RateLimitStore
	// auth 为管理员认证应用服务（注册/登录/改密）。
	auth AuthService
	// settings 为系统设置读写配置管理器。
	settings SettingsService
	// settingsRuntime 在配置保存后同步更新当前进程内的运行时组件；可为空。
	settingsRuntime SettingsRuntimeApplier
	// backup 为配置备份导入导出服务。
	backup BackupService
	// validateCron 为同步 cron 表达式保存前校验函数；为 nil 时跳过 cron 专项校验。
	validateCron CronValidator
	// stats 为统计查询应用服务。
	stats StatsService
	// audit 为审计日志分页查询应用服务。
	audit AuditService
	// auditRecorder 为审计事件异步写入器（登录/增删改/访问被拒）；为 nil 时跳过审计写入。
	auditRecorder AuditRecorder
	// security 为安全中心服务。
	security SecurityService
	// systemLogs 为进程运行日志缓冲。
	systemLogs SystemLogService
	// templates 为模板市场只读查询应用服务。
	templates TemplateService
}

// Deps 聚合构造 Router 所需的全部依赖，便于装配层一次性注入。
type Deps struct {
	Upstream        UpstreamService
	Refresher       ToolRefresher
	ToolCache       ToolCacheStore
	CacheEnsurer    ToolCacheEnsurer
	UpstreamTester  UpstreamTester
	Aggregation     AggregationToolService
	RuleValidator   RuleValidator
	AliasStore      AliasStore
	FilterMCPStore  FilterMCPStore
	ToolPolicyStore ToolPolicyStore
	APIKeys         APIKeyService
	APIKeyFilters   APIKeyFilterService
	ACLStore        ACLStore
	RateLimitStore  RateLimitStore
	Auth            AuthService
	Settings        SettingsService
	SettingsRuntime SettingsRuntimeApplier
	Backup          BackupService
	ValidateCron    CronValidator
	Stats           StatsService
	Audit           AuditService
	AuditRecorder   AuditRecorder
	Security        SecurityService
	SystemLogs      SystemLogService
	Templates       TemplateService
}

// NewRouter 构造管理 REST API 路由器。
func NewRouter(d Deps) *Router {
	return &Router{
		upstream:        d.Upstream,
		refresher:       d.Refresher,
		toolCache:       d.ToolCache,
		cacheEnsurer:    d.CacheEnsurer,
		upstreamTester:  d.UpstreamTester,
		aggregation:     d.Aggregation,
		ruleValidator:   d.RuleValidator,
		aliasStore:      d.AliasStore,
		filterMCPStore:  d.FilterMCPStore,
		toolPolicyStore: d.ToolPolicyStore,
		apiKeys:         d.APIKeys,
		apiKeyFilters:   d.APIKeyFilters,
		aclStore:        d.ACLStore,
		rateLimitStore:  d.RateLimitStore,
		auth:            d.Auth,
		settings:        d.Settings,
		settingsRuntime: d.SettingsRuntime,
		backup:          d.Backup,
		validateCron:    d.ValidateCron,
		stats:           d.Stats,
		audit:           d.Audit,
		auditRecorder:   d.AuditRecorder,
		security:        d.Security,
		systemLogs:      d.SystemLogs,
		templates:       d.Templates,
	}
}

// Register 在给定路由器上注册管理 REST API 端点（Req 17.5）。
//
// 路由分两组：
//   - 公开认证组 /api/auth/*：管理员注册（首次初始化）与登录，无需 JWT（Req 1.1、1.4），
//     以便未持令牌的浏览器完成初始化与登录。该组始终注册，不受 adminAuth 是否为 nil 影响。
//   - 受保护管理组 /api/admin/*：上游 MCP、规则、API Key、系统设置、统计、审计与改密，
//     全部置于管理员 JWT 中间件之下。
//
// adminAuth 为管理员 JWT 鉴权中间件（通常为 auth.RequireAdmin 的返回值），以参数注入
// 以避免本包与具体鉴权实现耦合。adminAuth 为 nil 时不注册任何受保护端点，避免误将管理
// 端点暴露为无保护；但公开认证组仍会注册。
func (r *Router) Register(router gin.IRouter, adminAuth gin.HandlerFunc) {
	// 公开认证端点（注册/登录），无需 JWT（Req 1.1、1.4）。
	r.registerPublicAuthRoutes(router)

	if adminAuth == nil {
		return
	}

	admin := router.Group("/api/admin", adminAuth)

	r.registerUpstreamRoutes(admin)
	r.registerRuleRoutes(admin)
	r.registerAPIKeyRoutes(admin)
	r.registerSettingsRoutes(admin)
	r.registerBackupRoutes(admin)
	r.registerToolRoutes(admin)
	r.registerStatsRoutes(admin)
	r.registerAuditRoutes(admin)
	r.registerSecurityRoutes(admin)
	r.registerSystemLogRoutes(admin)
	r.registerTemplateRoutes(admin)
	r.registerProtectedAuthRoutes(admin)
	r.registerDiagnosticsRoutes(admin)
}
