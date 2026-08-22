package aggregation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// StatRecorder 是聚合服务异步记录调用统计的窄接口（Req 16.1、16.8）。
//
// 仅声明「以非阻塞方式提交一条调用记录」这一最小能力，聚合层据此在工具调用完成后采集
// 维度数据（稳定标识 (上游, 原始名)、对外名、所用 API Key、毫秒时间戳、耗时、成败），
// 而不耦合具体的统计实现与存储。*stats.Recorder 的 RecordAsync 天然满足本接口，由装配层
// （任务 27.2）经 SetRecorder 注入；未注入时聚合调用路径不做任何统计动作。
type StatRecorder interface {
	// RecordAsync 非阻塞地提交一条调用统计记录，绝不阻塞调用方主流程（Req 16.8、16.9）。
	RecordAsync(ctx context.Context, rec store.CallStatRecord)
}

// modeContextKey 用于把 MCP 模式（full/smart）注入 context，供调用记录采集。
type modeContextKey struct{}

// ModeFromContext 从 context 中提取 MCP 模式；未设置时返回 "full"。
func ModeFromContext(ctx context.Context) string {
	if m, ok := ctx.Value(modeContextKey{}).(string); ok && m != "" {
		return m
	}
	return "full"
}

// ContextWithMode 返回携带 MCP 模式的 context。
func ContextWithMode(ctx context.Context, mode string) context.Context {
	return context.WithValue(ctx, modeContextKey{}, mode)
}

// sourceContextKey 用于把调用来源（api/xiaozhi）注入 context，供调用记录采集。
//
// 与 mode 机制同构：在调用入口（mcpapi 处理器、小智接入装配）注入，在采集点 recordCall
// 读取。未设置时回退 "api"。采用 context 注入而非给 InvokeTool 加参数，避免改动
// domain.Aggregation_Service 契约与全部 mock。
type sourceContextKey struct{}

// SourceFromContext 从 context 中提取调用来源；未设置时返回 "api"。
func SourceFromContext(ctx context.Context) string {
	if s, ok := ctx.Value(sourceContextKey{}).(string); ok && s != "" {
		return s
	}
	return "api"
}

// ContextWithSource 返回携带调用来源的 context。
func ContextWithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceContextKey{}, source)
}

// UpstreamLister 是聚合服务读取「启用上游列表」所需的窄接口。
//
// 仅声明聚合所需的最小能力（按 sort_order 升序列出上游），不直接耦合具体的
// *store.UpstreamRepo，便于在属性测试中以内存实现替换（mock），也便于未来更换存储。
// 实现方（如 store.UpstreamRepo 的适配器）负责返回所有上游；聚合服务自行筛选启用项。
type UpstreamLister interface {
	// ListUpstreams 返回全部上游 MCP（已按 sort_order 升序排列）。
	// 聚合服务只会纳入其中 Enabled == true 的上游（Req 3.3、10.1）。
	ListUpstreams(ctx context.Context) ([]domain.Upstream, error)
}

// AliasLister 是按上游读取别名规则的窄接口（Req 8）。
type AliasLister interface {
	// ListAliasesByUpstream 返回某上游 MCP 的全部别名规则（建议按 sort_order 升序）。
	ListAliasesByUpstream(ctx context.Context, upstreamID string) ([]domain.AliasRule, error)
}

// MCPFilterLister 是按上游读取 MCP 级屏蔽规则的窄接口（Req 9）。
type MCPFilterLister interface {
	// ListMCPFiltersByUpstream 返回某上游 MCP 的全部屏蔽规则。
	ListMCPFiltersByUpstream(ctx context.Context, upstreamID string) ([]domain.FilterRule, error)
}

// APIKeyFilterLister 是按 API Key 读取屏蔽规则的窄接口（Req 13）。
type APIKeyFilterLister interface {
	// ListAPIKeyFiltersByAPIKey 返回某 API Key 的全部屏蔽规则。
	// 当 apiKeyID 为空（无 API Key 级过滤）时，编排层不会调用本方法。
	ListAPIKeyFiltersByAPIKey(ctx context.Context, apiKeyID string) ([]domain.FilterRule, error)
}

// ToolPolicyLister 读取按对外工具名匹配的工具策略规则。
type ToolPolicyLister interface {
	ListToolPolicies(ctx context.Context) ([]domain.ToolPolicyRule, error)
}

