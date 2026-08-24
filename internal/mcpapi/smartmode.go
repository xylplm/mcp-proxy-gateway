package mcpapi

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/toolsearch"
)

// 本文件实现对外 MCP API 服务（MCP_API_Service）的「智能模式」（Req 11.3-11.7）。
//
// 智能模式语义：对外仅暴露少量「网关工具」（gateway tools），而不一次性暴露全部聚合
// 工具定义，以节省外部 AI 客户端（如 Claude Code）的上下文窗口。客户端通过网关工具按需
// 发现、获取入参结构并调用具体聚合工具：
//   - list_tools：分页返回可见聚合工具的「名称 + 简述」概览，不含完整 inputSchema；
//   - search_tools：按关键字过滤（名称或描述命中），返回数量默认取配置 smart_discovery_limit
//     （默认 50，范围 1-200），无匹配返回空列表而非错误（Req 11.4、11.5）；
//   - get_tool：返回单个工具的完整定义（含 inputSchema），不可见工具返回工具不存在（Req 11.7）；
//   - call_tool：经聚合服务路由到具体聚合工具，不可见返回工具不存在且不发起调用（Req 11.6、11.7）。
//
// 与全量模式一致，所有可见性判定都以聚合服务 BuildToolSet/InvokeTool 为唯一来源（已过完整
// 管线，含 API Key 级过滤），保证两种模式的可见性一致，差异仅在「暴露方式」。
//
// 本文件聚焦「模式无关」的网关工具编排核心 SmartModeHandler，不耦合任何真实的 MCP server
// transport——SSE/Streamable-HTTP/WebSocket 的接线属任务 13.4；届时只需把这里的网关工具
// 定义（GatewayTools）与四个处理方法适配为对应传输的 server handler 即可。
// 为避免与 fullmode.go 的标识符冲突，本文件类型/方法统一以 SmartMode/Gateway 前缀命名，
// 并复用 tools.go 中的共享转换辅助（toMCPTool/toCallToolResult），不重复定义。

// 智能模式工具发现返回数的取值边界（Req 11.4）。
const (
	minDiscoveryLimit     = 1
	maxDiscoveryLimit     = 200
	defaultDiscoveryLimit = 50
)

// 智能模式对外暴露的四个网关工具名称。
const (
	GatewayToolListTools   = "list_tools"
	GatewayToolSearchTools = "search_tools"
	GatewayToolGetTool     = "get_tool"
	GatewayToolCallTool    = "call_tool"
)

// 智能模式网关工具描述。描述会直接暴露给 AI 客户端，需强调使用顺序与入参约束。
const (
	GatewayToolListToolsDescription   = "【第一步·发现】分页浏览可用工具的名称和简述。首次调用会返回上游服务概览；工具很多时优先用 upstream 按服务筛选，避免逐页浏览。选定工具后用 get_tool 获取完整定义，再用 call_tool 执行。"
	GatewayToolSearchToolsDescription = "【发现】用多个关键词或自然语言检索工具，支持空格分隔、常见缩写和中英文运维术语。结果按相关度排序，不要求所有词都命中；无结果时会给出候选关键词。找到目标后，用 get_tool 获取完整参数定义，再用 call_tool 调用。"
	GatewayToolGetToolDescription     = "【第二步·获取定义】根据一个或多个工具名称获取完整定义，包括 inputSchema（参数结构、必填字段和类型约束）。批量最多 20 个；在调用 call_tool 前必须先确认参数格式。"
	GatewayToolCallToolDescription    = "【第三步·执行】调用指定的真实聚合工具。name 必须来自 list_tools 或 search_tools 返回的结果，arguments 必须符合 get_tool 返回的 inputSchema。请确保先发现并获取定义再调用。"
)

