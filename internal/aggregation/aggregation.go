package aggregation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
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
		log:           slog.Default(),
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

	// 步骤 3-4：命中——反向映射已还原 (UpstreamID, OriginalName)，经窄接口透传转发。
	// invoker 未注入时（仅未接线装配或单元测试）返回防御性占位错误；可见性校验在此之前已完整执行。
	if s.invoker == nil {
		log.Warn("上游调用转发器未注入，调用未执行", "exposedName", exposedName, "upstreamID", entry.UpstreamID)
		return domain.ToolResult{}, domain.ErrNotImplemented
	}

	// 调用前记录起点，用于计算响应耗时（毫秒，Req 16.1）。统计写入绝不影响调用结果。
	log.Debug("工具调用转发上游", "exposedName", exposedName, "upstreamID", entry.UpstreamID, "originalName", entry.OriginalName, "apiKeyID", apiKeyID, "mode", mode)
	startedAt := time.Now()
	result, callErr := s.invoker.CallUpstream(ctx, entry.UpstreamID, entry.OriginalName, args)

	// 步骤 5：异步采集调用统计（Req 16.1、16.8、16.9）。
	// 以非阻塞方式提交，主流程附加耗时极小且永不阻塞；记录器未注入时跳过。
	s.recordCall(ctx, apiKeyID, exposedName, entry, startedAt, args, result, callErr)

	if callErr != nil {
		log.Warn("工具调用上游失败", "exposedName", exposedName, "upstreamID", entry.UpstreamID, "latencyMS", int(time.Since(startedAt).Milliseconds()), "error", callErr)
	} else {
		log.Debug("工具调用完成", "exposedName", exposedName, "upstreamID", entry.UpstreamID, "latencyMS", int(time.Since(startedAt).Milliseconds()), "isError", result.IsError, "mode", mode)
	}

	return result, callErr
}

// recordCall 在工具调用完成后以非阻塞方式提交一条调用统计记录（Req 16.1、16.8、16.9）。
//
// 采集维度采用稳定标识 (UpstreamID, OriginalName)（不随别名/排序变动而断裂），并记录
// 对外名、所用 API Key、毫秒精度时间戳与响应耗时（毫秒）。成败判定：转发返回 error 或
// 上游报告的错误结果（result.IsError）均记为失败，其余记为成功——与「原样透传上游结果」
// 的语义一致（Req 10.3）。recorder 未注入时为无操作。
func (s *Service) recordCall(ctx context.Context, apiKeyID, exposedName string, entry ReverseEntry, startedAt time.Time, args json.RawMessage, result domain.ToolResult, callErr error) {
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
		var apiErr *domain.APIError
		if errors.As(callErr, &apiErr) {
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
			log.Warn("读取 API Key 级屏蔽规则失败", "apiKeyID", apiKeyID, "error", err)
			return nil, nil, err
		}
	}

	// 阶段 2-6：交由确定性纯函数管线处理。
	tools, reverse := runPipeline(s.engine, bundles, apiKeyFilters)
	return tools, reverse, nil
}