// Service 是 domain.Aggregation_Service 的实现，编排聚合管线并（后续任务）负责调用路由。
//
// 依赖通过构造函数注入，全部为窄接口与领域接口，不直接耦合具体存储类型：
//   - cache：工具缓存（domain.Tool_Cache），聚合永不实时拉取上游（Req 6.2）。
//   - engine：规则引擎（domain.Rule_Engine），提供屏蔽/别名/匹配的纯函数能力。
//   - upstreams/aliases/mcpFilters/apiKeyFilters：数据访问窄接口。
//
// 本类型的 BuildToolSet 实现工具聚合；InvokeTool 的可见性校验与别名反向映射路由
// 经可选注入的 UpstreamInvoker（窄接口）转发，真实上游会话调用与超时控制由
// upstream_invoker.go 实现，生产装配在 app/build.go 注入。
type Service struct {
	cache         domain.Tool_Cache
	engine        domain.Rule_Engine
	upstreams     UpstreamLister
	aliases       AliasLister
	mcpFilters    MCPFilterLister
	apiKeyFilters APIKeyFilterLister
	toolPolicies  ToolPolicyLister
	// upstreamConfigs 保存最近一次构建管线读取到的上游配置，供路由选择读取限流配置。
	upstreamConfigs   map[string]domain.UpstreamConfig
	upstreamConfigsMu sync.RWMutex
	// routingStrategy 为同名工具多来源时的调用选择策略。
	routingStrategy        domain.ToolRoutingStrategy
	routingMu              sync.RWMutex
	roundRobin             map[string]uint64
	roundRobinMu           sync.Mutex
	resultCache            map[string]cachedToolResult
	resultCacheMu          sync.Mutex
	resultCacheHits        uint64
	resultCacheMisses      uint64
	resultCacheStores      uint64
	resultCacheEvictions   uint64
	resultCacheExpired     uint64
	resultCacheLastCleared time.Time
	quota                  *QuotaManager
	sourceFailures         map[string]sourceFailureState
	sourceFailureMu        sync.Mutex
	// sourceFailureThreshold/sourceFailureCooldown 是轻量故障降级的固定默认值。
	// 不暴露管理配置，避免为了小概率调参增加用户理解成本。
	sourceFailureThreshold int
	sourceFailureCooldown  time.Duration
	now                    func() time.Time
	// invoker 为上游调用转发器（窄接口），可选注入（生产装配见 app/build.go）。
	// 为 nil 时（仅未接线装配或单元测试）InvokeTool 在通过可见性校验后返回防御性
	// 占位错误；可见性校验逻辑始终完整执行（Req 10.4、11.7）。
	invoker UpstreamInvoker
	// recorder 为调用统计异步记录器（窄接口），可选注入（Req 16.1、16.8）。
	// 为 nil 时聚合调用路径不采集统计；非 nil 时在工具调用完成后以非阻塞方式提交一条
	// 调用记录，绝不阻塞主流程（Req 16.8、16.9）。
	recorder StatRecorder
	// log 为结构化日志器，用于记录调用链关键节点（入口/可见性拒绝/失败源头/统计提交）。
	// 为 nil 时回退到 slog.Default()。
	log *slog.Logger
}

const (
	defaultSourceFailureThreshold = 3
	defaultSourceFailureCooldown  = 30 * time.Second
	maxResultCacheEntries         = 512
	maxCachedToolResultBytes      = 1 << 20
)

type sourceFailureState struct {
	Consecutive    int
	SuspendedUntil time.Time
}

type cachedToolResult struct {
	result      domain.ToolResult
	expiresAt   time.Time
	apiKeyID    string
	exposedName string
}

// 编译期断言：Service 必须满足 domain.Aggregation_Service 接口契约。
var _ domain.Aggregation_Service = (*Service)(nil)

// NewService 构造聚合服务，按依赖倒置注入缓存、规则引擎与各数据访问窄接口。
func NewService(
	cache domain.Tool_Cache,
	engine domain.Rule_Engine,
	upstreams UpstreamLister,
	aliases AliasLister,
	mcpFilters MCPFilterLister,
	apiKeyFilters APIKeyFilterLister,
	toolPolicies ToolPolicyLister,
) *Service {
	if engine == nil {
		engine = domain.NewRuleEngine()
	}
	return &Service{
		cache:                  cache,
		engine:                 engine,
		upstreams:              upstreams,
		aliases:                aliases,
		mcpFilters:             mcpFilters,
		apiKeyFilters:          apiKeyFilters,
		toolPolicies:           toolPolicies,
		upstreamConfigs:        make(map[string]domain.UpstreamConfig),
		routingStrategy:        domain.ToolRoutingSmartBalance,
		roundRobin:             make(map[string]uint64),
		resultCache:            make(map[string]cachedToolResult),
		sourceFailures:         make(map[string]sourceFailureState),
		sourceFailureThreshold: defaultSourceFailureThreshold,
		sourceFailureCooldown:  defaultSourceFailureCooldown,
		now:                    time.Now,
		log:                    slog.Default(),
	}
}

// SetInvoker 注入上游调用转发器（窄接口），供 InvokeTool 在反向映射后转发调用。
//
// 采用可选 setter 而非修改 NewService 签名，以避免破坏既有调用方：未注入时（invoker
// 为 nil），InvokeTool 仍完整执行可见性校验（Req 10.4、11.7），仅在校验通过后因未
// 接线上游会话而返回防御性占位错误。真实转发器由 upstream_invoker.go 实现并通过本方法注入。
//
// 返回 *Service 以支持链式调用。
func (s *Service) SetInvoker(invoker UpstreamInvoker) *Service {
	s.invoker = invoker
	return s
}

// SetRecorder 注入调用统计异步记录器（窄接口），供 InvokeTool 在调用完成后采集统计。
//
// 与 SetInvoker 一致采用可选 setter，避免破坏既有调用方：未注入时（recorder 为 nil），
// InvokeTool 不做任何统计动作；注入后，每次工具调用完成都会以非阻塞方式提交一条调用记录
// （稳定标识 (上游, 原始名)、对外名、API Key、毫秒时间戳、耗时、成败），绝不阻塞主流程，
// 且统计写入失败不影响调用结果返回（Req 16.1、16.8、16.9）。真实记录器由任务 27.2 注入。
//
// 返回 *Service 以支持链式调用。
func (s *Service) SetRecorder(recorder StatRecorder) *Service {
	s.recorder = recorder
	return s
}

// SetLogger 注入结构化日志器，供调用链关键节点记录（入口/可见性拒绝/失败/统计提交）。
// 为空时回退到 slog.Default()。返回 *Service 以支持链式调用。
func (s *Service) SetLogger(l *slog.Logger) *Service {
	if l != nil {
		s.log = l
	}
	return s
}

