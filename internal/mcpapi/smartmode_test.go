package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/toolsearch"
)

// 本文件为任务 13.2「实现智能模式网关工具 list_tools / search_tools / get_tool / call_tool」
// 的单元测试，使用 fake Aggregation_Service，覆盖四个网关工具的核心行为与边界
// （Req 11.3-11.7 及其依赖的聚合调用语义 Req 10.3、11.7）：
//   - GatewayTools 仅暴露四个网关工具；
//   - list_tools 分页（默认条数、自定义条数、游标翻页、越界、非法游标、空集合）；
//   - search_tools 关键字命中（名称/描述、不区分大小写）、无匹配返回空列表、条数上限、默认条数、越界收敛；
//   - get_tool 命中返回完整定义（含 inputSchema）、不可见返回 TOOL_NOT_FOUND；
//   - call_tool 路由透传成功结果、不可见返回 TOOL_NOT_FOUND 且不发起调用。
//
// fake 与辅助统一以 sm 前缀命名，避免与同包其它测试（如全量模式 fm 前缀）的标识符冲突。

// smFakeAggregation 是 domain.Aggregation_Service 的内存假实现。
//
// buildResult/buildErr 控制 BuildToolSet 返回；invokeResult/invokeErr 控制 InvokeTool
// 返回。同时记录最近一次调用的入参，便于断言「apiKeyID / name / args 透传」与「不可见时
// 不发起调用」。
type smFakeAggregation struct {
	buildResult   []domain.ToolDef
	buildErr      error
	gotBuildKeyID string

	invokeResult   domain.ToolResult
	invokeErr      error
	gotInvokeKeyID string
	gotInvokeName  string
	gotInvokeArgs  json.RawMessage
	invokeCalled   bool
}

func (f *smFakeAggregation) BuildToolSet(_ context.Context, apiKeyID string) ([]domain.ToolDef, error) {
	f.gotBuildKeyID = apiKeyID
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	return f.buildResult, nil
}

func (f *smFakeAggregation) InvokeTool(_ context.Context, apiKeyID, exposedName string, args json.RawMessage) (domain.ToolResult, error) {
	f.invokeCalled = true
	f.gotInvokeKeyID = apiKeyID
	f.gotInvokeName = exposedName
	f.gotInvokeArgs = args
	if f.invokeErr != nil {
		return domain.ToolResult{}, f.invokeErr
	}
	return f.invokeResult, nil
}

// 编译期断言：fake 必须满足 domain.Aggregation_Service 接口契约。
var _ domain.Aggregation_Service = (*smFakeAggregation)(nil)

// smDetailedAggregation adds optional source detail capability used in
// production for Smart-mode disambiguation.
type smDetailedAggregation struct {
	smFakeAggregation
	details     []domain.ToolDetail
	detailErr   error
	detailCalls int
}

func (f *smDetailedAggregation) BuildToolDetails(_ context.Context, apiKeyID string) ([]domain.ToolDetail, error) {
	f.detailCalls++
	f.gotBuildKeyID = apiKeyID
	if f.detailErr != nil {
		return nil, f.detailErr
	}
	return f.details, nil
}

// smTool 构造一个测试用领域工具定义。
func smTool(name, desc string) domain.ToolDef {
	return domain.ToolDef{
		OriginalName: name,
		Name:         name,
		Description:  desc,
		UpstreamID:   "up",
	}
}

// smTools 构造 n 个名称为 tool_0..tool_{n-1} 的测试工具。
func smTools(n int) []domain.ToolDef {
	out := make([]domain.ToolDef, 0, n)
	for i := range n {
		out = append(out, smTool(fmt.Sprintf("tool_%d", i), fmt.Sprintf("第 %d 个工具", i)))
	}
	return out
}

// --- GatewayTools ---

