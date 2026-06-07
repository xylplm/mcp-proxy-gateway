package template

import (
	"fmt"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// 本文件实现设计文档「Correctness Properties」中的 Property 25（模板关键字检索命中），
// 针对 Market.Search 进行属性测试。
//
// 文件内的生成器与辅助标识符均使用 p25 前缀命名，以避免与同包 market_test.go 中的
// sampleTemplates/ids/newMarket 等标识符发生冲突。

// p25WordPool 是构造模板名称与简介的候选词，刻意混入大小写差异（如 SEARCH/Beta/Read）
// 与中文词（数据库、网关），以便与关键字生成器配合充分锻炼「大小写不敏感」匹配语义。
var p25WordPool = []string{
	"alpha", "Beta", "SEARCH", "gamma", "store", "Read", "delete",
	"数据库", "网关", "云",
}

// p25KeywordVariants 是一组关键字大小写变体，与 p25WordPool 中的词存在交集但大小写不同，
// 用于验证检索对大小写不敏感（关键字 search 应命中名称 SEARCH 等）。
var p25KeywordVariants = []string{
	"alpha", "ALPHA", "beta", "BETA", "search", "Search",
	"store", "STORE", "read", "DELETE", "数据库", "网关",
}

// genP25Text 生成模板的名称或简介：以较高概率由若干候选词拼接而成（便于关键字命中），
// 并混入少量任意短串与空串，覆盖边界情况。
func genP25Text() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.Custom(func(t *rapid.T) string {
			parts := rapid.SliceOfN(rapid.SampledFrom(p25WordPool), 0, 3).Draw(t, "textParts")
			return strings.Join(parts, " ")
		}),
		rapid.StringMatching(`[a-zA-Z ]{0,8}`),
	)
}

// genP25Keyword 生成检索关键字：覆盖「可能命中的原词」「大小写变体」「必不命中哨兵/空白」
// 以及任意短串，从而同时锻炼命中、未命中（Req 14.5 空列表）与空关键字（返回全部）路径。
func genP25Keyword() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.SampledFrom(p25WordPool),
		rapid.SampledFrom(p25KeywordVariants),
		rapid.SampledFrom([]string{"", "   ", "zzz-never-match", "qqq"}),
		rapid.StringMatching(`[a-zA-Z]{1,4}`),
	)
}

// genP25Templates 生成一组模板（含空集合）。ID 按下标赋予且全局唯一，便于按身份判断某模板
// 是否出现在检索结果中；分类固定为 Other，因为本属性只关心名称/简介与关键字的匹配关系。
func genP25Templates() *rapid.Generator[[]Template] {
	return rapid.Custom(func(t *rapid.T) []Template {
		n := rapid.IntRange(0, 8).Draw(t, "templateCount")
		out := make([]Template, n)
		for i := 0; i < n; i++ {
			out[i] = Template{
				ID:       fmt.Sprintf("tpl-%d", i),
				Name:     genP25Text().Draw(t, fmt.Sprintf("name-%d", i)),
				Summary:  genP25Text().Draw(t, fmt.Sprintf("summary-%d", i)),
				Category: CategoryOther,
			}
		}
		return out
	})
}

// Feature: mcp-proxy-gateway, Property 25: 模板关键字检索命中
//
// Validates: Requirements 14.4, 14.5
//
// 对任意模板集合与关键字：
//   - 返回的每个模板其名称或简介（小写）都包含关键字（小写）（Req 14.4）；
//   - 不在结果中的模板其名称与简介（小写）均不含关键字，确保检索「不漏不多」；
//   - 不存在匹配时返回空列表而非错误/nil（Req 14.5）。
//
// 参考语义：Search 将关键字去除首尾空白并转小写后，对名称或简介做小写子串包含；关键字
// 去空白后为空时等价于匹配全部（strings.Contains(x, "") 恒为真）。
func TestProperty25TemplateSearchKeywordHit(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		templates := genP25Templates().Draw(t, "templates")
		keyword := genP25Keyword().Draw(t, "keyword")

		m := newMarket(templates)
		got := m.Search(keyword)

		lower := strings.ToLower(strings.TrimSpace(keyword))

		// 参考命中集合（按 ID），与 Search 语义保持一致。
		wantIDs := make(map[string]bool)
		for _, tmpl := range templates {
			if strings.Contains(strings.ToLower(tmpl.Name), lower) ||
				strings.Contains(strings.ToLower(tmpl.Summary), lower) {
				wantIDs[tmpl.ID] = true
			}
		}

		// 断言 0：结果必为非 nil（无匹配返回空列表而非 nil）（Req 14.5）。
		if got == nil {
			t.Fatalf("Search 应返回空列表而非 nil：keyword=%q", keyword)
		}

		// 断言 1：返回的每个模板，其名称或简介（小写）含关键字（小写）（Req 14.4）。
		gotIDs := make(map[string]bool, len(got))
		for _, tmpl := range got {
			gotIDs[tmpl.ID] = true
			nameHit := strings.Contains(strings.ToLower(tmpl.Name), lower)
			summaryHit := strings.Contains(strings.ToLower(tmpl.Summary), lower)
			if !nameHit && !summaryHit {
				t.Fatalf("命中模板的名称或简介均不含关键字：id=%s name=%q summary=%q keyword=%q",
					tmpl.ID, tmpl.Name, tmpl.Summary, keyword)
			}
		}

		// 断言 2：不在结果中的模板，其名称与简介（小写）均不含关键字（验证「不漏」）。
		for _, tmpl := range templates {
			if gotIDs[tmpl.ID] {
				continue
			}
			nameHit := strings.Contains(strings.ToLower(tmpl.Name), lower)
			summaryHit := strings.Contains(strings.ToLower(tmpl.Summary), lower)
			if nameHit || summaryHit {
				t.Fatalf("含关键字的模板被漏掉：id=%s name=%q summary=%q keyword=%q",
					tmpl.ID, tmpl.Name, tmpl.Summary, keyword)
			}
		}

		// 断言 3：结果集 ID 恰等于参考命中集（数量与内容一致，验证「不多」）。
		if len(gotIDs) != len(wantIDs) {
			t.Fatalf("命中数量与参考集不一致：got=%d want=%d keyword=%q",
				len(gotIDs), len(wantIDs), keyword)
		}
		for id := range wantIDs {
			if !gotIDs[id] {
				t.Fatalf("期望命中但结果缺失：id=%s keyword=%q", id, keyword)
			}
		}

		// 断言 4：当不存在匹配时返回空列表（Req 14.5）。
		if len(wantIDs) == 0 && len(got) != 0 {
			t.Fatalf("无匹配应返回空列表，实际命中 %d 个：keyword=%q", len(got), keyword)
		}
	})
}
