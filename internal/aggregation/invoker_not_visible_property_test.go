package aggregation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

// p10FatalReporter 抽象出断言所需的最小报告能力，使同一断言逻辑既能用于属性测试的
// *rapid.T，也能用于示例测试的 *testing.T（二者均实现 Helper 与 Fatalf）。
// 同包已有的 assertToolNotFound 仅接受 *testing.T，无法在 rapid.Check 内复用，故此处补充。
type p10FatalReporter interface {
	Helper()
	Fatalf(format string, args ...any)
}

// p10AssertToolNotFound 断言 err 是错误码为 TOOL_NOT_FOUND 的 *domain.APIError。
func p10AssertToolNotFound(tb p10FatalReporter, err error) {
	tb.Helper()
	if err == nil {
		tb.Fatalf("期望返回 TOOL_NOT_FOUND 错误，但 err 为 nil")
		return
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		tb.Fatalf("期望 *domain.APIError，got %T: %v", err, err)
		return
	}
	if apiErr.Code != domain.CodeToolNotFound {
		tb.Fatalf("期望错误码 %s，got %s", domain.CodeToolNotFound, apiErr.Code)
	}
}

// 本文件实现设计文档「Correctness Properties」中的 Property 10（不可见工具调用必被拒），
// 针对聚合服务 Service.InvokeTool 进行属性测试。
//
// 属性陈述：对任意不属于当前可见聚合工具集合的工具名称，调用请求返回工具不存在错误
//（domain.CodeToolNotFound），且不向任何上游 MCP 转发该调用（Req 10.4、11.7）。
//
// 为什么直接测试 Service.InvokeTool 而非纯函数管线：
//   - 「不可见即拒绝且不转发」这一行为发生在 InvokeTool 的可见性校验环节——它先以
//     apiKeyID 视角构建可见集合及反向映射，再判断 exposedName 是否在其中。只有完整经过
//     Service（缓存读取 → 管线 → 反向映射 → 路由判定）才能真实验证该端到端行为。
//   - 通过注入的 invRecordingInvoker 观测 called 标志，可断言「未发生任何上游转发」这一
//     不变量；若 InvokeTool 误把不可见工具放行转发，invoker.called 会变为 true 而被捕获。
//
// 测试方法（关键点：先求出真实可见集合，再构造一个保证不在其中的工具名）：
//  1. 随机生成若干上游（含启用/停用、排序、工具列表、别名规则、MCP 级屏蔽规则），并可选
//     地以某 API Key 视角施加 API Key 级屏蔽规则。
//  2. 用 BuildToolSet(apiKeyID) 求出该视角下真实的可见对外名称集合（被测实现自身的口径）。
//  3. 抽取一个候选名称，若它恰好落在可见集合内，则确定性地追加哨兵后缀直至其必然不在
//     可见集合中——从而得到「任意不可见名称」。该构造覆盖空可见集合情形（无上游/全部停用/
//     工具为空/全部被屏蔽时可见集合为空，任何名称都不可见）。
//  4. 以该不可见名称调用 InvokeTool，断言返回 TOOL_NOT_FOUND 且 invoker 未被调用。
//
// 文件内所有生成器与辅助函数均使用 p10 前缀命名，以避免与同包内其它聚合属性测试文件
//（Property 1 的 agg*、Property 2 的 p2*、Property 7 的 p7*、Property 8 的 p8*、Property 9
// 的 p9*）及 invoker 单元测试（inv*）中的标识符发生冲突；同包已有的 fake 实现与辅助函数
//（invFakeCache、invFakeUpstreams、invFakeAliases、invFakeMCPFilters、invFakeAPIKeyFilters、
// invRecordingInvoker、invNewService、assertToolNotFound 等）直接复用。

// p10NamePool 是工具 OriginalName、非正则别名/屏蔽模式与候选名称共享的小名称池。
// 收窄取值空间可显著提高「候选名称与可见工具同名」的概率，从而充分锻炼可见性校验
// 在「形似但需经构造才不可见」边界上的正确性。集合刻意不含 "__"，以免与去重后缀混淆。
var p10NamePool = []string{
	"read", "write", "search", "list_dir",
	"alpha", "beta", "gamma", "exec", "query",
}

// p10RegexPool 是一组「单独合法」的正则模式，与 p10NamePool 有交集，使正则完整匹配
// 能命中部分工具名，丰富别名重写与屏蔽过滤的输入多样性。
var p10RegexPool = []string{
	".*", "a.*", "[a-z_]+", "(alpha|beta)", "read.*", "search", "[a-z]{1,4}",
}

// p10TargetNames 是别名规则可用的目标名称（空串表示不覆盖对外名称）。
// 别名重写会改变可见集合中的对外名称，进而影响「哪些名称不可见」，故纳入生成。
var p10TargetNames = []string{"", "renamed", "fs_read", "exposed_tool"}