// TestSmartGatewayToolsExposesFour 验证：智能模式仅暴露四个固定网关工具（Req 11.3）。
func TestSmartGatewayToolsExposesFour(t *testing.T) {
	h := NewSmartModeHandler(&smFakeAggregation{}, 50)
	tools := h.GatewayTools()
	if len(tools) != 4 {
		t.Fatalf("智能模式应仅暴露 4 个网关工具，got=%d", len(tools))
	}
	want := map[string]bool{
		GatewayToolListTools:   false,
		GatewayToolSearchTools: false,
		GatewayToolGetTool:     false,
		GatewayToolCallTool:    false,
	}
	for _, tl := range tools {
		if _, ok := want[tl.Name]; !ok {
			t.Fatalf("出现非预期的网关工具：%q", tl.Name)
		}
		want[tl.Name] = true
		if tl.InputSchema == nil {
			t.Fatalf("网关工具 %q 应携带 inputSchema", tl.Name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("缺少网关工具：%q", name)
		}
	}
}

// --- list_tools ---

// TestSmartListToolsDefaultLimit 验证：list_tools 默认使用配置返回数，并正确给出下一页游标。
func TestSmartListToolsDefaultLimit(t *testing.T) {
	agg := &smFakeAggregation{buildResult: smTools(120)}
	h := NewSmartModeHandler(agg, 50)

	page, err := h.ListTools(context.Background(), "key-1", "", "", 0)
	if err != nil {
		t.Fatalf("ListTools 不应返回错误：%v", err)
	}
	if agg.gotBuildKeyID != "key-1" {
		t.Fatalf("apiKeyID 未透传：got=%q", agg.gotBuildKeyID)
	}
	if len(page.Tools) != 50 {
		t.Fatalf("默认应返回 50 条，got=%d", len(page.Tools))
	}
	if page.Tools[0].Name != "tool_0" {
		t.Fatalf("首条应为 tool_0，got=%q", page.Tools[0].Name)
	}
	// 返回的摘要不应携带 schema（仅名称+简述），描述应正确。
	if page.Tools[0].Description != "第 0 个工具" {
		t.Fatalf("摘要描述错误，got=%q", page.Tools[0].Description)
	}
	if page.NextCursor != "50" {
		t.Fatalf("下一页游标应为 50，got=%q", page.NextCursor)
	}
}

// TestSmartListToolsCursorPaging 验证：list_tools 按游标翻页，末页无 NextCursor。
func TestSmartListToolsCursorPaging(t *testing.T) {
	agg := &smFakeAggregation{buildResult: smTools(10)}
	h := NewSmartModeHandler(agg, 50)

	// 第一页：limit=4 → tool_0..tool_3，游标 4。
	page1, err := h.ListTools(context.Background(), "", "", "", 4)
	if err != nil {
		t.Fatalf("第一页出错：%v", err)
	}
	if len(page1.Tools) != 4 || page1.Tools[0].Name != "tool_0" || page1.NextCursor != "4" {
		t.Fatalf("第一页结果异常：len=%d first=%q cursor=%q", len(page1.Tools), page1.Tools[0].Name, page1.NextCursor)
	}

	// 第二页：cursor=4 limit=4 → tool_4..tool_7，游标 8。
	page2, err := h.ListTools(context.Background(), "", page1.NextCursor, "", 4)
	if err != nil {
		t.Fatalf("第二页出错：%v", err)
	}
	if len(page2.Tools) != 4 || page2.Tools[0].Name != "tool_4" || page2.NextCursor != "8" {
		t.Fatalf("第二页结果异常：len=%d first=%q cursor=%q", len(page2.Tools), page2.Tools[0].Name, page2.NextCursor)
	}

	// 第三页：cursor=8 limit=4 → tool_8..tool_9（仅 2 条），无下一页游标。
	page3, err := h.ListTools(context.Background(), "", page2.NextCursor, "", 4)
	if err != nil {
		t.Fatalf("第三页出错：%v", err)
	}
	if len(page3.Tools) != 2 || page3.Tools[0].Name != "tool_8" {
		t.Fatalf("第三页结果异常：len=%d first=%q", len(page3.Tools), page3.Tools[0].Name)
	}
	if page3.NextCursor != "" {
		t.Fatalf("末页不应有下一页游标，got=%q", page3.NextCursor)
	}
}

// TestSmartListToolsEmpty 验证：可见集合为空时返回空页（非 nil）且无游标。
func TestSmartListToolsEmpty(t *testing.T) {
	agg := &smFakeAggregation{buildResult: nil}
	h := NewSmartModeHandler(agg, 50)

	page, err := h.ListTools(context.Background(), "", "", "", 0)
	if err != nil {
		t.Fatalf("空集合不应返回错误：%v", err)
	}
	if page.Tools == nil {
		t.Fatalf("应返回非 nil 空切片")
	}
	if len(page.Tools) != 0 {
		t.Fatalf("应返回空列表，got=%d", len(page.Tools))
	}
	if page.NextCursor != "" {
		t.Fatalf("空集合不应有游标，got=%q", page.NextCursor)
	}
}

// TestSmartListToolsOffsetBeyondEnd 验证：游标超过总数时返回空页而非错误。
func TestSmartListToolsOffsetBeyondEnd(t *testing.T) {
	agg := &smFakeAggregation{buildResult: smTools(3)}
	h := NewSmartModeHandler(agg, 50)

	page, err := h.ListTools(context.Background(), "", "100", "", 10)
	if err != nil {
		t.Fatalf("越界游标不应返回错误：%v", err)
	}
	if len(page.Tools) != 0 || page.NextCursor != "" {
		t.Fatalf("越界游标应返回空页且无游标：len=%d cursor=%q", len(page.Tools), page.NextCursor)
	}
}

// TestSmartListToolsInvalidCursor 验证：非法游标返回字段级校验错误。
func TestSmartListToolsInvalidCursor(t *testing.T) {
	agg := &smFakeAggregation{buildResult: smTools(3)}
	h := NewSmartModeHandler(agg, 50)

	for _, bad := range []string{"abc", "-1", "1.5", strings.Repeat("1", maxCursorBytes+1)} {
		_, err := h.ListTools(context.Background(), "", bad, "", 0)
		var apiErr *domain.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("游标 %q 应返回 *domain.APIError，got %T: %v", bad, err, err)
		}
		if apiErr.Code != domain.CodeValidation {
			t.Fatalf("游标 %q 应返回 VALIDATION，got %s", bad, apiErr.Code)
		}
	}
}

