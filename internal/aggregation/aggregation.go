package aggregation

import (
	"context"
	"encoding/json"
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

// Service 是 domain.Aggregation_Service 的实现，编排聚合管线并（后续任务）负责调用路由。
//
// 依赖通过构造函数注入，全部为窄接口与领域接口，不直接耦合具体存储类型：
//   - cache：工具缓存（domain.Tool_Cache），聚合永不实时拉取上游（Req 6.2）。
//   - engine：规则引擎（domain.Rule_Engine），提供屏蔽/别名/匹配的纯函数能力。
//   - upstreams/aliases/mcpFilters/apiKeyFilters：数据访问窄接口。
//
// 本类型的 BuildToolSet 在任务 4.1 实现；InvokeTool 的可见性校验与别名反向映射路由
// 在任务 4.6 实现，转发动作经可选注入的 UpstreamInvoker（窄接口）占位；真实的上游会话
// 调用与超时控制在任务 11.1 接入。
type Service struct {
	cache         domain.Tool_Cache
	engine        domain.Rule_Engine
	upstreams     UpstreamLister
	aliases       AliasLister
	mcpFilters    MCPFilterLister
	apiKeyFilters APIKeyFilterLister
	// invoker 为上游调用转发器（窄接口），可选注入。
	// 为 nil 时表示尚未接线真实上游会话（任务 11.1），此时 InvokeTool 在通过
	// 可见性校验后返回占位错误；可见性校验逻辑始终完整执行（Req 10.4、11.7）。
	invoker UpstreamInvoker
	// recorder 为调用统计异步记录器（窄接口），可选注入（Req 16.1、16.8）。
	// 为 nil 时聚合调用路径不采集统计；非 nil 时在工具调用完成后以非阻塞方式提交一条
	// 调用记录，绝不阻塞主流程（Req 16.8、16.9）。
	recorder StatRecorder
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
) *Service {
	return &Service{
		cache:         cache,
		engine:        engine,
		upstreams:     upstreams,
		aliases:       aliases,
		mcpFilters:    mcpFilters,
		apiKeyFilters: apiKeyFilters,
	}
}

// SetInvoker 注入上游调用转发器（窄接口），供 InvokeTool 在反向映射后转发调用。
//
// 采用可选 setter 而非修改 NewService 签名，以避免破坏既有调用方：未注入时（invoker
// 为 nil），InvokeTool 仍完整执行可见性校验（Req 10.4、11.7），仅在校验通过后因尚未
// 接线真实上游会话而返回占位错误。真实转发器由任务 11.1 提供并通过本方法注入。
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
// UpstreamInvoker 的实现方在任务 11.1 提供。当 invoker 未注入（nil）时，本方法在通过
// 可见性校验后返回占位错误 domain.ErrNotImplemented——可见性校验逻辑始终完整执行。
func (s *Service) InvokeTool(ctx context.Context, apiKeyID, exposedName string, args json.RawMessage) (domain.ToolResult, error) {
	// 步骤 1：构建该 API Key 视角的可见集合与反向映射。
	_, reverse, err := s.buildToolSetWithReverseMap(ctx, apiKeyID)
	if err != nil {
		return domain.ToolResult{}, err
	}

	// 步骤 2：可见性校验。不在可见集合内则拒绝且不转发（Req 10.4、11.7）。
	entry, ok := reverse[exposedName]
	if !ok {
		return domain.ToolResult{}, domain.NewError(domain.CodeToolNotFound, "工具不存在于当前可见聚合工具集合中")
	}

	// 步骤 3-4：命中——反向映射已还原 (UpstreamID, OriginalName)，经窄接口透传转发。
	// invoker 未注入时（任务 11.1 之前）返回占位错误；可见性校验在此之前已完整执行。
	if s.invoker == nil {
		return domain.ToolResult{}, domain.ErrNotImplemented
	}

	// 调用前记录起点，用于计算响应耗时（毫秒，Req 16.1）。统计写入绝不影响调用结果。
	startedAt := time.Now()
	result, callErr := s.invoker.CallUpstream(ctx, entry.UpstreamID, entry.OriginalName, args)

	// 步骤 5：异步采集调用统计（Req 16.1、16.8、16.9）。
	// 以非阻塞方式提交，主流程附加耗时极小且永不阻塞；记录器未注入时跳过。
	s.recordCall(ctx, apiKeyID, exposedName, entry, startedAt, result, callErr)

	return result, callErr
}

// recordCall 在工具调用完成后以非阻塞方式提交一条调用统计记录（Req 16.1、16.8、16.9）。
//
// 采集维度采用稳定标识 (UpstreamID, OriginalName)（不随别名/排序变动而断裂），并记录
// 对外名、所用 API Key、毫秒精度时间戳与响应耗时（毫秒）。成败判定：转发返回 error 或
// 上游报告的错误结果（result.IsError）均记为失败，其余记为成功——与「原样透传上游结果」
// 的语义一致（Req 10.3）。recorder 未注入时为无操作。
func (s *Service) recordCall(ctx context.Context, apiKeyID, exposedName string, entry ReverseEntry, startedAt time.Time, result domain.ToolResult, callErr error) {
	if s.recorder == nil {
		return
	}
	latencyMS := int(time.Since(startedAt).Milliseconds())
	success := callErr == nil && !result.IsError
	s.recorder.RecordAsync(ctx, store.CallStatRecord{
		UpstreamID:   entry.UpstreamID,
		OriginalName: entry.OriginalName,
		ExposedName:  exposedName,
		APIKeyID:     apiKeyID,
		CalledAt:     startedAt.UTC(),
		LatencyMS:    latencyMS,
		Success:      success,
	})
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
	bundles := make([]upstreamBundle, 0)

	// 阶段 1：仅取启用上游，并从缓存读取其工具列表与绑定的规则。
	upstreams, err := s.upstreams.ListUpstreams(ctx)
	if err != nil {
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
			return nil, nil, err
		}
		mcpFilters, err := s.mcpFilters.ListMCPFiltersByUpstream(ctx, up.ID)
		if err != nil {
			return nil, nil, err
		}

		bundles = append(bundles, upstreamBundle{
			upstreamID: up.ID,
			sortOrder:  up.Config.SortOrder,
			tools:      tools,
			aliases:    aliases,
			mcpFilters: mcpFilters,
		})
	}

	// 阶段 6 所需的 API Key 级屏蔽规则：apiKeyID 为空表示无 API Key 级过滤。
	var apiKeyFilters []domain.FilterRule
	if apiKeyID != "" {
		apiKeyFilters, err = s.apiKeyFilters.ListAPIKeyFiltersByAPIKey(ctx, apiKeyID)
		if err != nil {
			return nil, nil, err
		}
	}

	// 阶段 2-6：交由确定性纯函数管线处理。
	tools, reverse := runPipeline(s.engine, bundles, apiKeyFilters)
	return tools, reverse, nil
}
