package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

// 本文件实现设计文档「Correctness Properties」中的 Property 11（智能模式工具发现与获取
// 结果正确），针对 SmartModeHandler 的 SearchTools / GetTool / CallTool 进行属性测试。
//
// 被测对象与可见集合来源：
//   - SmartModeHandler 的可见聚合工具集合唯一来源于聚合服务 BuildToolSet；本测试复用
//     smartmode_test.go 中的 smFakeAggregation 作为可控聚合服务，将随机生成的工具集合
//     注入 buildResult，即「当前可见聚合工具集合」。
//   - search_tools / get_tool 完全基于该可见集合在内存中过滤/查找，故以 smFakeAggregation
//     即可对其行为做端到端的黑盒断言。
//   - call_tool 经聚合服务 InvokeTool 路由，可见性由聚合服务保证（真实不向上游转发已由
//     aggregation 包 Property 10 覆盖）；这里断言 SmartModeHandler 对 InvokeTool 的转发
//     与错误透传：可见工具路由成功结果、不可见工具上抛 TOOL_NOT_FOUND。
//
// 为避免与同包其它测试（smFakeAggregation/smTool/smTools 等）的标识符冲突，本文件新增的
// 生成器与辅助统一以 p11 前缀命名。

// p11NamePool 是一组工具名称词元（全部小写）。工具对外名称内嵌这些词元，使「关键字命中
// 名称」有较高概率发生；同时因全部小写，可保证后文构造的不可见名称（带大写前缀）必不与
// 任何可见名称相等。
var p11NamePool = []string{"query", "read", "write", "list", "search", "exec", "status"}

// p11DescPool 是一组描述词元（刻意首字母大写）。工具描述内嵌这些词元，配合可被大小写
// 变换的关键字，用于验证「名称或描述包含关键字」的匹配不区分大小写（Req 11.4）。
var p11DescPool = []string{"Database", "File", "Network", "Memory", "Process", "Config"}