// p10GenTool 生成单个工具：OriginalName 取自共享名称池以利于与规则命中。
// 不设置 UpstreamID/Order，二者由管线按所属上游统一规范化覆盖。
func p10GenTool() *rapid.Generator[domain.ToolDef] {
	return rapid.Custom(func(t *rapid.T) domain.ToolDef {
		name := rapid.SampledFrom(p10NamePool).Draw(t, "originalName")
		return domain.ToolDef{
			OriginalName: name,
			Name:         name,
			Description:  "原始描述",
			InputSchema:  []byte("{}"),
		}
	})
}

// p10GenFilter 生成单条屏蔽规则（pattern、isRegex、enabled），同时用于 MCP 级与
// API Key 级屏蔽规则的生成：正则模式取自合法正则池或任意字符串（可能非法，锻炼
// 管线对非法正则的防御性兜底）；非正则模式取自名称池（易命中）或任意字符串。
func p10GenFilter() *rapid.Generator[domain.FilterRule] {
	return rapid.Custom(func(t *rapid.T) domain.FilterRule {
		isRegex := rapid.Bool().Draw(t, "filterIsRegex")
		var pattern string
		if isRegex {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p10RegexPool),
				rapid.String(),
			).Draw(t, "filterRegexPattern")
		} else {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p10NamePool),
				rapid.String(),
			).Draw(t, "filterExactPattern")
		}
		return domain.FilterRule{
			Pattern: pattern,
			IsRegex: isRegex,
			Enabled: rapid.Bool().Draw(t, "filterEnabled"),
		}
	})
}

// p10GenAlias 生成单条别名规则。模式取值与屏蔽规则同源；目标名称可空可非空，
// SortOrder 取较小区间以制造并列。
func p10GenAlias() *rapid.Generator[domain.AliasRule] {
	return rapid.Custom(func(t *rapid.T) domain.AliasRule {
		isRegex := rapid.Bool().Draw(t, "aliasIsRegex")
		var pattern string
		if isRegex {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p10RegexPool),
				rapid.String(),
			).Draw(t, "aliasRegexPattern")
		} else {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p10NamePool),
				rapid.String(),
			).Draw(t, "aliasExactPattern")
		}
		return domain.AliasRule{
			Pattern:    pattern,
			IsRegex:    isRegex,
			TargetName: rapid.SampledFrom(p10TargetNames).Draw(t, "aliasTargetName"),
			SortOrder:  rapid.IntRange(0, 4).Draw(t, "aliasSortOrder"),
		}
	})
}

// p10GenCandidateName 生成候选工具名称：以较高概率取自名称池或别名目标名（更可能与
// 可见工具同名，从而触发后续的「构造为不可见」逻辑），并混入任意字符串以扩大取值空间。
func p10GenCandidateName() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.SampledFrom(p10NamePool),
		rapid.SampledFrom(p10TargetNames),
		rapid.String(),
	)
}

// Feature: mcp-proxy-gateway, Property 10: 不可见工具调用必被拒
//
// Validates: Requirements 10.4, 11.7
//
// 对任意上游集合（含启用/停用、工具列表、别名规则、MCP 级屏蔽规则）与任意 API Key 视角
// （无 API Key 或带 API Key 级屏蔽规则），先以 BuildToolSet 求出该视角真实的可见对外名称
// 集合，再构造一个保证不在该集合内的工具名称，调用 Service.InvokeTool 满足：
//   - 返回错误码为 TOOL_NOT_FOUND 的 *domain.APIError（Req 10.4、11.7）。
//   - 注入的 invoker 全程未被调用，即不向任何上游 MCP 转发该调用（Req 10.4、11.7）。
//
// 该属性天然覆盖空可见集合情形：当无启用上游、工具列表为空或全部被屏蔽时可见集合为空，
// 任何名称均不可见，调用都应被拒且不转发。
func TestProperty10InvokeUnknownToolRejected(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ctx := context.Background()

		// 生成 0 至 3 个上游：0 个或全部停用/工具为空时即覆盖空可见集合情形。
		n := rapid.IntRange(0, 3).Draw(t, "numUpstreams")
		cacheTools := make(map[string][]domain.ToolDef, n)
		ups := make([]domain.Upstream, 0, n)
		aliasesByUp := make(map[string][]domain.AliasRule, n)
		mcpFiltersByUp := make(map[string][]domain.FilterRule, n)

		for i := 0; i < n; i++ {
			id := fmt.Sprintf("u%d", i)
			cacheTools[id] = rapid.SliceOfN(p10GenTool(), 0, 4).Draw(t, fmt.Sprintf("tools_%d", i))
			ups = append(ups, domain.Upstream{
				ID: id,
				Config: domain.UpstreamConfig{
					Name:      id,
					Enabled:   rapid.Bool().Draw(t, fmt.Sprintf("enabled_%d", i)),
					SortOrder: rapid.IntRange(0, 5).Draw(t, fmt.Sprintf("sortOrder_%d", i)),
				},
				State: domain.ConnAvailable,
			})
			aliasesByUp[id] = rapid.SliceOfN(p10GenAlias(), 0, 3).Draw(t, fmt.Sprintf("aliases_%d", i))
			mcpFiltersByUp[id] = rapid.SliceOfN(p10GenFilter(), 0, 3).Draw(t, fmt.Sprintf("filters_%d", i))
		}

		// 可选地以某 API Key 视角施加 API Key 级屏蔽规则，进一步收窄可见集合。
		apiKeyID := ""
		apiKeyFiltersByKey := make(map[string][]domain.FilterRule)
		if rapid.Bool().Draw(t, "useAPIKey") {
			apiKeyID = "key-1"
			apiKeyFiltersByKey["key-1"] = rapid.SliceOfN(p10GenFilter(), 0, 4).Draw(t, "apiKeyFilters")
		}

		cache := &invFakeCache{tools: cacheTools}
		upstreams := &invFakeUpstreams{upstreams: ups}
		aliases := &invFakeAliases{byUpstream: aliasesByUp}
		mcpFilters := &invFakeMCPFilters{byUpstream: mcpFiltersByUp}
		apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: apiKeyFiltersByKey}

		// 注入记录型 invoker：一旦发生转发即被记录，用于断言「不转发」不变量。
		invoker := &invRecordingInvoker{returnResult: domain.ToolResult{Content: json.RawMessage(`[]`)}}
		svc := invNewService(cache, upstreams, aliases, mcpFilters, apiKeyFilters).SetInvoker(invoker)

		// 以同一 API Key 视角求出真实可见对外名称集合（被测实现自身的口径）。
		visible, err := svc.BuildToolSet(ctx, apiKeyID)
		if err != nil {
			t.Fatalf("构建可见集合不应返回错误：%v", err)
		}
		visibleNames := make(map[string]struct{}, len(visible))
		for _, tool := range visible {
			visibleNames[tool.Name] = struct{}{}
		}

		// 抽取候选名并构造为「保证不在可见集合内」：可见集合有限，追加哨兵后缀必然收敛。
		candidate := p10GenCandidateName().Draw(t, "candidate")
		for {
			if _, ok := visibleNames[candidate]; !ok {
				break
			}
			candidate += "__p10_absent"
		}

		// 以不可见名称调用：必返回 TOOL_NOT_FOUND 且不向任何上游转发。
		_, err = svc.InvokeTool(ctx, apiKeyID, candidate, json.RawMessage(`{"k":"v"}`))
		p10AssertToolNotFound(t, err)
		if invoker.called {
			t.Fatalf("调用不可见工具不应转发到任何上游：exposedName=%q apiKeyID=%q", candidate, apiKeyID)
		}
	})
}

