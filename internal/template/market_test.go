package template

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// sampleTemplates 提供一组确定性的测试模板，覆盖多分类与可检索的名称/简介，
// 便于在不依赖内置集合细节的情况下验证查询行为。
func sampleTemplates() []Template {
	return []Template{
		{
			ID:        "alpha-search",
			Name:      "Alpha 搜索",
			Category:  CategorySearch,
			Summary:   "提供网页搜索能力的服务。",
			DocURL:    "https://example.com/alpha",
			Transport: domain.TransportStreamableHTTP,
			Placeholders: []Placeholder{
				{Name: "apiKey", Required: true, Rule: ParamRule{Kind: ParamSecret}},
			},
		},
		{
			ID:        "beta-db",
			Name:      "Beta 数据库",
			Category:  CategoryDatabase,
			Summary:   "连接关系型数据库进行查询。",
			DocURL:    "https://example.com/beta",
			Transport: domain.TransportStdio,
		},
		{
			ID:        "gamma-db",
			Name:      "Gamma 存储网关",
			Category:  CategoryDatabase,
			Summary:   "对象存储读写，支持搜索索引。",
			DocURL:    "https://example.com/gamma",
			Transport: domain.TransportStdio,
		},
	}
}

func ids(ts []Template) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

func TestListByCategory_ReturnsAllInCategory(t *testing.T) {
	m := newMarket(sampleTemplates())

	got := m.ListByCategory(CategoryDatabase)
	if len(got) != 2 {
		t.Fatalf("分类 database 期望 2 个模板，实际 %d 个：%v", len(got), ids(got))
	}
	for _, tm := range got {
		if tm.Category != CategoryDatabase {
			t.Errorf("返回的模板 %s 分类应为 database，实际 %s", tm.ID, tm.Category)
		}
	}
}

func TestListByCategory_NoMatchReturnsEmptyNotNil(t *testing.T) {
	m := newMarket(sampleTemplates())

	got := m.ListByCategory(CategoryAutomation)
	if got == nil {
		t.Fatal("无匹配分类应返回空列表而非 nil")
	}
	if len(got) != 0 {
		t.Fatalf("无匹配分类期望空列表，实际 %v", ids(got))
	}
}

func TestSearch_MatchesNameOrSummary(t *testing.T) {
	m := newMarket(sampleTemplates())

	// 关键字「搜索」命中 alpha 名称与 gamma 简介。
	got := m.Search("搜索")
	gotIDs := ids(got)
	if len(gotIDs) != 2 {
		t.Fatalf("关键字「搜索」期望命中 2 个模板，实际 %v", gotIDs)
	}
	for _, tm := range got {
		if !strings.Contains(tm.Name, "搜索") && !strings.Contains(tm.Summary, "搜索") {
			t.Errorf("命中模板 %s 的名称或简介均不含关键字「搜索」", tm.ID)
		}
	}
}

func TestSearch_CaseInsensitive(t *testing.T) {
	m := newMarket(sampleTemplates())

	got := m.Search("alpha")
	if len(got) != 1 || got[0].ID != "alpha-search" {
		t.Fatalf("大小写不敏感检索 alpha 应命中 alpha-search，实际 %v", ids(got))
	}
}

func TestSearch_NoMatchReturnsEmptyNotNil(t *testing.T) {
	m := newMarket(sampleTemplates())

	got := m.Search("不存在的关键字")
	if got == nil {
		t.Fatal("无匹配关键字应返回空列表而非 nil")
	}
	if len(got) != 0 {
		t.Fatalf("无匹配关键字期望空列表，实际 %v", ids(got))
	}
}

func TestSearch_EmptyKeywordReturnsAll(t *testing.T) {
	m := newMarket(sampleTemplates())

	got := m.Search("   ")
	if len(got) != len(sampleTemplates()) {
		t.Fatalf("空白关键字应返回全部模板，期望 %d 个，实际 %v", len(sampleTemplates()), ids(got))
	}
}

func TestGet_ReturnsDetail(t *testing.T) {
	m := newMarket(sampleTemplates())

	got, err := m.Get("alpha-search")
	if err != nil {
		t.Fatalf("查询存在的模板不应报错：%v", err)
	}
	if got.Summary == "" || got.DocURL == "" || got.Transport == "" {
		t.Errorf("详情应包含简介、文档链接与传输类型，实际 %+v", got)
	}
	if len(got.Placeholders) != 1 || got.Placeholders[0].Name != "apiKey" {
		t.Errorf("详情应返回全部占位参数定义，实际 %+v", got.Placeholders)
	}
}