// p11GenVisibleTools 生成「当前可见聚合工具集合」：
//   - 数量 0-12，覆盖空集合（无匹配/无可见工具）到多工具；
//   - 对外名称以序号 i 保证全局唯一（契合聚合管线名称唯一不变量），并内嵌名称词元；
//   - 描述内嵌（大小写混合的）描述词元；
//   - 部分工具携带自定义 inputSchema、部分留空，以覆盖 get_tool 的 schema 透传与默认回退。
func p11GenVisibleTools() *rapid.Generator[[]domain.ToolDef] {
	return rapid.Custom(func(t *rapid.T) []domain.ToolDef {
		n := rapid.IntRange(0, 12).Draw(t, "numTools")
		out := make([]domain.ToolDef, 0, n)
		for i := range n {
			nameTok := rapid.SampledFrom(p11NamePool).Draw(t, fmt.Sprintf("nameTok-%d", i))
			descTok := rapid.SampledFrom(p11DescPool).Draw(t, fmt.Sprintf("descTok-%d", i))
			name := fmt.Sprintf("%s_tool_%d", nameTok, i)
			desc := fmt.Sprintf("%s：第 %d 个工具的说明", descTok, i)

			var schema json.RawMessage
			if rapid.Bool().Draw(t, fmt.Sprintf("hasSchema-%d", i)) {
				schema = json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"p%d":{"type":"string"}}}`, i))
			}
			out = append(out, domain.ToolDef{
				OriginalName: fmt.Sprintf("orig_%d", i),
				Name:         name,
				Description:  desc,
				InputSchema:  schema,
				UpstreamID:   "up",
			})
		}
		return out
	})
}

// p11GenKeyword 生成查询关键字，覆盖：命中名称的词元、命中描述的词元、几乎不命中的词元、
// 空串与全空白（trim 后为空、匹配全部）、任意短串；并随机做大小写变换以验证匹配不区分
// 大小写。
func p11GenKeyword() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		base := rapid.OneOf(
			rapid.SampledFrom(p11NamePool),
			rapid.SampledFrom(p11DescPool),
			rapid.Just("zzz_no_match_token"),
			rapid.Just(""),
			rapid.Just("   "),
			rapid.StringMatching(`[a-zA-Z]{1,5}`),
		).Draw(t, "keywordBase")

		switch rapid.IntRange(0, 2).Draw(t, "keywordCase") {
		case 0:
			return strings.ToUpper(base)
		case 1:
			return strings.ToLower(base)
		default:
			return base
		}
	})
}

// p11EffectiveLimit 独立复刻 SmartModeHandler 的有效返回数推导（NewSmartModeHandler 的
// discoveryLimit 收敛 + resolveLimit），作为属性测试的预期上限来源，避免直接调用实现内部
// 方法导致断言与实现循环依赖。
//
//   - 构造时 discoveryLimit 越界（非 1-200）回退默认 50；本测试只生成 [1,200] 故必不回退；
//   - requested<=0 使用 discoveryLimit；requested>200 收敛到 200；否则取 requested。
func p11EffectiveLimit(discoveryLimit, requested int) int {
	d := discoveryLimit
	if d < minDiscoveryLimit || d > maxDiscoveryLimit {
		d = defaultDiscoveryLimit
	}
	if requested <= 0 {
		return d
	}
	if requested > maxDiscoveryLimit {
		return maxDiscoveryLimit
	}
	return requested
}

// p11Hit 复刻 SmartModeHandler.SearchTools 的命中判定：关键字与名称、描述均经去空白、
// 转小写后做子串包含，命中任一即为真。
func p11Hit(kw string, td domain.ToolDef) bool {
	return strings.Contains(strings.ToLower(td.Name), kw) ||
		strings.Contains(strings.ToLower(td.Description), kw)
}

// Feature: mcp-proxy-gateway, Property 11: 智能模式工具发现与获取结果正确
//
// Validates: Requirements 11.4, 11.5, 11.7
//
// 对任意可见工具集合、查询关键字与返回数（含越界值），断言：
//   - search_tools（Req 11.4、11.5）：
//   - 命中关键字——返回的每个工具其名称或描述（不区分大小写）都包含该关键字；
//   - 数量不超过有效上限——返回条数 ≤ 有效返回数（默认 50、范围 1-200）；
//   - 不漏——按可见集合原序，返回恰为全部命中工具截断至有效上限的前缀（既不漏命中、
//     也不乱序、也不超额）；
//   - 无匹配返回空列表而非错误。
//   - get_tool（Req 11.7）：对集合内任一对外名称返回该工具的完整定义（名称、描述与
//     inputSchema 原样，含默认 schema 回退）；对集合外（不可见）名称返回 TOOL_NOT_FOUND。
//   - call_tool（Req 11.7 配合 10.3）：经聚合服务路由，可见工具透传成功结果，不可见工具
//     上抛 TOOL_NOT_FOUND。
func TestProperty11SmartDiscoveryAndGet(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ctx := context.Background()

		discoveryLimit := rapid.IntRange(1, 200).Draw(t, "discoveryLimit")
		tools := p11GenVisibleTools().Draw(t, "visibleTools")
		rawKeyword := p11GenKeyword().Draw(t, "keyword")
		requested := rapid.OneOf(
			rapid.IntRange(-5, 0),  // 非正：使用配置默认值
			rapid.IntRange(1, 250), // 含合法范围与超界（>200 收敛到 200）
		).Draw(t, "requestedLimit")
		apiKeyID := rapid.SampledFrom([]string{"", "key-1", "key-2"}).Draw(t, "apiKeyID")

		agg := &smFakeAggregation{buildResult: tools}
		h := NewSmartModeHandler(agg, discoveryLimit)

		kw := strings.ToLower(strings.TrimSpace(rawKeyword))
		effLimit := p11EffectiveLimit(discoveryLimit, requested)

		// 独立计算预期命中序列（保序），再截断至有效上限。
		expected := make([]domain.ToolDef, 0, len(tools))
		for _, td := range tools {
			if p11Hit(kw, td) {
				expected = append(expected, td)
			}
		}
		wantLen := min(len(expected), effLimit)

		// ---- search_tools ----
		got, err := h.SearchTools(ctx, apiKeyID, rawKeyword, requested)
		if err != nil {
			t.Fatalf("SearchTools 不应返回错误：%v", err)
		}
		if got == nil {
			t.Fatalf("SearchTools 应返回非 nil 切片")
		}
		// 数量不超过有效上限。
		if len(got) > effLimit {
			t.Fatalf("返回条数超过有效上限：got=%d effLimit=%d", len(got), effLimit)
		}
		// 无匹配返回空列表而非错误。
		if len(expected) == 0 && len(got) != 0 {
			t.Fatalf("无命中应返回空列表，got=%d", len(got))
		}
		// 不漏 + 保序 + 不超额：got 恰为预期命中序列截断至有效上限的前缀。
		if len(got) != wantLen {
			t.Fatalf("返回条数与预期不符：got=%d want=%d (命中总数=%d effLimit=%d kw=%q)",
				len(got), wantLen, len(expected), effLimit, kw)
		}
		for i := range got {
			if got[i].Name != expected[i].Name || got[i].Description != expected[i].Description {
				t.Fatalf("第 %d 条与预期命中不一致：got=%+v want=%+v", i, got[i], expected[i])
			}
			// 命中关键字：返回的每个工具其名称或描述（不区分大小写）都包含关键字。
			if !(strings.Contains(strings.ToLower(got[i].Name), kw) ||
				strings.Contains(strings.ToLower(got[i].Description), kw)) {
				t.Fatalf("返回的工具未命中关键字：kw=%q tool=%+v", kw, got[i])
			}
		}

		// ---- get_tool：可见工具返回完整定义 ----
		for _, td := range tools {
			def, gerr := h.GetTool(ctx, apiKeyID, td.Name)
			if gerr != nil {
				t.Fatalf("可见工具 %q 的 GetTool 不应返回错误：%v", td.Name, gerr)
			}
			if def == nil {
				t.Fatalf("可见工具 %q 的 GetTool 不应返回 nil", td.Name)
			}
			if def.Name != td.Name || def.Description != td.Description {
				t.Fatalf("get_tool 定义不一致：got name=%q desc=%q want name=%q desc=%q",
					def.Name, def.Description, td.Name, td.Description)
			}
			// inputSchema 须与 toMCPTool 的转换一致（含空 schema 时的默认回退）。
			want := toMCPTool(td)
			gotSchema, ok := def.InputSchema.(json.RawMessage)
			if !ok {
				t.Fatalf("get_tool 的 InputSchema 类型应为 json.RawMessage，got %T", def.InputSchema)
			}
			wantSchema, ok := want.InputSchema.(json.RawMessage)
			if !ok {
				t.Fatalf("期望 InputSchema 类型应为 json.RawMessage，got %T", want.InputSchema)
			}
			if string(gotSchema) != string(wantSchema) {
				t.Fatalf("get_tool 的 inputSchema 未原样返回：got=%s want=%s", gotSchema, wantSchema)
			}
		}

		// 构造一个保证不可见的工具名称：大写前缀必不与任何可见名称（全小写/数字/下划线）相等。
		absentName := "ABSENT_" + rapid.StringMatching(`[a-z]{1,6}`).Draw(t, "absentSuffix")

		// ---- get_tool：不可见工具返回 TOOL_NOT_FOUND ----
		def, gerr := h.GetTool(ctx, apiKeyID, absentName)
		if def != nil {
			t.Fatalf("不可见工具不应返回定义，got=%+v", def)
		}
		var getErr *domain.APIError
		if !errors.As(gerr, &getErr) {
			t.Fatalf("不可见工具 get_tool 期望 *domain.APIError，got %T: %v", gerr, gerr)
		}
		if getErr.Code != domain.CodeToolNotFound {
			t.Fatalf("不可见工具 get_tool 期望 TOOL_NOT_FOUND，got %s", getErr.Code)
		}

		// ---- call_tool：可见工具透传成功结果 ----
		if len(tools) > 0 {
			idx := rapid.IntRange(0, len(tools)-1).Draw(t, "callIdx")
			target := tools[idx].Name
			args := json.RawMessage(`{"k":"v"}`)
			agg.invokeErr = nil
			agg.invokeResult = domain.ToolResult{IsError: false, Content: json.RawMessage(`[{"type":"text","text":"ok"}]`)}
			agg.invokeCalled = false

			res, cerr := h.CallTool(ctx, apiKeyID, target, args)
			if cerr != nil {
				t.Fatalf("可见工具 call_tool 不应返回错误：%v", cerr)
			}
			if res == nil {
				t.Fatalf("可见工具 call_tool 应返回结果")
			}
			if !agg.invokeCalled {
				t.Fatalf("call_tool 应路由到聚合服务 InvokeTool")
			}
			if agg.gotInvokeName != target {
				t.Fatalf("call_tool 未透传工具名：got=%q want=%q", agg.gotInvokeName, target)
			}
			if string(agg.gotInvokeArgs) != string(args) {
				t.Fatalf("call_tool 未原样透传参数：got=%s want=%s", agg.gotInvokeArgs, args)
			}
		}

		// ---- call_tool：不可见工具上抛 TOOL_NOT_FOUND ----
		agg.invokeResult = domain.ToolResult{}
		agg.invokeErr = domain.NewError(domain.CodeToolNotFound, "工具不存在于当前可见聚合工具集合中")
		res, cerr := h.CallTool(ctx, apiKeyID, absentName, json.RawMessage(`{}`))
		if res != nil {
			t.Fatalf("不可见工具 call_tool 出错时不应返回结果，got=%+v", res)
		}
		var callErr *domain.APIError
		if !errors.As(cerr, &callErr) {
			t.Fatalf("不可见工具 call_tool 期望 *domain.APIError，got %T: %v", cerr, cerr)
		}
		if callErr.Code != domain.CodeToolNotFound {
			t.Fatalf("不可见工具 call_tool 期望 TOOL_NOT_FOUND，got %s", callErr.Code)
		}
	})
}