func TestSmartListToolsProvidesAndFiltersUpstreamOverview(t *testing.T) {
	pveTool := smTool("vm_list", "列出虚拟机")
	pveTool.SourceCount = 1
	githubTool := smTool("create_pull_request", "创建拉取请求")
	githubTool.SourceCount = 1
	agg := &smDetailedAggregation{details: []domain.ToolDetail{
		{Tool: pveTool, Sources: []domain.ToolSourceView{{UpstreamName: "PVE 生产集群", UpstreamTags: []string{"virtualization"}}}},
		{Tool: githubTool, Sources: []domain.ToolSourceView{{UpstreamName: "GitHub", UpstreamTags: []string{"code"}}}},
	}}
	h := NewSmartModeHandler(agg, 50)

	page, err := h.ListTools(context.Background(), "key-1", "", "pve", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tools) != 1 || page.Tools[0].Name != "vm_list" {
		t.Fatalf("upstream substring filter failed: %+v", page)
	}
	if len(page.Upstreams) != 2 || page.Upstreams[0].Name != "GitHub" || page.Upstreams[1].Name != "PVE 生产集群" {
		t.Fatalf("first page should include deterministic overview: %+v", page.Upstreams)
	}
	page, err = h.ListTools(context.Background(), "key-1", "1", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Upstreams) != 0 {
		t.Fatalf("non-first page must not repeat overview: %+v", page.Upstreams)
	}
}