// SetRoutingStrategy 更新同名工具多来源时的内部调用选择策略。
func (s *Service) SetRoutingStrategy(strategy domain.ToolRoutingStrategy) *Service {
	s.routingMu.Lock()
	s.routingStrategy = domain.NormalizeToolRoutingStrategy(strategy)
	s.routingMu.Unlock()
	return s
}

// SetQuotaManager 注入上游调用额度管理器。
func (s *Service) SetQuotaManager(q *QuotaManager) *Service {
	s.quota = q
	return s
}

func (s *Service) ToolResultCacheStats() domain.ToolResultCacheStats {
	if s == nil {
		return domain.ToolResultCacheStats{MaxEntries: maxResultCacheEntries}
	}
	s.resultCacheMu.Lock()
	defer s.resultCacheMu.Unlock()
	s.pruneExpiredCachedResultsLocked()
	stats := domain.ToolResultCacheStats{
		Entries:    len(s.resultCache),
		MaxEntries: maxResultCacheEntries,
		Hits:       s.resultCacheHits,
		Misses:     s.resultCacheMisses,
		Stores:     s.resultCacheStores,
		Evictions:  s.resultCacheEvictions,
		Expired:    s.resultCacheExpired,
	}
	if !s.resultCacheLastCleared.IsZero() {
		t := s.resultCacheLastCleared
		stats.LastClearedAt = &t
	}
	return stats
}

func (s *Service) ClearToolResultCache(filter domain.ToolResultCacheClearFilter) domain.ToolResultCacheClearResult {
	if s == nil {
		return domain.ToolResultCacheClearResult{}
	}
	s.resultCacheMu.Lock()
	defer s.resultCacheMu.Unlock()

	deleted := 0
	if filter.ExposedName == "" && filter.APIKeyID == "" {
		deleted = len(s.resultCache)
		clear(s.resultCache)
	} else {
		for key, entry := range s.resultCache {
			if filter.ExposedName != "" && entry.exposedName != filter.ExposedName {
				continue
			}
			if filter.APIKeyID != "" && entry.apiKeyID != filter.APIKeyID {
				continue
			}
			delete(s.resultCache, key)
			deleted++
		}
	}
	if deleted > 0 {
		s.resultCacheLastCleared = s.currentTime()
	}
	return domain.ToolResultCacheClearResult{
		Deleted:   deleted,
		Remaining: len(s.resultCache),
	}
}

func (s *Service) currentRoutingStrategy() domain.ToolRoutingStrategy {
	s.routingMu.RLock()
	defer s.routingMu.RUnlock()
	if domain.ValidToolRoutingStrategy(s.routingStrategy) {
		return domain.NormalizeToolRoutingStrategy(s.routingStrategy)
	}
	return domain.ToolRoutingSmartBalance
}

// logger 返回已注入的日志器（保证非 nil）。
func (s *Service) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}

// BuildToolSet 构建某 API Key 视角的可见聚合工具集合（执行完整六阶段管线，Req 10、13）。
//
//   - apiKeyID 为空：表示无 API Key 级过滤，返回全局可见聚合集合（Req 10.1/10.2/10.7）。
//   - apiKeyID 非空：在全局集合之上再应用该 Key 的启用屏蔽规则（Req 13.7）。
//
// 当无启用上游或全部工具被屏蔽时，返回非 nil 的空切片而非错误（Req 10.7）。
func (s *Service) BuildToolSet(ctx context.Context, apiKeyID string) ([]domain.ToolDef, error) {
	tools, _, err := s.buildToolSetWithReverseMap(ctx, apiKeyID)
	return tools, err
}

// BuildToolDetails 构建管理台可读的聚合工具详情，包含每个工具的来源上游列表。
func (s *Service) BuildToolDetails(ctx context.Context, apiKeyID string) ([]domain.ToolDetail, error) {
	tools, reverse, err := s.buildToolSetWithReverseMap(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}
	policies, err := s.listToolPolicies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ToolDetail, 0, len(tools))
	for _, tool := range tools {
		entry := reverse[tool.Name]
		sources := make([]domain.ToolSourceView, 0, len(entry.Candidates))
		routableSourceCount := 0
		for _, c := range entry.Candidates {
			if c.Compatible {
				routableSourceCount++
			}
		}
		canDegrade := routableSourceCount > 1
		for _, c := range entry.Candidates {
			cfg, _ := s.upstreamConfig(c.UpstreamID)
			degraded, reason, until := s.sourceDegradation(c)
			sourceDegraded := c.Compatible && canDegrade && degraded
			routingAvailable := c.Compatible
			if sourceDegraded {
				routingAvailable = false
			}
			var degradationUntil *time.Time
			if sourceDegraded && !until.IsZero() {
				degradationUntil = &until
			}
			degradationReason := ""
			if sourceDegraded {
				degradationReason = reason
			}
			sources = append(sources, domain.ToolSourceView{
				UpstreamID:          c.UpstreamID,
				UpstreamName:        c.UpstreamName,
				OriginalName:        c.OriginalName,
				Description:         c.Tool.Description,
				InputSchema:         c.Tool.InputSchema,
				Compatible:          c.Compatible,
				SchemaConflict:      c.SchemaConflict,
				RoutingAvailable:    routingAvailable,
				TemporarilyDegraded: sourceDegraded,
				DegradationReason:   degradationReason,
				DegradationUntil:    degradationUntil,
				RateLimits:          cfg.RateLimits,
			})
		}
		var policyView *domain.ToolPolicyView
		if policy, ok := s.matchToolPolicy(policies, tool.Name); ok {
			policyView = toolPolicyView(policy)
		}
		out = append(out, domain.ToolDetail{Tool: tool, Sources: sources, Policy: policyView})
	}
	return out, nil
}

