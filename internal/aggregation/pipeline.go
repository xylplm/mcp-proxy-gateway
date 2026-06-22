package aggregation

import (
	"sort"
	"strconv"
	"strings"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// ReverseEntry 是「对外名称 → 上游来源」反向映射的值。
//
// 聚合管线对外暴露的工具名称全局唯一，因此可由对外 Name 唯一还原出该工具
// 所属的上游标识与上游原始工具名，供工具调用路由（任务 4.6 的 InvokeTool）复用
// （Req 10.6）。
type ReverseEntry struct {
	// UpstreamID 为该工具所属上游 MCP 的标识。
	UpstreamID string
	// UpstreamName 为调用时的上游名称快照，仅用于统计展示。
	UpstreamName string
	// OriginalName 为该工具在上游 MCP 中的原始名称（调用转发依据）。
	OriginalName string
}

// upstreamBundle 是单个启用上游在聚合管线中的输入数据。
//
// 将「已从数据源读取好的数据」与「纯逻辑管线」解耦：编排层（Service）负责读缓存、
// 读规则并填充该结构，runPipeline 仅对这些已就绪的数据做确定性变换，便于属性测试
// 直接构造输入而无需依赖真实缓存/数据库。
type upstreamBundle struct {
	// upstreamID 为该上游的标识。
	upstreamID string
	// upstreamName 为该上游的名称快照，仅用于统计展示。
	upstreamName string
	// sortOrder 为该上游的排序顺序，决定其工具在聚合结果中的相对位置（Req 3.4、10.1）。
	sortOrder int
	// tools 为从工具缓存读取的该上游工具列表（仅启用上游，Req 6.2、10.1）。
	tools []domain.ToolDef
	// aliases 为绑定在该上游上的别名/描述重写规则（Req 8）。
	aliases []domain.AliasRule
	// mcpFilters 为绑定在该上游上的 MCP 级屏蔽规则（Req 9）。
	mcpFilters []domain.FilterRule
}

// runPipeline 执行聚合管线的第 2 至 6 阶段，是一个确定性纯函数。
//
// 入参 bundles 为各启用上游的已就绪数据（对应阶段 1「缓存读取，仅启用上游」的产物，
// 由编排层完成），apiKeyFilters 为某 API Key 的屏蔽规则（apiKeyID 为空即无 API Key
// 级过滤时传入 nil/空切片）。
//
// 执行顺序固定，不可调换（Req 10.2、设计文档「工具聚合管线」）：
//  1. 阶段 2 排序合并：按 upstream.sort_order 升序（稳定）拼接各上游工具（Req 3.4、10.1）。
//  2. 阶段 3 MCP 级屏蔽：对每个上游的工具应用其绑定的启用屏蔽规则，命中即排除（Req 9.3）。
//  3. 阶段 4 别名/描述重写：对保留工具按规则 sort_order 应用首条匹配的别名规则（Req 8.2/8.3/8.5）。
//     注意「先屏蔽后重写」：被屏蔽的工具不会因别名重写而重新出现（Req 10.2）。
//  4. 阶段 5 同名去重：排序在前者保留原名，排序在后的同名工具追加可区分后缀，
//     确保对外名称全局唯一（Req 3.6、8.6）。
//  5. 阶段 6 API Key 级过滤：在统一聚合结果之上再应用该 API Key 的启用屏蔽规则，
//     匹配对象为工具的 OriginalName（上游原始名称），而非别名重写后的对外名（Req 13.7）。
//
// 返回变换后的工具集合（始终为非 nil 切片，无工具时为空切片）与「对外名称 → 上游来源」
// 反向映射。
//
// 关键不变量：
//   - 输出集合中所有对外 Name 全局唯一（Property 1）。
//   - API Key 级过滤置于去重之后，保证某 API Key 的可见集合是全局集合的子集——
//     过滤只删除工具而不改名，剩余工具的对外名与全局集合一致（Property 8）。
func runPipeline(engine domain.Rule_Engine, bundles []upstreamBundle, apiKeyFilters []domain.FilterRule) ([]domain.ToolDef, map[string]ReverseEntry) {
	// 阶段 2：排序合并。复制后稳定排序，不修改调用方传入的切片顺序；
	// 相同 sort_order 的上游维持其原相对顺序。
	sorted := make([]upstreamBundle, len(bundles))
	copy(sorted, bundles)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].sortOrder < sorted[j].sortOrder
	})

	merged := make([]domain.ToolDef, 0)
	for _, b := range sorted {
		// 复制该上游的工具，并规范化工具的归属信息：
		// 强制 UpstreamID 与 Order 与所读取的上游一致，保证后续去重后缀与反向映射正确，
		// 不依赖缓存内工具自带字段的准确性（防御性，且保持纯函数不修改入参元素）。
		tools := make([]domain.ToolDef, len(b.tools))
		copy(tools, b.tools)
		for i := range tools {
			tools[i].UpstreamID = b.upstreamID
			tools[i].Order = b.sortOrder
		}

		// 阶段 3：MCP 级屏蔽（仅对该上游的工具应用其绑定的屏蔽规则）。
		tools = engine.ApplyFilters(tools, b.mcpFilters)
		// 阶段 4：别名/描述重写（仅对该上游的工具应用其绑定的别名规则，首条匹配生效）。
		tools = engine.ApplyAliases(tools, b.aliases)

		merged = append(merged, tools...)
	}

	// 阶段 5：同名去重，保证对外名称全局唯一。
	merged = dedupNames(merged)

	// 阶段 6：API Key 级过滤（匹配 OriginalName；apiKeyFilters 为空时返回原集合的副本）。
	merged = engine.ApplyFilters(merged, apiKeyFilters)

	// 构建「对外名称 → (上游标识, 原始名)」反向映射，供调用路由复用（Req 10.6）。
	reverse := make(map[string]ReverseEntry, len(merged))
	upstreamNames := make(map[string]string, len(sorted))
	for _, b := range sorted {
		upstreamNames[b.upstreamID] = b.upstreamName
	}
	for _, t := range merged {
		reverse[t.Name] = ReverseEntry{UpstreamID: t.UpstreamID, UpstreamName: upstreamNames[t.UpstreamID], OriginalName: t.OriginalName}
	}

	return merged, reverse
}