func TestSmartDiscoveryFallsBackWhenOptionalSourceDetailsFail(t *testing.T) {
	agg := &smDetailedAggregation{
		smFakeAggregation: smFakeAggregation{buildResult: []domain.ToolDef{smTool("pg_query", "查询")}},
		detailErr:         errors.New("tool policy store unavailable"),
	}
	page, err := NewSmartModeHandler(agg, 50).ListTools(context.Background(), "", "", "", 50)
	if err != nil {
		t.Fatalf("optional source enrichment failure must not block discovery: %v", err)
	}
	if len(page.Tools) != 1 || page.Tools[0].Upstream != "" {
		t.Fatalf("fallback should retain tools without source metadata: %+v", page)
	}
}

// --- search_tools ---

// TestSmartSearchToolsMatchesNameAndDescription 验证：名称或描述命中关键字均被返回，
// 且匹配不区分大小写（Req 11.4）。
func TestSmartSearchToolsMatchesNameAndDescription(t *testing.T) {
	agg := &smFakeAggregation{buildResult: []domain.ToolDef{
		smTool("pg_query", "在 PostgreSQL 中执行查询"),
		smTool("fs_read", "读取文件内容"),
		smTool("db_status", "查看 Database 状态"), // 描述含 Database，关键字 database 应命中（不区分大小写）
	}}
	h := NewSmartModeHandler(agg, 50)

	// 关键字命中名称：query 命中 pg_query。
	got, err := h.SearchTools(context.Background(), "", "query", "", 0)
	if err != nil {
		t.Fatalf("SearchTools 出错：%v", err)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "pg_query" {
		t.Fatalf("关键字 query 应仅命中 pg_query，got=%v", got)
	}

	// 关键字命中描述且不区分大小写：database 命中 db_status 的描述。
	got2, err := h.SearchTools(context.Background(), "", "database", "", 0)
	if err != nil {
		t.Fatalf("SearchTools 出错：%v", err)
	}
	if len(got2.Tools) != 1 || got2.Tools[0].Name != "db_status" {
		t.Fatalf("关键字 database 应命中 db_status，got=%v", got2)
	}
}

// TestSmartSearchToolsNoMatchReturnsEmpty 验证：无匹配时返回空列表而非错误（Req 11.5）。
func TestSmartSearchToolsNoMatchReturnsEmpty(t *testing.T) {
	agg := &smFakeAggregation{buildResult: smTools(5)}
	h := NewSmartModeHandler(agg, 50)

	got, err := h.SearchTools(context.Background(), "", "不存在的关键字", "", 0)
	if err != nil {
		t.Fatalf("无匹配不应返回错误：%v", err)
	}
	if got.Tools == nil || got.Suggestions == nil {
		t.Fatalf("应返回非 nil 空切片")
	}
	if len(got.Tools) != 0 {
		t.Fatalf("无匹配应返回空列表，got=%d", len(got.Tools))
	}
}

// TestSmartSearchToolsRespectsLimit 验证：命中数超过返回数上限时截断。
func TestSmartSearchToolsRespectsLimit(t *testing.T) {
	// 100 个工具名称均含 tool 子串，关键字 tool 全部命中。
	agg := &smFakeAggregation{buildResult: smTools(100)}
	h := NewSmartModeHandler(agg, 50)

	got, err := h.SearchTools(context.Background(), "", "tool", "", 10)
	if err != nil {
		t.Fatalf("SearchTools 出错：%v", err)
	}
	if len(got.Tools) != 10 {
		t.Fatalf("limit=10 应仅返回 10 条，got=%d", len(got.Tools))
	}
}

// TestSmartSearchToolsDefaultLimit 验证：limit<=0 时使用配置默认返回数。
func TestSmartSearchToolsDefaultLimit(t *testing.T) {
	agg := &smFakeAggregation{buildResult: smTools(100)}
	h := NewSmartModeHandler(agg, 50)

	got, err := h.SearchTools(context.Background(), "", "tool", "", 0)
	if err != nil {
		t.Fatalf("SearchTools 出错：%v", err)
	}
	if len(got.Tools) != 50 {
		t.Fatalf("默认应返回 50 条，got=%d", len(got.Tools))
	}
}

// TestSmartSearchToolsLimitClampedToMax 验证：limit 超过 200 时收敛到 200。
func TestSmartSearchToolsLimitClampedToMax(t *testing.T) {
	agg := &smFakeAggregation{buildResult: smTools(250)}
	h := NewSmartModeHandler(agg, 50)

	got, err := h.SearchTools(context.Background(), "", "tool", "", 1000)
	if err != nil {
		t.Fatalf("SearchTools 出错：%v", err)
	}
	if len(got.Tools) != 200 {
		t.Fatalf("limit 超界应收敛到 200，got=%d", len(got.Tools))
	}
}

// TestSmartSearchToolsEmptyQueryReturnsGuidance 验证：空关键字不再伪装成工具浏览。
func TestSmartSearchToolsEmptyQueryReturnsGuidance(t *testing.T) {
	agg := &smFakeAggregation{buildResult: smTools(5)}
	h := NewSmartModeHandler(agg, 50)

	got, err := h.SearchTools(context.Background(), "", "   ", "", 0)
	if err != nil {
		t.Fatalf("SearchTools 出错：%v", err)
	}
	if len(got.Tools) != 0 || got.Hint == "" {
		t.Fatalf("空关键字应返回空列表和引导，got=%+v", got)
	}
}

func TestSmartSearchToolsRejectsOversizedQueryBeforeLoadingTools(t *testing.T) {
	agg := &smFakeAggregation{buildResult: smTools(1)}
	h := NewSmartModeHandler(agg, 50)
	_, err := h.SearchTools(context.Background(), "key-1", strings.Repeat("查", toolsearch.MaxQueryRunes+1), "", 10)
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeValidation {
		t.Fatalf("oversized query should return VALIDATION, got %T: %v", err, err)
	}
	if agg.gotBuildKeyID != "" {
		t.Fatalf("oversized query must be rejected before aggregate loading, got key=%q", agg.gotBuildKeyID)
	}
}

func TestSmartSearchToolsUsesSourceMetadataAndPaging(t *testing.T) {
	first := smTool("create_pull_request", "创建拉取请求")
	first.SourceCount = 2
	first.SchemaConflict = true
	second := smTool("create_issue", "创建问题")
	agg := &smDetailedAggregation{details: []domain.ToolDetail{
		{Tool: first, Sources: []domain.ToolSourceView{{UpstreamName: "GitHub", UpstreamTags: []string{"code"}}, {UpstreamName: "GitHub Mirror", UpstreamTags: []string{"backup"}}}},
		{Tool: second, Sources: []domain.ToolSourceView{{UpstreamName: "GitHub", UpstreamTags: []string{"code"}}}},
	}}
	h := NewSmartModeHandler(agg, 50)

	page, err := h.SearchTools(context.Background(), "", "github create pr", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tools) != 1 || page.Tools[0].Name != "create_pull_request" || page.NextCursor != "1" {
		t.Fatalf("source-aware search/paging failed: %+v", page)
	}
	if page.Tools[0].Upstream != "GitHub, GitHub Mirror" || page.Tools[0].SourceCount != 2 || !page.Tools[0].SchemaConflict {
		t.Fatalf("result lacks disambiguation metadata: %+v", page.Tools[0])
	}
	if len([]rune(truncateDescription(string(make([]rune, 241)), 240))) != 241 {
		t.Fatalf("truncated description should retain 240 runes plus ellipsis")
	}
}

// --- get_tool ---

// TestSmartGetToolReturnsFullDefinition 验证：get_tool 命中返回含 inputSchema 的完整定义（Req 11.7）。
func TestSmartGetToolReturnsFullDefinition(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`)
	agg := &smFakeAggregation{buildResult: []domain.ToolDef{
		{OriginalName: "query", Name: "pg_query", Description: "执行查询", InputSchema: schema, UpstreamID: "up"},
	}}
	h := NewSmartModeHandler(agg, 50)

	tool, err := h.GetTool(context.Background(), "key-9", "pg_query")
	if err != nil {
		t.Fatalf("GetTool 不应返回错误：%v", err)
	}
	if agg.gotBuildKeyID != "key-9" {
		t.Fatalf("apiKeyID 未透传：got=%q", agg.gotBuildKeyID)
	}
	if tool.Name != "pg_query" || tool.Description != "执行查询" {
		t.Fatalf("工具定义转换错误：name=%q desc=%q", tool.Name, tool.Description)
	}
	gotSchema, ok := tool.InputSchema.(json.RawMessage)
	if !ok {
		t.Fatalf("InputSchema 类型应为 json.RawMessage，got %T", tool.InputSchema)
	}
	if string(gotSchema) != string(schema) {
		t.Fatalf("inputSchema 未原样返回：got=%s", gotSchema)
	}
}

func TestSmartGetToolSkipsOptionalDetailEnrichment(t *testing.T) {
	agg := &smDetailedAggregation{
		smFakeAggregation: smFakeAggregation{buildResult: []domain.ToolDef{smTool("pg_query", "查询")}},
		details:           []domain.ToolDetail{{Tool: smTool("pg_query", "查询")}},
	}
	tool, err := NewSmartModeHandler(agg, 50).GetTool(context.Background(), "", "pg_query")
	if err != nil || tool == nil {
		t.Fatalf("GetTool should use the authorized aggregate: tool=%+v err=%v", tool, err)
	}
	if agg.detailCalls != 0 {
		t.Fatalf("GetTool must not read optional details, calls=%d", agg.detailCalls)
	}
}

// TestSmartGetToolNotVisible 验证：不可见工具返回 TOOL_NOT_FOUND（Req 11.7）。
func TestSmartGetToolNotVisible(t *testing.T) {
	agg := &smFakeAggregation{buildResult: smTools(3)}
	h := NewSmartModeHandler(agg, 50)

	tool, err := h.GetTool(context.Background(), "", "ghost_tool")
	if tool != nil {
		t.Fatalf("不可见工具不应返回定义，got=%+v", tool)
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望 *domain.APIError，got %T: %v", err, err)
	}
	if apiErr.Code != domain.CodeToolNotFound {
		t.Fatalf("期望 TOOL_NOT_FOUND，got %s", apiErr.Code)
	}
}

func TestSmartGetToolsReturnsVisibleItemsAndRejectsOversizedBatch(t *testing.T) {
	agg := &smFakeAggregation{buildResult: []domain.ToolDef{smTool("one", "first"), smTool("two", "second")}}
	h := NewSmartModeHandler(agg, 50)
	batch, err := h.GetTools(context.Background(), "", []string{"one", "missing", "two"})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Tools) != 2 || batch.Tools[0].Name != "one" || len(batch.NotFound) != 1 || batch.NotFound[0] != "missing" {
		t.Fatalf("batch result unexpected: %+v", batch)
	}
	names := make([]string, maxBatchToolNames+1)
	for i := range names {
		names[i] = fmt.Sprintf("missing_%d", i)
	}
	batch, err = h.GetTools(context.Background(), "", names)
	if batch.Tools != nil || batch.NotFound != nil {
		t.Fatalf("oversized batch must not return a partial response: %+v", batch)
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeValidation {
		t.Fatalf("oversized batch should return VALIDATION, got %T: %v", err, err)
	}
	for _, invalid := range [][]string{nil, {}, {"   "}} {
		_, err = h.GetTools(context.Background(), "", invalid)
		if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeValidation {
			t.Fatalf("invalid batch %q should return VALIDATION, got %T: %v", invalid, err, err)
		}
	}
}

func TestGatewayHandlersReturnExtendedSearchAndBatchShapes(t *testing.T) {
	agg := &smFakeAggregation{buildResult: []domain.ToolDef{smTool("pg_query", "执行 PostgreSQL 查询")}}
	svc := NewService(agg, 50, nil)
	h := NewSmartModeHandler(agg, 50)

	result, err := svc.handleGatewaySearchTools(context.Background(), "", json.RawMessage(`{"query":"pg query"}`), h)
	if err != nil {
		t.Fatal(err)
	}
	var page SearchPage
	if err := json.Unmarshal([]byte(gatewayResultText(t, result)), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Tools) != 1 || page.Tools[0].Name != "pg_query" || page.Suggestions == nil {
		t.Fatalf("search response shape unexpected: %+v", page)
	}

	result, err = svc.handleGatewayGetTool(context.Background(), "", json.RawMessage(`{"names":["pg_query","missing"]}`), h)
	if err != nil {
		t.Fatal(err)
	}
	var batch ToolBatch
	if err := json.Unmarshal([]byte(gatewayResultText(t, result)), &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Tools) != 1 || batch.Tools[0].Name != "pg_query" || len(batch.NotFound) != 1 || batch.NotFound[0] != "missing" {
		t.Fatalf("batch response shape unexpected: %+v", batch)
	}
	_, err = svc.handleGatewayGetTool(context.Background(), "", json.RawMessage(`{"names":["a","b","c","d","e","f","g","h","i","j","k","l","m","n","o","p","q","r","s","t","u"]}`), h)
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeValidation {
		t.Fatalf("oversized gateway batch should return VALIDATION, got %T: %v", err, err)
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"name":"pg_query","names":["pg_query"]}`),
		json.RawMessage(`{"names":[]}`),
		json.RawMessage(`{"names":[""]}`),
	} {
		_, err = svc.handleGatewayGetTool(context.Background(), "", raw, h)
		if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeValidation {
			t.Fatalf("invalid get_tool payload %s should return VALIDATION, got %T: %v", raw, err, err)
		}
	}
}