// InvokeTool 调用聚合工具：可见性校验 → 别名反向映射 → 路由到上游 → 原样返回结果。
//
// 执行步骤（对应设计文档「工具调用与别名反向映射」伪代码）：
//  1. 以 apiKeyID 视角构建可见聚合工具集合及其反向映射（已过完整管线，含 API Key 级
//     过滤），保证可见性与 BuildToolSet 一致（Req 11）。
//  2. 可见性校验：若 exposedName 不在该视角的可见集合内（含被过滤而不可见的情形），
//     则不向任何上游转发，直接返回 TOOL_NOT_FOUND（Req 10.4、11.7）。
//  3. 命中则用反向映射唯一还原出 (UpstreamID, OriginalName)（Req 10.6）。
//  4. 经窄接口 UpstreamInvoker 以原始参数 args 透传转发，并将上游结果原样返回（Req 10.3）。
//  5. 调用完成后经窄接口 StatRecorder 以非阻塞方式异步提交一条调用统计记录（Req 16.1、
//     16.8、16.9）；statistics 写入绝不阻塞主流程、其失败不影响调用结果返回。
//
// 注意：真实的上游会话调用、连接不可用判断与调用超时控制（Req 10.5、10.8）由
// UpstreamInvoker 的实现方提供（生产装配见 app/build.go 的 SetInvoker 注入）。当 invoker
// 未注入（nil）时——仅见于未接线装配或单元测试——本方法在通过可见性校验后返回防御性
// 占位错误 domain.ErrNotImplemented，可见性校验逻辑始终完整执行。
func (s *Service) InvokeTool(ctx context.Context, apiKeyID, exposedName string, args json.RawMessage) (domain.ToolResult, error) {
	log := s.logger()
	mode := ModeFromContext(ctx)
	log.Debug("工具调用进入聚合层", "exposedName", exposedName, "apiKeyID", apiKeyID, "mode", mode)

	// 步骤 1：构建该 API Key 视角的可见集合与反向映射。
	_, reverse, err := s.buildToolSetWithReverseMap(ctx, apiKeyID)
	if err != nil {
		log.Warn("构建可见工具集合失败，调用未转发", "exposedName", exposedName, "apiKeyID", apiKeyID, "error", err)
		return domain.ToolResult{}, err
	}

	// 步骤 2：可见性校验。不在可见集合内则拒绝且不转发（Req 10.4、11.7）。
	entry, ok := reverse[exposedName]
	if !ok {
		// 可见性拒绝：不转发、不记统计。这是定位「调用未进统计」的关键节点，用 Info 记录。
		log.Info("工具不在当前可见集合，拒绝调用（不转发、不记统计）",
			"exposedName", exposedName, "apiKeyID", apiKeyID, "mode", mode)
		return domain.ToolResult{}, domain.NewError(domain.CodeToolNotFound, "工具不存在于当前可见聚合工具集合中")
	}

	policy, hasPolicy, err := s.resolveToolPolicy(ctx, exposedName)
	if err != nil {
		log.Warn("读取工具策略失败，调用未转发", "exposedName", exposedName, "apiKeyID", apiKeyID, "error", err)
		return domain.ToolResult{}, err
	}
	if hasPolicy && policy.CacheEnabled && policy.CacheTTLSeconds > 0 {
		if cached, ok := s.getCachedToolResult(apiKeyID, exposedName, args); ok {
			log.Debug("工具调用命中策略缓存", "exposedName", exposedName, "apiKeyID", apiKeyID)
			return cached, nil
		}
	}

	// 步骤 3-4：命中——反向映射已还原 (UpstreamID, OriginalName)，经窄接口透传转发。
	// invoker 未注入时（仅未接线装配或单元测试）返回防御性占位错误；可见性校验在此之前已完整执行。
	if s.invoker == nil {
		log.Warn("上游调用转发器未注入，调用未执行", "exposedName", exposedName)
		return domain.ToolResult{}, domain.ErrNotImplemented
	}

	candidate, err := s.selectCandidate(ctx, entry, policy)
	if err != nil {
		log.Warn("工具来源选择失败，调用未转发", "exposedName", exposedName, "apiKeyID", apiKeyID, "error", err)
		return domain.ToolResult{}, err
	}

	// 调用前记录起点，用于计算响应耗时（毫秒，Req 16.1）。统计写入绝不影响调用结果。
	log.Debug("工具调用转发上游", "exposedName", exposedName, "upstreamID", candidate.UpstreamID, "originalName", candidate.OriginalName, "apiKeyID", apiKeyID, "mode", mode)
	startedAt := time.Now()
	var result domain.ToolResult
	var callErr error
	for {
		if candidate.deferQuotaReservation {
			preDispatch, ok := s.invoker.(PreDispatchInvoker)
			if !ok {
				return domain.ToolResult{}, domain.NewError(domain.CodeUpstreamUnavailable, "上游 MCP 恢复协调器未就绪")
			}
			result, callErr = preDispatch.CallUpstreamWithPreDispatch(
				ctx,
				candidate.UpstreamID,
				candidate.OriginalName,
				args,
				func(reserveCtx context.Context) error {
					return s.reserveCandidateQuota(reserveCtx, candidate)
				},
			)
		} else {
			result, callErr = s.invoker.CallUpstream(ctx, candidate.UpstreamID, candidate.OriginalName, args)
		}

		if pending, ok := errors.AsType[*PreDispatchError](callErr); ok {
			if len(candidate.recoveryFallbacks) > 0 {
				next := candidate.recoveryFallbacks[0]
				next.recoveryFallbacks = append([]ToolCandidate(nil), candidate.recoveryFallbacks[1:]...)
				candidate = next
				continue
			}
			// 未发送工具请求的恢复/额度失败：不记录来源故障或调用统计。
			return domain.ToolResult{}, pending.Err
		}
		break
	}
	s.recordSourceResult(candidate, callErr)

	// 步骤 5：异步采集调用统计（Req 16.1、16.8、16.9）。
	// 以非阻塞方式提交，主流程附加耗时极小且永不阻塞；记录器未注入时跳过。
	s.recordCall(ctx, apiKeyID, exposedName, candidate, startedAt, args, result, callErr)

	if callErr != nil {
		log.Warn("工具调用上游失败", "exposedName", exposedName, "upstreamID", candidate.UpstreamID, "latencyMS", int(time.Since(startedAt).Milliseconds()), "error", callErr)
	} else {
		log.Debug("工具调用完成", "exposedName", exposedName, "upstreamID", candidate.UpstreamID, "latencyMS", int(time.Since(startedAt).Milliseconds()), "isError", result.IsError, "mode", mode)
		if hasPolicy && policy.CacheEnabled && policy.CacheTTLSeconds > 0 && !result.IsError {
			s.setCachedToolResult(apiKeyID, exposedName, args, result, time.Duration(policy.CacheTTLSeconds)*time.Second)
		}
	}

	return result, callErr
}