// dedupNames 对工具集合按出现顺序做同名去重，确保对外 Name 全局唯一（Req 3.6、8.6）。
//
// 规则：
//   - 首次出现的某对外名称予以保留（排序在前者保留原名）。
//   - 之后再次出现的同名工具被追加可区分后缀 "__{upstreamShortId}"。
//   - 若追加后缀后仍与已有名称冲突（例如同一上游内别名重写产生多个同名工具，
//     或后缀本身恰好已被占用），再追加递增序号 "_1"、"_2"……直至唯一，
//     以在任何输入下都保证全局唯一这一硬不变量。
//
// 该方法为纯函数：复制输入切片、不修改入参元素，保持工具原有相对顺序。
func dedupNames(tools []domain.ToolDef) []domain.ToolDef {
	out := make([]domain.ToolDef, len(tools))
	copy(out, tools)

	seen := make(map[string]struct{}, len(out))
	for i := range out {
		name := out[i].Name
		if _, exists := seen[name]; !exists {
			// 首次出现：保留原名。
			seen[name] = struct{}{}
			continue
		}

		// 同名冲突：为排序在后者追加可区分后缀。
		base := name + "__" + shortUpstreamID(out[i].UpstreamID)
		candidate := base
		for n := 1; ; n++ {
			if _, exists := seen[candidate]; !exists {
				break
			}
			candidate = base + "_" + strconv.Itoa(n)
		}
		out[i].Name = candidate
		seen[candidate] = struct{}{}
	}

	return out
}

// shortUpstreamID 取上游标识的短形式，用作同名去重后缀的可区分标记。
//
// 对 UUID 形式的标识去除连字符后取前 8 个字符；对更短或非 UUID 形式的标识则原样使用。
// 采用 rune 切片以避免在多字节字符上截断产生非法 UTF-8（虽然 UUID 为纯 ASCII，
// 此处对任意输入保持健壮）。注意：短标记可能在极端情况下不唯一，最终唯一性由
// dedupNames 的递增序号兜底保证。
func shortUpstreamID(id string) string {
	cleaned := strings.ReplaceAll(id, "-", "")
	r := []rune(cleaned)
	if len(r) > 8 {
		return string(r[:8])
	}
	if len(r) > 0 {
		return cleaned
	}
	return id
}