func TestGatewayHandlersValidateRequiredAndObjectArguments(t *testing.T) {
	agg := &smFakeAggregation{buildResult: []domain.ToolDef{smTool("pg_query", "执行 PostgreSQL 查询")}}
	svc := NewService(agg, 50, nil)
	h := NewSmartModeHandler(agg, 50)

	for _, raw := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`null`)} {
		_, err := svc.handleGatewaySearchTools(context.Background(), "", raw, h)
		var apiErr *domain.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeValidation {
			t.Fatalf("invalid search payload %s should return VALIDATION, got %T: %v", raw, err, err)
		}
	}

	for _, raw := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"name":"   "}`),
		json.RawMessage(`{"name":"pg_query","arguments":null}`),
		json.RawMessage(`{"name":"pg_query","arguments":[]}`),
	} {
		_, err := svc.handleGatewayCallTool(context.Background(), "", raw, h)
		var apiErr *domain.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeValidation {
			t.Fatalf("invalid call payload %s should return VALIDATION, got %T: %v", raw, err, err)
		}
	}
}

func gatewayResultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil || len(result.Content) != 1 {
		t.Fatalf("expected one gateway text result, got %+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", result.Content[0])
	}
	return text.Text
}

// --- call_tool ---

// TestSmartCallToolRoutesThrough 验证：call_tool 把 apiKeyID/name/原始 args 原样透传给
// InvokeTool，并将上游结果原样转换返回（Req 11.6、10.3）。
func TestSmartCallToolRoutesThrough(t *testing.T) {
	wantContent := json.RawMessage(`[{"type":"text","text":"ok"}]`)
	agg := &smFakeAggregation{invokeResult: domain.ToolResult{IsError: false, Content: wantContent}}
	h := NewSmartModeHandler(agg, 50)

	args := json.RawMessage(`{"sql":"select 1"}`)
	res, err := h.CallTool(context.Background(), "key-2", "pg_query", args)
	if err != nil {
		t.Fatalf("CallTool 不应返回错误：%v", err)
	}
	if !agg.invokeCalled {
		t.Fatalf("CallTool 应路由到聚合服务 InvokeTool")
	}
	if agg.gotInvokeKeyID != "key-2" || agg.gotInvokeName != "pg_query" {
		t.Fatalf("apiKeyID/name 未透传：keyID=%q name=%q", agg.gotInvokeKeyID, agg.gotInvokeName)
	}
	if string(agg.gotInvokeArgs) != string(args) {
		t.Fatalf("原始参数未原样透传：got=%s want=%s", agg.gotInvokeArgs, args)
	}
	if res.IsError {
		t.Fatalf("成功结果不应标记为错误")
	}
}

// TestSmartCallToolNotVisibleNoInvoke 验证：工具不可见时返回 TOOL_NOT_FOUND，
// 且依赖聚合服务保证不向上游发起调用（Req 11.7）。
//
// 这里 fake 的 InvokeTool 直接返回 TOOL_NOT_FOUND（模拟聚合服务可见性校验拒绝），
// 断言 CallTool 上抛该错误且不返回结果。真实「不向上游转发」由聚合服务 InvokeTool 保证，
// 已在 aggregation 包属性测试（Property 10）覆盖。
func TestSmartCallToolNotVisibleNoInvoke(t *testing.T) {
	notFound := domain.NewError(domain.CodeToolNotFound, "工具不存在于当前可见聚合工具集合中")
	agg := &smFakeAggregation{invokeErr: notFound}
	h := NewSmartModeHandler(agg, 50)

	res, err := h.CallTool(context.Background(), "", "ghost_tool", json.RawMessage(`{}`))
	if res != nil {
		t.Fatalf("不可见工具出错时不应返回结果，got=%+v", res)
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望 *domain.APIError，got %T: %v", err, err)
	}
	if apiErr.Code != domain.CodeToolNotFound {
		t.Fatalf("期望 TOOL_NOT_FOUND，got %s", apiErr.Code)
	}
}

// --- 构造器边界 ---

// TestSmartNewHandlerClampsDiscoveryLimit 验证：构造时越界 discoveryLimit 回退默认值 50。
func TestSmartNewHandlerClampsDiscoveryLimit(t *testing.T) {
	agg := &smFakeAggregation{buildResult: smTools(100)}

	for _, bad := range []int{0, -5, 201, 1000} {
		h := NewSmartModeHandler(agg, bad)
		got, err := h.SearchTools(context.Background(), "", "tool", "", 0)
		if err != nil {
			t.Fatalf("SearchTools 出错：%v", err)
		}
		if len(got.Tools) != 50 {
			t.Fatalf("越界 discoveryLimit=%d 应回退默认 50，实际默认返回数=%d", bad, len(got.Tools))
		}
	}
}

// --- 错误透传 ---

// TestSmartBuildToolSetErrorPropagates 验证：BuildToolSet 失败时各发现型网关工具原样上抛错误。
func TestSmartBuildToolSetErrorPropagates(t *testing.T) {
	wantErr := domain.NewError(domain.CodeUpstreamUnavailable, "缓存读取失败")
	agg := &smFakeAggregation{buildErr: wantErr}
	h := NewSmartModeHandler(agg, 50)

	if _, err := h.ListTools(context.Background(), "", "", "", 0); !errors.Is(err, wantErr) {
		t.Fatalf("list_tools 应原样上抛聚合错误，got=%v", err)
	}
	if _, err := h.SearchTools(context.Background(), "", "x", "", 0); !errors.Is(err, wantErr) {
		t.Fatalf("search_tools 应原样上抛聚合错误，got=%v", err)
	}
	if _, err := h.GetTool(context.Background(), "", "x"); !errors.Is(err, wantErr) {
		t.Fatalf("get_tool 应原样上抛聚合错误，got=%v", err)
	}
}