func (s *Service) selectCandidate(ctx context.Context, entry ReverseEntry, policy domain.ToolPolicyRule) (ToolCandidate, error) {
	candidates := make([]ToolCandidate, 0, len(entry.Candidates))
	for _, c := range entry.Candidates {
		if c.Compatible {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 0 {
		return ToolCandidate{}, domain.NewError(domain.CodeToolNotFound, "工具没有可兼容调用的上游来源")
	}
	strategy := s.currentRoutingStrategy()
	if domain.ValidToolRoutingStrategy(policy.RoutingStrategy) {
		strategy = domain.NormalizeOptionalToolRoutingStrategy(policy.RoutingStrategy)
	}
	start := 0
	if domain.ToolRoutingBalancesAcrossSources(strategy) && len(candidates) > 1 {
		s.roundRobinMu.Lock()
		n := s.roundRobin[entry.Name]
		s.roundRobin[entry.Name] = n + 1
		s.roundRobinMu.Unlock()
		start = int(n % uint64(len(candidates)))
	}
	var lastReason string
	availability, hasAvailability := s.invoker.(UpstreamAvailability)
	// recoveryCapable 表示调用器能在“尚未把工具请求发往上游”时协调一次安全的
	// 按需恢复。没有可用来源时可挑选一个兼容候选交由它等待恢复；若存在健康来源，
	// 仍优先使用健康来源，避免为恢复中的来源增加无谓等待。
	recovery, recoveryCapable := s.invoker.(RecoveryAwareInvoker)
	recoveryCapable = recoveryCapable && recovery.SupportsOnDemandRecovery()
	// 保留全部恢复候选，后续仍要逐个通过配额校验；不能因为首个离线来源已超额
	// 就错过另一个可以立即恢复并执行的同名来源。
	recoveryCandidates := make([]ToolCandidate, 0, len(candidates))
	sawAvailable := false
	sawQuotaLimited := false
	sawFailureDegraded := false
	for i := 0; i < len(candidates); i++ {
		idx := i
		if domain.ToolRoutingBalancesAcrossSources(strategy) {
			idx = (start + i) % len(candidates)
		}
		c := candidates[idx]
		if len(candidates) > 1 {
			if degraded, reason := s.sourceTemporarilyDegraded(c); degraded {
				sawFailureDegraded = true
				lastReason = reason
				// 短暂失败降级属于调用结果的保护窗口，不等同于连接不可用；即使
				// 其它来源不可用也不绕过它，避免把真实失败放大为重试风暴。
				continue
			}
		}
		if hasAvailability && !availability.UpstreamAvailable(c.UpstreamID) {
			lastReason = "所有上游来源当前均不可用"
			if recoveryCapable {
				recoveryCandidates = append(recoveryCandidates, c)
			}
			continue
		}
		sawAvailable = true
		cfg, ok := s.upstreamConfig(c.UpstreamID)
		if !ok {
			return c, nil
		}
		if s.quota == nil {
			return c, nil
		}
		allowed, reason := s.quota.Allow(ctx, c.UpstreamID, cfg.RateLimits)
		if allowed {
			return c, nil
		}
		sawQuotaLimited = true
		lastReason = reason
	}
	if lastReason == "" {
		lastReason = "所有上游来源均已达到限流或额度上限"
	}
	// 存在暂不可用但可恢复的兼容来源时，将其交给具备恢复能力的调用器。在已有
	// 健康来源受额度限制时，这也允许恢复后的其它来源承接请求。额度不在这里预占：
	// 连接恢复失败时工具从未发往上游，待会话恢复且即将首次分发时才原子预占。
	if hasAvailability && len(recoveryCandidates) > 0 {
		candidate := recoveryCandidates[0]
		candidate.deferQuotaReservation = true
		if len(recoveryCandidates) > 1 {
			candidate.recoveryFallbacks = make([]ToolCandidate, 0, len(recoveryCandidates)-1)
			for _, fallback := range recoveryCandidates[1:] {
				fallback.deferQuotaReservation = true
				candidate.recoveryFallbacks = append(candidate.recoveryFallbacks, fallback)
			}
		}
		return candidate, nil
	}
	if hasAvailability && !sawAvailable {
		return ToolCandidate{}, domain.NewError(domain.CodeUpstreamUnavailable, lastReason)
	}
	if sawFailureDegraded && !sawQuotaLimited {
		return ToolCandidate{}, domain.NewError(domain.CodeUpstreamUnavailable, lastReason)
	}
	if sawQuotaLimited {
		return ToolCandidate{}, domain.NewError(domain.CodeRateLimited, lastReason)
	}
	return ToolCandidate{}, domain.NewError(domain.CodeRateLimited, lastReason)
}

func (s *Service) resolveToolPolicy(ctx context.Context, exposedName string) (domain.ToolPolicyRule, bool, error) {
	policies, err := s.listToolPolicies(ctx)
	if err != nil {
		return domain.ToolPolicyRule{}, false, err
	}
	policy, ok := s.matchToolPolicy(policies, exposedName)
	return policy, ok, nil
}

func (s *Service) listToolPolicies(ctx context.Context) ([]domain.ToolPolicyRule, error) {
	if s.toolPolicies == nil {
		return nil, nil
	}
	policies, err := s.toolPolicies.ListToolPolicies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ToolPolicyRule, 0, len(policies))
	for _, policy := range policies {
		if !policy.Enabled {
			continue
		}
		out = append(out, policy)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].SortOrder < out[j].SortOrder
	})
	return out, nil
}