// 各网关工具的入参 JSON Schema。这些 Schema 在智能模式 tools/list 中作为网关工具定义对外
// 暴露，供客户端构造网关工具调用入参。
var (
	listToolsInputSchema = json.RawMessage(`{"type":"object","properties":{"cursor":{"type":"string","maxLength":20,"description":"上一页返回的分页游标，省略则从头开始"},"limit":{"type":"integer","minimum":1,"maximum":200,"description":"本页返回条数，范围 1-200，省略则使用服务端默认值"},"upstream":{"type":"string","maxLength":256,"description":"按上游服务名称筛选；可使用首次返回的 upstreams 概览中的名称"}}}`)

	searchToolsInputSchema = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","maxLength":512,"description":"多个关键词或自然语言需求；支持常见缩写与中英文运维术语，最多 512 个字符"},"cursor":{"type":"string","maxLength":20,"description":"上一页返回的分页游标，省略则从头开始"},"limit":{"type":"integer","minimum":1,"maximum":200,"description":"返回条数，范围 1-200，默认 50"}},"required":["query"]}`)

	getToolInputSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"description":"单个目标工具的对外名称"},"names":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":20,"description":"批量获取的目标工具名称，最多 20 个；与 name 二选一"}},"oneOf":[{"required":["name"]},{"required":["names"]}]}`)

	callToolInputSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"description":"目标工具的对外名称"},"arguments":{"type":"object","description":"传递给目标工具的原始入参"}},"required":["name"]}`)
)

// ToolSummary 是工具的轻量摘要（仅名称与简述），用于 list_tools / search_tools 的返回。
//
// 智能模式刻意只返回名称 + 描述以节省上下文，不携带完整 inputSchema；客户端选定目标工具后
// 再用 get_tool 取回该工具的完整定义。
type ToolSummary struct {
	// Name 为工具对外暴露名称（已由聚合管线完成别名重写与同名去重）。
	Name string `json:"name"`
	// Description 为工具对外暴露描述（简述）。
	Description string `json:"description"`
	// Upstream is a stable, human-readable source label. Multiple sources are
	// joined in lexical order while SourceCount retains the exact cardinality.
	Upstream string `json:"upstream,omitempty"`
	// SourceCount identifies an aggregated tool backed by multiple upstreams.
	SourceCount int `json:"sourceCount,omitempty"`
	// SchemaConflict prompts callers to obtain the current input schema first.
	SchemaConflict bool `json:"schemaConflict,omitempty"`
}

// UpstreamSummary is the compact first-page overview for large tool sets.
type UpstreamSummary struct {
	Name      string `json:"name"`
	ToolCount int    `json:"toolCount"`
}

// ToolPage 是 list_tools 的分页结果。
type ToolPage struct {
	// Tools 为本页工具摘要列表，始终非 nil（无数据时为空切片）。
	Tools []ToolSummary `json:"tools"`
	// NextCursor 为下一页游标；为空表示已无更多数据。
	NextCursor string `json:"nextCursor,omitempty"`
	// Upstreams is returned only for the first page when source information is
	// available, avoiding repeated token cost while paging.
	Upstreams []UpstreamSummary `json:"upstreams,omitempty"`
}

// SearchPage mirrors ToolPage while including deterministic zero-result help.
type SearchPage struct {
	Tools       []ToolSummary `json:"tools"`
	NextCursor  string        `json:"nextCursor,omitempty"`
	Suggestions []string      `json:"suggestions"`
	Hint        string        `json:"hint,omitempty"`
}

// ToolBatch is returned only for get_tool's names form. The existing name form
// intentionally remains a direct MCP tool definition for compatibility.
type ToolBatch struct {
	Tools    []*mcp.Tool `json:"tools"`
	NotFound []string    `json:"notFound"`
}

const (
	searchHintWithSuggestions = "未匹配到工具。可尝试上述关键词，或调用 list_tools 查看有哪些上游服务。"
	searchHintBrowseTools     = "未匹配到工具。请调用 list_tools 查看可用上游服务和工具。"
	descriptionSummaryLimit   = 240
	maxBatchToolNames         = 20
	maxUpstreamFilterRunes    = 256
	maxCursorBytes            = 20
)

// toolDiscoverySource is an optional, cached source-label capability. The
// base aggregation interface remains small, so tests and third-party
// implementations retain the name-and-description-only fallback.
type toolDiscoverySource interface {
	BuildToolDiscoveries(ctx context.Context, apiKeyID string) ([]domain.ToolDiscovery, error)
}

// toolSearchSource adds the lazily cached lexical index used only by
// search_tools. Implementations also supply the discovery projection used by
// list_tools.
type toolSearchSource interface {
	toolDiscoverySource
	BuildToolSearchSet(ctx context.Context, apiKeyID string) ([]domain.ToolDiscovery, *toolsearch.Index, error)
}

// toolDetailSource remains a compatibility fallback for existing aggregation
// implementations that can supply source labels but not a cached search index.
type toolDetailSource interface {
	BuildToolDetails(ctx context.Context, apiKeyID string) ([]domain.ToolDetail, error)
}

type smartToolSources struct {
	names []string
	tags  []string
}

// SmartModeHandler 是智能模式下的网关工具编排核心。
//
// 它把对外的网关工具调用映射为对聚合服务的操作：list_tools/search_tools/get_tool 基于
// BuildToolSet 求得某 API Key 视角的可见聚合工具集合后在内存中分页/过滤/查找；call_tool
// 经 InvokeTool 路由到上游（含可见性校验与别名反向映射）。
//
// 该类型不持有任何传输状态，可被多传输端点共享；可见性始终以 BuildToolSet/InvokeTool 为
// 唯一来源，保证智能模式与全量模式的可见性一致。
type SmartModeHandler struct {
	agg domain.Aggregation_Service
	// discoveryLimit 为工具发现返回数默认值（来自配置 smart_discovery_limit）。
	// 构造时已保证落在 [minDiscoveryLimit, maxDiscoveryLimit] 内。
	discoveryLimit int
}

// NewSmartModeHandler 构造智能模式处理器，注入聚合服务接口与工具发现返回数默认值。
//
// discoveryLimit 取自配置 mcp_api.smart_discovery_limit；若传入值越界（非 1-200），
// 则回退到默认值 50，保证后续返回数始终合法（Req 11.4）。
func NewSmartModeHandler(agg domain.Aggregation_Service, discoveryLimit int) *SmartModeHandler {
	if discoveryLimit < minDiscoveryLimit || discoveryLimit > maxDiscoveryLimit {
		discoveryLimit = defaultDiscoveryLimit
	}
	return &SmartModeHandler{agg: agg, discoveryLimit: discoveryLimit}
}

// GatewayTools 返回智能模式下对外暴露的四个网关工具定义（供 tools/list 一次性返回，Req 11.3）。
//
// 智能模式 tools/list 只暴露这四个工具，客户端上下文仅占用四个工具定义，而非全部聚合工具。
// 始终返回固定顺序的非 nil 切片。
func (h *SmartModeHandler) GatewayTools() []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:        GatewayToolListTools,
			Description: GatewayToolListToolsDescription,
			InputSchema: listToolsInputSchema,
		},
		{
			Name:        GatewayToolSearchTools,
			Description: GatewayToolSearchToolsDescription,
			InputSchema: searchToolsInputSchema,
		},
		{
			Name:        GatewayToolGetTool,
			Description: GatewayToolGetToolDescription,
			InputSchema: getToolInputSchema,
		},
		{
			Name:        GatewayToolCallTool,
			Description: GatewayToolCallToolDescription,
			InputSchema: callToolInputSchema,
		},
	}
}

// ListTools 实现网关工具 list_tools：分页返回可见聚合工具的「名称 + 简述」，并在首页提供
// 上游概览；可选 upstream 只在当前可见集合中筛选（Req 11.3）。
//
//   - apiKeyID 为空：基于全局可见聚合集合；非空：基于该 Key 视角的可见集合（含 API Key 级过滤）。
//   - cursor 为上一页返回的游标（本实现以十进制偏移量编码）；为空表示从头开始；非法游标返回
//     校验错误。
//   - limit 为本页条数，<=0 表示使用配置默认值，越界则收敛到 [1,200]。
//
// 返回的 ToolPage.Tools 始终非 nil；当还有后续数据时 NextCursor 指向下一偏移量，否则为空。
func (h *SmartModeHandler) ListTools(ctx context.Context, apiKeyID, cursor, upstream string, limit int) (ToolPage, error) {
	offset, err := parseCursor(cursor)
	if err != nil {
		return ToolPage{}, err
	}
	if exceedsRuneLimit(upstream, maxUpstreamFilterRunes) {
		return ToolPage{}, domain.NewValidationError("网关工具入参非法", map[string]string{
			"upstream": "上游筛选条件不能超过 256 个字符",
		})
	}
	tools, sources, err := h.loadDiscovery(ctx, apiKeyID)
	if err != nil {
		return ToolPage{}, err
	}
	overview := upstreamOverview(tools, sources)
	if strings.TrimSpace(upstream) != "" {
		tools = filterByUpstream(tools, sources, upstream)
	}

	total := len(tools)
	// 偏移量越界时收敛到末尾，返回空页（不报错）。
	if offset > total {
		offset = total
	}

	end := min(offset+h.resolveLimit(limit), total)

	page := ToolPage{Tools: toToolSummaries(tools[offset:end], sources)}
	if end < total {
		page.NextCursor = strconv.Itoa(end)
	}
	if cursor == "" && len(overview) > 0 {
		page.Upstreams = overview
	}
	return page, nil
}

// SearchTools 实现网关工具 search_tools：对可见聚合工具进行确定性词法召回与排序
// （Req 11.4、11.5、11.10）。
//
//   - 查询在纯函数检索内核中完成归一化、分词、停用词过滤与常见缩写展开；命中任一有效
//     词元即可返回，覆盖率和字段权重决定稳定排序。
//   - limit <=0 表示使用配置默认值（smart_discovery_limit），越界则收敛到 [1,200]。
//   - 命中数量超过有效返回数时截断；无匹配或空查询时返回空页与固定引导（Req 11.5）。
//
// 返回的 Tools 和 Suggestions 始终非 nil。
func (h *SmartModeHandler) SearchTools(ctx context.Context, apiKeyID, query, cursor string, limit int) (SearchPage, error) {
	if toolsearch.QueryTooLong(query) {
		return SearchPage{}, domain.NewValidationError("网关工具入参非法", map[string]string{
			"query": "查询不能超过 512 个字符",
		})
	}
	offset, err := parseCursor(cursor)
	if err != nil {
		return SearchPage{}, err
	}
	tools, sources, index, err := h.loadSearchDiscovery(ctx, apiKeyID)
	if err != nil {
		return SearchPage{}, err
	}
	result := index.Search(query, h.resolveLimit(limit), offset)
	page := SearchPage{
		Tools:       make([]ToolSummary, 0, len(result.Hits)),
		Suggestions: append([]string{}, result.Suggestions...),
	}
	for _, hit := range result.Hits {
		if hit.DocIndex < 0 || hit.DocIndex >= len(tools) {
			return SearchPage{}, domain.NewError(domain.CodeInternal, "工具检索索引与可见工具集合不一致")
		}
		tool := tools[hit.DocIndex]
		page.Tools = append(page.Tools, toToolSummary(tool, sources[tool.Name]))
	}
	if offset+len(result.Hits) < result.Total {
		page.NextCursor = strconv.Itoa(offset + len(result.Hits))
	}
	if result.Total == 0 {
		if len(page.Suggestions) > 0 {
			page.Hint = searchHintWithSuggestions
		} else {
			page.Hint = searchHintBrowseTools
		}
	}
	return page, nil
}

// GetTool 实现网关工具 get_tool：返回单个可见工具的完整定义（含 inputSchema，Req 11.7）。
//
// 在该 API Key 视角的可见聚合集合中按对外名称查找：命中则转换为 MCP 工具定义返回；
// 不可见（不在集合内）则返回 TOOL_NOT_FOUND，不泄露被过滤工具的存在。
func (h *SmartModeHandler) GetTool(ctx context.Context, apiKeyID, name string) (*mcp.Tool, error) {
	tools, err := h.agg.BuildToolSet(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}
	for _, t := range tools {
		if t.Name == name {
			return toMCPTool(t), nil
		}
	}
	return nil, domain.NewError(domain.CodeToolNotFound, "工具不存在于当前可见聚合工具集合中")
}

// GetTools retrieves up to 20 visible tools in one request. Missing names are
// echoed only from caller input, so the batch form discloses no hidden tool.
func (h *SmartModeHandler) GetTools(ctx context.Context, apiKeyID string, names []string) (ToolBatch, error) {
	if len(names) == 0 || len(names) > maxBatchToolNames {
		return ToolBatch{}, domain.NewValidationError("网关工具入参非法", map[string]string{
			"names": "批量获取工具名数量必须在 1 至 20 之间",
		})
	}
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return ToolBatch{}, domain.NewValidationError("网关工具入参非法", map[string]string{
				"names": "批量获取的工具名不能为空",
			})
		}
	}
	tools, err := h.agg.BuildToolSet(ctx, apiKeyID)
	if err != nil {
		return ToolBatch{}, err
	}
	visible := make(map[string]domain.ToolDef, len(tools))
	for _, tool := range tools {
		visible[tool.Name] = tool
	}
	batch := ToolBatch{Tools: make([]*mcp.Tool, 0), NotFound: make([]string, 0)}
	for _, name := range names {
		if tool, ok := visible[name]; ok {
			batch.Tools = append(batch.Tools, toMCPTool(tool))
		} else {
			batch.NotFound = append(batch.NotFound, name)
		}
	}
	return batch, nil
}

// CallTool 实现网关工具 call_tool：将一次调用经聚合服务路由到具体聚合工具（Req 11.6、11.7）。
//
//   - name 为对外暴露的工具名称；args 为原始入参，原样透传给聚合服务（Req 10.3）。
//   - 工具不在当前可见集合内时，聚合服务 InvokeTool 返回 TOOL_NOT_FOUND 且不向任何上游
//     转发（Req 11.7）；该错误原样上抛由传输层映射为 MCP 错误响应。
//   - 上游成功结果与上游报告的错误结果均经 toCallToolResult 转换后原样返回（Req 10.3）。
func (h *SmartModeHandler) CallTool(ctx context.Context, apiKeyID, name string, args json.RawMessage) (*mcp.CallToolResult, error) {
	res, err := h.agg.InvokeTool(ctx, apiKeyID, name, args)
	if err != nil {
		return nil, err
	}
	return toCallToolResult(res)
}

// resolveLimit 将请求的返回数收敛到合法返回数：<=0 使用配置默认值，否则收敛到 [1,200]。
func (h *SmartModeHandler) resolveLimit(requested int) int {
	if requested <= 0 {
		return h.discoveryLimit
	}
	if requested < minDiscoveryLimit {
		return minDiscoveryLimit
	}
	if requested > maxDiscoveryLimit {
		return maxDiscoveryLimit
	}
	return requested
}

// toToolSummaries 将领域工具定义列表转换为轻量摘要列表，并截断过长描述以控制上下文占用。
//
// 始终返回非 nil 切片，保证「无工具」时返回空列表而非 null。
func toToolSummaries(tools []domain.ToolDef, sources map[string]smartToolSources) []ToolSummary {
	out := make([]ToolSummary, 0, len(tools))
	for _, t := range tools {
		out = append(out, toToolSummary(t, sources[t.Name]))
	}
	return out
}

func toToolSummary(tool domain.ToolDef, source smartToolSources) ToolSummary {
	return ToolSummary{
		Name:           tool.Name,
		Description:    truncateDescription(tool.Description, descriptionSummaryLimit),
		Upstream:       strings.Join(source.names, ", "),
		SourceCount:    tool.SourceCount,
		SchemaConflict: tool.SchemaConflict,
	}
}

func truncateDescription(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func parseCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	if len(cursor) > maxCursorBytes {
		return 0, domain.NewValidationError(
			"分页游标非法",
			map[string]string{"cursor": "游标必须为非负整数"},
		)
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, domain.NewValidationError(
			"分页游标非法",
			map[string]string{"cursor": "游标必须为非负整数"},
		)
	}
	return offset, nil
}

func (h *SmartModeHandler) loadDiscovery(ctx context.Context, apiKeyID string) ([]domain.ToolDef, map[string]smartToolSources, error) {
	if discovered, ok := h.agg.(toolDiscoverySource); ok {
		discoveries, err := discovered.BuildToolDiscoveries(ctx, apiKeyID)
		if err != nil {
			return nil, nil, err
		}
		tools, sources := discoveryParts(discoveries)
		return tools, sources, nil
	}
	if detailed, ok := h.agg.(toolDetailSource); ok {
		details, err := detailed.BuildToolDetails(ctx, apiKeyID)
		if err != nil {
			// Source labels are an optional enhancement. A failure in their
			// enrichment path (for example, a tool-policy read) must not make
			// Smart-mode discovery unavailable when the authorized tool set is
			// still readable.
			tools, baseErr := h.agg.BuildToolSet(ctx, apiKeyID)
			if baseErr != nil {
				return nil, nil, baseErr
			}
			return tools, make(map[string]smartToolSources), nil
		}
		tools := make([]domain.ToolDef, 0, len(details))
		sources := make(map[string]smartToolSources, len(details))
		for _, detail := range details {
			tools = append(tools, detail.Tool)
			names := make([]string, 0, len(detail.Sources))
			tags := make([]string, 0, len(detail.Sources))
			for _, source := range detail.Sources {
				names = append(names, source.UpstreamName)
				tags = append(tags, source.UpstreamTags...)
			}
			sources[detail.Tool.Name] = smartToolSources{names: sortedUniqueStrings(names), tags: sortedUniqueStrings(tags)}
		}
		return tools, sources, nil
	}
	tools, err := h.agg.BuildToolSet(ctx, apiKeyID)
	if err != nil {
		return nil, nil, err
	}
	return tools, make(map[string]smartToolSources), nil
}

func (h *SmartModeHandler) loadSearchDiscovery(ctx context.Context, apiKeyID string) ([]domain.ToolDef, map[string]smartToolSources, *toolsearch.Index, error) {
	if indexed, ok := h.agg.(toolSearchSource); ok {
		discoveries, index, err := indexed.BuildToolSearchSet(ctx, apiKeyID)
		if err != nil {
			return nil, nil, nil, err
		}
		tools, sources := discoveryParts(discoveries)
		if index == nil {
			index = toolsearch.Build(toolSearchDocs(tools, sources))
		}
		return tools, sources, index, nil
	}
	tools, sources, err := h.loadDiscovery(ctx, apiKeyID)
	if err != nil {
		return nil, nil, nil, err
	}
	return tools, sources, toolsearch.Build(toolSearchDocs(tools, sources)), nil
}

func discoveryParts(discoveries []domain.ToolDiscovery) ([]domain.ToolDef, map[string]smartToolSources) {
	tools := make([]domain.ToolDef, 0, len(discoveries))
	sources := make(map[string]smartToolSources, len(discoveries))
	for _, discovery := range discoveries {
		tools = append(tools, discovery.Tool)
		sources[discovery.Tool.Name] = smartToolSources{
			names: sortedUniqueStrings(discovery.UpstreamNames),
			tags:  sortedUniqueStrings(discovery.UpstreamTags),
		}
	}
	return tools, sources
}

func toolSearchDocs(tools []domain.ToolDef, sources map[string]smartToolSources) []toolsearch.Doc {
	docs := make([]toolsearch.Doc, 0, len(tools))
	for _, tool := range tools {
		source := sources[tool.Name]
		docs = append(docs, toolsearch.Doc{
			Name:          tool.Name,
			OriginalName:  tool.OriginalName,
			Description:   tool.Description,
			UpstreamNames: source.names,
			UpstreamTags:  source.tags,
		})
	}
	return docs
}

func exceedsRuneLimit(value string, limit int) bool {
	count := 0
	for range value {
		count++
		if count > limit {
			return true
		}
	}
	return false
}

func upstreamOverview(tools []domain.ToolDef, sources map[string]smartToolSources) []UpstreamSummary {
	counts := make(map[string]int)
	for _, tool := range tools {
		for _, name := range sources[tool.Name].names {
			counts[name]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	overview := make([]UpstreamSummary, 0, len(counts))
	for name, count := range counts {
		overview = append(overview, UpstreamSummary{Name: name, ToolCount: count})
	}
	sort.Slice(overview, func(i, j int) bool {
		if overview[i].ToolCount != overview[j].ToolCount {
			return overview[i].ToolCount > overview[j].ToolCount
		}
		return overview[i].Name < overview[j].Name
	})
	return overview
}

func filterByUpstream(tools []domain.ToolDef, sources map[string]smartToolSources, query string) []domain.ToolDef {
	query = strings.TrimSpace(query)
	matchedExact := func(name string) bool { return strings.EqualFold(name, query) }
	matched := filterToolsBySource(tools, sources, matchedExact)
	if len(matched) > 0 {
		return matched
	}
	return filterToolsBySource(tools, sources, func(name string) bool {
		return strings.Contains(strings.ToLower(name), strings.ToLower(query))
	})
}

func filterToolsBySource(tools []domain.ToolDef, sources map[string]smartToolSources, matches func(string) bool) []domain.ToolDef {
	out := make([]domain.ToolDef, 0)
	for _, tool := range tools {
		for _, name := range sources[tool.Name].names {
			if matches(name) {
				out = append(out, tool)
				break
			}
		}
	}
	return out
}

func sortedUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