// TestProperty10InvokeUnknownToolRejectedDirected 是 Property 10 的定向示例，锚定两类
// 关键情形（Req 10.4、11.7）：
//   - 空可见集合：无任何上游时，任意名称都不可见，调用被拒且不转发；
//   - 非空可见集合：存在可见工具时，对一个明确不在集合内的名称调用，被拒且不转发，
//     而对可见工具的调用则正常转发（反向印证拒绝逻辑只针对不可见名称）。
func TestProperty10InvokeUnknownToolRejectedDirected(t *testing.T) {
	ctx := context.Background()

	t.Run("空可见集合：无上游时任意名称被拒且不转发", func(t *testing.T) {
		cache := &invFakeCache{tools: map[string][]domain.ToolDef{}}
		upstreams := &invFakeUpstreams{upstreams: []domain.Upstream{}}
		aliases := &invFakeAliases{byUpstream: map[string][]domain.AliasRule{}}
		mcpFilters := &invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}}
		apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}}

		invoker := &invRecordingInvoker{}
		svc := invNewService(cache, upstreams, aliases, mcpFilters, apiKeyFilters).SetInvoker(invoker)

		_, err := svc.InvokeTool(ctx, "", "anything", json.RawMessage(`{}`))
		p10AssertToolNotFound(t, err)
		if invoker.called {
			t.Fatalf("空可见集合下不应转发任何调用")
		}
	})

	t.Run("非空可见集合：不可见名称被拒、可见名称正常转发", func(t *testing.T) {
		cache := &invFakeCache{tools: map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "read", Name: "read"}},
		}}
		upstreams := &invFakeUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up-a", 0)}}
		aliases := &invFakeAliases{byUpstream: map[string][]domain.AliasRule{}}
		mcpFilters := &invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}}
		apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}}

		invoker := &invRecordingInvoker{returnResult: domain.ToolResult{Content: json.RawMessage(`[]`)}}
		svc := invNewService(cache, upstreams, aliases, mcpFilters, apiKeyFilters).SetInvoker(invoker)

		// 不可见名称：被拒且不转发。
		_, err := svc.InvokeTool(ctx, "", "not_there", json.RawMessage(`{}`))
		p10AssertToolNotFound(t, err)
		if invoker.called {
			t.Fatalf("调用不可见工具不应转发到上游")
		}

		// 可见名称：正常转发（反向印证拒绝逻辑仅作用于不可见名称）。
		invoker.called = false
		if _, err := svc.InvokeTool(ctx, "", "read", json.RawMessage(`{}`)); err != nil {
			t.Fatalf("调用可见工具不应返回错误：%v", err)
		}
		if !invoker.called {
			t.Fatalf("调用可见工具应转发到上游")
		}
	})
}