func (s *Service) matchToolPolicy(policies []domain.ToolPolicyRule, exposedName string) (domain.ToolPolicyRule, bool) {
	for _, policy := range policies {
		matched, err := s.engine.Match(policy.Pattern, policy.IsRegex, exposedName)
		if err != nil {
			continue
		}
		if matched {
			return policy, true
		}
	}
	return domain.ToolPolicyRule{}, false
}

func toolPolicyView(policy domain.ToolPolicyRule) *domain.ToolPolicyView {
	return &domain.ToolPolicyView{
		RuleID:          policy.ID,
		Pattern:         policy.Pattern,
		RoutingStrategy: policy.RoutingStrategy,
		CacheEnabled:    policy.CacheEnabled,
		CacheTTLSeconds: policy.CacheTTLSeconds,
		RiskTags:        append([]string(nil), policy.RiskTags...),
		IgnoredRiskTags: append([]string(nil), policy.IgnoredRiskTags...),
	}
}

func (s *Service) getCachedToolResult(apiKeyID, exposedName string, args json.RawMessage) (domain.ToolResult, bool) {
	key := toolResultCacheKey(apiKeyID, exposedName, args)
	now := s.currentTime()
	s.resultCacheMu.Lock()
	defer s.resultCacheMu.Unlock()
	entry, ok := s.resultCache[key]
	if !ok {
		s.resultCacheMisses++
		return domain.ToolResult{}, false
	}
	if !entry.expiresAt.After(now) {
		delete(s.resultCache, key)
		s.resultCacheMisses++
		s.resultCacheExpired++
		return domain.ToolResult{}, false
	}
	s.resultCacheHits++
	return cloneToolResult(entry.result), true
}

func (s *Service) setCachedToolResult(apiKeyID, exposedName string, args json.RawMessage, result domain.ToolResult, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	if len(result.Content) > maxCachedToolResultBytes {
		return
	}
	key := toolResultCacheKey(apiKeyID, exposedName, args)
	s.resultCacheMu.Lock()
	defer s.resultCacheMu.Unlock()
	if len(s.resultCache) >= maxResultCacheEntries {
		s.evictExpiredOrOldestCachedResult()
	}
	s.resultCache[key] = cachedToolResult{
		result:      cloneToolResult(result),
		expiresAt:   s.currentTime().Add(ttl),
		apiKeyID:    apiKeyID,
		exposedName: exposedName,
	}
	s.resultCacheStores++
}

func (s *Service) evictExpiredOrOldestCachedResult() {
	now := s.currentTime()
	var oldestKey string
	var oldest time.Time
	for key, entry := range s.resultCache {
		if !entry.expiresAt.After(now) {
			delete(s.resultCache, key)
			s.resultCacheExpired++
			return
		}
		if oldestKey == "" || entry.expiresAt.Before(oldest) {
			oldestKey = key
			oldest = entry.expiresAt
		}
	}
	if oldestKey != "" {
		delete(s.resultCache, oldestKey)
		s.resultCacheEvictions++
	}
}

func (s *Service) pruneExpiredCachedResultsLocked() {
	now := s.currentTime()
	for key, entry := range s.resultCache {
		if !entry.expiresAt.After(now) {
			delete(s.resultCache, key)
			s.resultCacheExpired++
		}
	}
}

func toolResultCacheKey(apiKeyID, exposedName string, args json.RawMessage) string {
	sum := sha256.Sum256(args)
	return apiKeyID + "|" + exposedName + "|" + hex.EncodeToString(sum[:])
}

func cloneToolResult(result domain.ToolResult) domain.ToolResult {
	out := result
	if result.Content != nil {
		out.Content = append(json.RawMessage(nil), result.Content...)
	}
	return out
}

func (s *Service) sourceTemporarilyDegraded(c ToolCandidate) (bool, string) {
	degraded, reason, _ := s.sourceDegradation(c)
	return degraded, reason
}

func (s *Service) sourceDegradation(c ToolCandidate) (bool, string, time.Time) {
	if s == nil {
		return false, "", time.Time{}
	}
	now := s.currentTime()
	key := sourceFailureKey(c)
	s.sourceFailureMu.Lock()
	defer s.sourceFailureMu.Unlock()
	state, ok := s.sourceFailures[key]
	if !ok {
		return false, "", time.Time{}
	}
	if !state.SuspendedUntil.IsZero() {
		if state.SuspendedUntil.After(now) {
			return true, "部分上游近期连续失败，已短暂降级到其他健康来源", state.SuspendedUntil
		}
		delete(s.sourceFailures, key)
	}
	return false, "", time.Time{}
}