func TestGet_NotFoundReturnsError(t *testing.T) {
	m := newMarket(sampleTemplates())

	_, err := m.Get("missing")
	if err == nil {
		t.Fatal("查询不存在的模板应返回错误")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeNotFound {
		t.Fatalf("应返回 NOT_FOUND 错误，实际 %v", err)
	}
}

func TestEmptyMarket_QueriesReturnEmptyNotError(t *testing.T) {
	m := newMarket(nil)

	if got := m.List(); got == nil || len(got) != 0 {
		t.Fatalf("空市场 List 应返回空列表，实际 %v", ids(got))
	}
	if got := m.ListByCategory(CategorySearch); got == nil || len(got) != 0 {
		t.Fatalf("空市场 ListByCategory 应返回空列表，实际 %v", ids(got))
	}
	if got := m.Search("任意"); got == nil || len(got) != 0 {
		t.Fatalf("空市场 Search 应返回空列表，实际 %v", ids(got))
	}
	if _, err := m.Get("any"); err == nil {
		t.Fatal("空市场 Get 不存在的模板应返回错误")
	}
}

func TestBuiltinMarket_CoversAtLeastEightCategories(t *testing.T) {
	if len(orderedCategories) < 8 {
		t.Fatalf("分类集合至少应含 8 类，实际 %d", len(orderedCategories))
	}

	m := New()
	all := m.List()
	if len(all) == 0 {
		t.Fatal("内置模板集合不应为空")
	}

	// 每个内置模板的分类必须属于受支持分类集合，且关键字段非空。
	supported := make(map[Category]bool, len(orderedCategories))
	for _, c := range orderedCategories {
		supported[c] = true
	}
	seenIDs := make(map[string]bool, len(all))
	for _, tm := range all {
		if seenIDs[tm.ID] {
			t.Errorf("内置模板 ID 重复：%s", tm.ID)
		}
		seenIDs[tm.ID] = true
		if !supported[tm.Category] {
			t.Errorf("模板 %s 使用了未受支持的分类 %s", tm.ID, tm.Category)
		}
		if tm.ID == "" || tm.Name == "" || tm.Summary == "" || tm.DocURL == "" || tm.Transport == "" {
			t.Errorf("内置模板 %q 关键字段不应为空：%+v", tm.ID, tm)
		}
		if tm.TrustLevel == "" || len(tm.Runtimes) == 0 || len(tm.CredentialTypes) == 0 || len(tm.ToolTypes) == 0 {
			t.Errorf("内置模板 %q 应包含可信度、运行环境、凭证类型和工具类型标签：%+v", tm.ID, tm)
		}
		for _, ph := range tm.Placeholders {
			if ph.Name == "" {
				t.Errorf("模板 %s 的占位参数缺少参数名", tm.ID)
			}
		}
	}
}

func TestBuiltinTemplateMetadataTags(t *testing.T) {
	m := New()

	tavily, err := m.Get("tavily-search")
	if err != nil {
		t.Fatalf("查询 tavily-search 失败：%v", err)
	}
	assertTemplateMetaContains(t, tavily.CredentialTypes, CredentialAPIKey)
	assertTemplateMetaContains(t, tavily.Runtimes, RuntimeRemote)
	assertTemplateMetaContains(t, tavily.ToolTypes, ToolTypeSearch)
	if !tavily.ContainerReady {
		t.Fatal("远程 HTTP 模板应适合容器内运行")
	}

	github, err := m.Get("github-mcp")
	if err != nil {
		t.Fatalf("查询 github-mcp 失败：%v", err)
	}
	// GitHub 模板已从 docker run 改为官方托管远程端点：容器内无 docker CLI 也未挂宿主
	// socket，docker 通路永远不可用，只有远程传输才能开箱接入。
	assertTemplateMetaContains(t, github.Runtimes, RuntimeRemote)
	assertTemplateMetaContains(t, github.CredentialTypes, CredentialToken)
	assertTemplateMetaContains(t, github.ToolTypes, ToolTypeProjectManagement)
	if !github.ContainerReady {
		t.Fatal("远程传输模板应标记为适合容器内运行")
	}
	if github.Transport != domain.TransportStreamableHTTP {
		t.Fatalf("github-mcp 传输类型=%q，应为 streamable-http", github.Transport)
	}

	playwright, err := m.Get("playwright-mcp")
	if err != nil {
		t.Fatalf("查询 playwright-mcp 失败：%v", err)
	}
	assertTemplateMetaContains(t, playwright.Runtimes, RuntimeNode)
	assertTemplateMetaContains(t, playwright.CredentialTypes, CredentialNone)
	assertTemplateMetaContains(t, playwright.ToolTypes, ToolTypeBrowser)
	if playwright.ContainerReady {
		t.Fatal("本地 npx 浏览器模板不应默认标记为容器友好")
	}
}

func assertTemplateMetaContains[T comparable](t *testing.T, values []T, want T) {
	t.Helper()
	if slices.Contains(values, want) {
		return
	}
	t.Fatalf("标签列表 %v 未包含 %v", values, want)
}

func TestListByCategories_CoversAllCategoriesInOrder(t *testing.T) {
	m := New()
	views := m.ListByCategories()
	if len(views) != len(orderedCategories) {
		t.Fatalf("分类视图数应等于分类总数 %d，实际 %d", len(orderedCategories), len(views))
	}
	for i, v := range views {
		if v.Category != orderedCategories[i] {
			t.Errorf("第 %d 个分类视图应为 %s，实际 %s", i, orderedCategories[i], v.Category)
		}
		if v.DisplayName == "" {
			t.Errorf("分类 %s 缺少中文显示名", v.Category)
		}
		if v.Templates == nil {
			t.Errorf("分类 %s 的模板列表应为空列表而非 nil", v.Category)
		}
	}
}

func TestReturnedTemplatesAreCopies(t *testing.T) {
	m := newMarket(sampleTemplates())

	got := m.ListByCategory(CategorySearch)
	if len(got) != 1 {
		t.Fatalf("期望命中 1 个模板，实际 %v", ids(got))
	}
	// 修改返回副本不应影响内部数据。
	got[0].Name = "被篡改"
	got[0].Placeholders[0].Required = false

	again, err := m.Get("alpha-search")
	if err != nil {
		t.Fatalf("查询模板不应报错：%v", err)
	}
	if again.Name == "被篡改" {
		t.Error("返回的模板应为深拷贝，修改不应影响内部数据（Name）")
	}
	if !again.Placeholders[0].Required {
		t.Error("返回的模板应为深拷贝，修改不应影响内部数据（Placeholders）")
	}
}