func (s *Service) recordSourceResult(c ToolCandidate, err error) {
	if s == nil {
		return
	}
	if err == nil {
		// 上游已响应，即使 MCP 结果表示业务错误，也不视为连接健康失败。
		s.resetSourceFailure(c)
		return
	}
	if !isRoutableSourceFailure(err) {
		return
	}

	key := sourceFailureKey(c)
	now := s.currentTime()
	threshold := s.sourceFailureThreshold
	if threshold <= 0 {
		threshold = defaultSourceFailureThreshold
	}
	cooldown := s.sourceFailureCooldown
	if cooldown <= 0 {
		cooldown = defaultSourceFailureCooldown
	}

	s.sourceFailureMu.Lock()
	state := s.sourceFailures[key]
	state.Consecutive++
	if state.Consecutive >= threshold {
		state.SuspendedUntil = now.Add(cooldown)
	}
	s.sourceFailures[key] = state
	s.sourceFailureMu.Unlock()
}

func (s *Service) resetSourceFailure(c ToolCandidate) {
	key := sourceFailureKey(c)
	s.sourceFailureMu.Lock()
	delete(s.sourceFailures, key)
	s.sourceFailureMu.Unlock()
}

func sourceFailureKey(c ToolCandidate) string {
	return c.UpstreamID + "|" + c.OriginalName
}

func (s *Service) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func isRoutableSourceFailure(err error) bool {
	if apiErr, ok := errors.AsType[*domain.APIError](err); ok {
		return apiErr.Code == domain.CodeUpstreamUnavailable || apiErr.Code == domain.CodeUpstreamTimeout
	}
	return true
}

func (s *Service) upstreamConfig(id string) (domain.UpstreamConfig, bool) {
	s.upstreamConfigsMu.RLock()
	defer s.upstreamConfigsMu.RUnlock()
	cfg, ok := s.upstreamConfigs[id]
	return cfg, ok
}

// recordCall 在工具调用完成后以非阻塞方式提交一条调用统计记录（Req 16.1、16.8、16.9）。
//
// 采集维度采用稳定标识 (UpstreamID, OriginalName)（不随别名/排序变动而断裂），并记录
// 对外名、所用 API Key、毫秒精度时间戳与响应耗时（毫秒）。成败判定：转发返回 error 或
// 上游报告的错误结果（result.IsError）均记为失败，其余记为成功——与「原样透传上游结果」
// 的语义一致（Req 10.3）。recorder 未注入时为无操作。
func (s *Service) recordCall(ctx context.Context, apiKeyID, exposedName string, entry ToolCandidate, startedAt time.Time, args json.RawMessage, result domain.ToolResult, callErr error) {
	if s.recorder == nil {
		s.logger().Debug("统计记录器未注入，跳过调用统计", "exposedName", exposedName)
		return
	}
	latencyMS := int(time.Since(startedAt).Milliseconds())
	success := callErr == nil && !result.IsError
	status, errMsg, failureDetail := callFailure(result, callErr)
	mode := ModeFromContext(ctx)
	source := SourceFromContext(ctx)
	s.recorder.RecordAsync(ctx, store.CallStatRecord{
		UpstreamID:     entry.UpstreamID,
		UpstreamName:   entry.UpstreamName,
		OriginalName:   entry.OriginalName,
		ExposedName:    exposedName,
		APIKeyID:       apiKeyID,
		CalledAt:       startedAt.UTC(),
		LatencyMS:      latencyMS,
		Success:        success,
		Status:         status,
		RequestArgs:    args,
		ResponseResult: result.Content,
		ErrorMessage:   errMsg,
		FailureDetail:  failureDetail,
		Mode:           mode,
		Source:         source,
	})
	s.logger().Debug("调用统计已提交", "exposedName", exposedName, "upstreamID", entry.UpstreamID, "apiKeyID", apiKeyID, "success", success, "status", status, "latencyMS", latencyMS, "mode", mode, "source", source)
}

type callFailureDetail struct {
	Kind         string            `json:"kind"`
	Code         string            `json:"code,omitempty"`
	Message      string            `json:"message,omitempty"`
	HTTPStatus   int               `json:"httpStatus,omitempty"`
	BusinessCode int               `json:"businessCode,omitempty"`
	Timeout      bool              `json:"timeout,omitempty"`
	Fields       map[string]string `json:"fields,omitempty"`
}

func callFailure(result domain.ToolResult, callErr error) (string, string, json.RawMessage) {
	if callErr != nil {
		detail := callFailureDetail{
			Kind:         "gateway_error",
			Code:         string(domain.CodeInternal),
			Message:      callErr.Error(),
			HTTPStatus:   http.StatusInternalServerError,
			BusinessCode: 50000,
		}
		if apiErr, ok := errors.AsType[*domain.APIError](callErr); ok {
			detail.Code = string(apiErr.Code)
			detail.Message = apiErr.Message
			detail.HTTPStatus = domainErrorHTTPStatus(apiErr.Code)
			detail.BusinessCode = domainErrorBusinessCode(apiErr.Code)
			detail.Timeout = apiErr.Code == domain.CodeUpstreamTimeout
			detail.Fields = apiErr.Fields
		}
		return store.CallStatusFailed, callErr.Error(), marshalFailureDetail(detail)
	}
	if result.IsError {
		detail := callFailureDetail{
			Kind:    "upstream_result_error",
			Code:    "UPSTREAM_RESULT_ERROR",
			Message: "上游 MCP 返回错误结果",
		}
		return store.CallStatusUpstreamError, "", marshalFailureDetail(detail)
	}
	return store.CallStatusSuccess, "", nil
}

func marshalFailureDetail(detail callFailureDetail) json.RawMessage {
	raw, err := json.Marshal(detail)
	if err != nil {
		return nil
	}
	return raw
}

func domainErrorHTTPStatus(code domain.ErrorCode) int {
	switch code {
	case domain.CodeValidation:
		return http.StatusBadRequest
	case domain.CodeNotFound, domain.CodeToolNotFound:
		return http.StatusNotFound
	case domain.CodeConflict:
		return http.StatusConflict
	case domain.CodeUnauthorized:
		return http.StatusUnauthorized
	case domain.CodeForbidden:
		return http.StatusForbidden
	case domain.CodeRateLimited:
		return http.StatusTooManyRequests
	case domain.CodeUpstreamUnavailable:
		return http.StatusBadGateway
	case domain.CodeUpstreamTimeout:
		return http.StatusGatewayTimeout
	case domain.CodeBackupInvalid:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func domainErrorBusinessCode(code domain.ErrorCode) int {
	switch code {
	case domain.CodeValidation:
		return 40000
	case domain.CodeUnauthorized:
		return 40100
	case domain.CodeForbidden:
		return 40300
	case domain.CodeNotFound:
		return 40400
	case domain.CodeToolNotFound:
		return 40401
	case domain.CodeConflict:
		return 40900
	case domain.CodeBackupInvalid:
		return 42200
	case domain.CodeRateLimited:
		return 42900
	case domain.CodeUpstreamUnavailable:
		return 50200
	case domain.CodeUpstreamTimeout:
		return 50400
	default:
		return 50000
	}
}

// buildToolSetWithReverseMap 是聚合管线的数据获取编排层，返回可见工具集合及其反向映射。
//
// 职责分层：
//   - 本方法只负责「读取数据」——读启用上游、读各上游的别名/屏蔽规则、读 API Key 屏蔽规则，
//     并从工具缓存读取各启用上游的工具列表（阶段 1，仅启用上游，Req 6.2、10.1）。
//   - 真正的确定性变换交给纯函数 runPipeline（阶段 2-6），便于属性测试直接构造输入。
//
// 该方法供 BuildToolSet 与（任务 4.6）InvokeTool 共同复用：后者需要反向映射来把对外名
// 还原为 (上游标识, 原始名) 后转发调用（Req 10.6）。
func (s *Service) buildToolSetWithReverseMap(ctx context.Context, apiKeyID string) ([]domain.ToolDef, map[string]ReverseEntry, error) {
	log := s.logger()
	bundles := make([]upstreamBundle, 0)

	// 阶段 1：仅取启用上游，并从缓存读取其工具列表与绑定的规则。
	upstreams, err := s.upstreams.ListUpstreams(ctx)
	if err != nil {
		log.Warn("读取上游列表失败", "apiKeyID", apiKeyID, "error", err)
		return nil, nil, err
	}

	for _, up := range upstreams {
		// 停用上游完全不参与聚合（Req 3.3）。
		if !up.Config.Enabled {
			continue
		}

		// 从工具缓存读取该上游的工具列表；未命中则视为该上游暂无工具（不报错）。
		// 聚合路径永不向上游实时拉取（Req 6.2）。
		tools, _, _ := s.cache.Get(ctx, up.ID)

		aliases, err := s.aliases.ListAliasesByUpstream(ctx, up.ID)
		if err != nil {
			log.Warn("读取上游别名规则失败", "upstreamID", up.ID, "error", err)
			return nil, nil, err
		}
		mcpFilters, err := s.mcpFilters.ListMCPFiltersByUpstream(ctx, up.ID)
		if err != nil {
			log.Warn("读取上游 MCP 级屏蔽规则失败", "upstreamID", up.ID, "error", err)
			return nil, nil, err
		}

		bundles = append(bundles, upstreamBundle{
			upstreamID:   up.ID,
			upstreamName: up.Config.Name,
			sortOrder:    up.Config.SortOrder,
			tools:        tools,
			aliases:      aliases,
			mcpFilters:   mcpFilters,
		})
	}
	configs := make(map[string]domain.UpstreamConfig, len(upstreams))
	for _, up := range upstreams {
		configs[up.ID] = up.Config
	}
	s.upstreamConfigsMu.Lock()
	s.upstreamConfigs = configs
	s.upstreamConfigsMu.Unlock()

	// 阶段 6 所需的 API Key 级屏蔽规则：apiKeyID 为空表示无 API Key 级过滤。
	var apiKeyFilters []domain.FilterRule
	if apiKeyID != "" {
		apiKeyFilters, err = s.apiKeyFilters.ListAPIKeyFiltersByAPIKey(ctx, apiKeyID)
		if err != nil {
			log.Warn("读取 API Key 级屏蔽规则失败", "apiKeyID", apiKeyID, "error", err)
			return nil, nil, err
		}
	}

	// 阶段 2-6：交由确定性纯函数管线处理。
	tools, reverse := runPipeline(s.engine, bundles, apiKeyFilters)
	return tools, reverse, nil
}

func (s *Service) reserveCandidateQuota(ctx context.Context, candidate ToolCandidate) error {
	cfg, ok := s.upstreamConfig(candidate.UpstreamID)
	if !ok || s.quota == nil {
		return nil
	}
	allowed, reason := s.quota.Allow(ctx, candidate.UpstreamID, cfg.RateLimits)
	if allowed {
		return nil
	}
	return domain.NewError(domain.CodeRateLimited, reason)
}
