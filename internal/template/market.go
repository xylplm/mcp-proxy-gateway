package template

import (
	"sort"
	"strings"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// Market 是模板市场（Template_Market）的实现：维护内置分类化快捷模板集合，
// 并提供按分类筛选、关键字检索与详情查询能力（Req 14.1-14.6、14.13）。
//
// Market 实例化后其模板集合不可变，所有查询方法均为只读且并发安全；返回的模板均为
// 深拷贝，调用方对结果的修改不影响市场内部数据。无匹配或集合为空时返回空列表而非错误
// （Req 14.5、14.13）。
type Market struct {
	// templates 为模板集合，按构造时的顺序保存（保持稳定的展示次序）。
	templates []Template
	// index 为模板 ID 到其在 templates 中下标的映射，加速详情查询。
	index map[string]int
}

// New 构造一个使用内置模板集合的模板市场。
func New() *Market {
	return newMarket(builtinTemplates())
}

// newMarket 以给定模板集合构造模板市场；供内部与测试使用，便于注入空集合或自定义集合。
func newMarket(templates []Template) *Market {
	idx := make(map[string]int, len(templates))
	stored := make([]Template, 0, len(templates))
	for _, t := range templates {
		stored = append(stored, t.clone())
		idx[t.ID] = len(stored) - 1
	}
	return &Market{templates: stored, index: idx}
}

// List 返回模板市场中的全部模板；集合为空时返回空列表而非错误（Req 14.13）。
func (m *Market) List() []Template {
	return m.copyAll(m.templates)
}

// ListByCategory 返回指定分类下的全部模板（Req 14.3）。
//
// 不存在匹配模板时返回空列表而非错误（Req 14.5）。
func (m *Market) ListByCategory(c Category) []Template {
	out := make([]Template, 0)
	for i := range m.templates {
		if m.templates[i].Category == c {
			out = append(out, m.templates[i].clone())
		}
	}
	return out
}

// Search 按关键字检索模板，返回名称或简介中包含该关键字的模板（Req 14.4）。
//
// 匹配为大小写不敏感的子串包含；关键字为空（或仅空白）时视为不限定，返回全部模板。
// 不存在匹配时返回空列表而非错误（Req 14.5）。
func (m *Market) Search(keyword string) []Template {
	kw := strings.TrimSpace(keyword)
	if kw == "" {
		return m.copyAll(m.templates)
	}
	lower := strings.ToLower(kw)
	out := make([]Template, 0)
	for i := range m.templates {
		t := &m.templates[i]
		if strings.Contains(strings.ToLower(t.Name), lower) ||
			strings.Contains(strings.ToLower(t.Summary), lower) {
			out = append(out, t.clone())
		}
	}
	return out
}

// Get 按模板标识返回模板详情，包含简介、文档链接、传输类型与全部占位参数定义（Req 14.6）。
//
// 模板不存在时返回 NOT_FOUND 错误。
func (m *Market) Get(id string) (Template, error) {
	if i, ok := m.index[id]; ok {
		return m.templates[i].clone(), nil
	}
	return Template{}, domain.NewError(domain.CodeNotFound, "快捷模板不存在："+id)
}

// CategoryView 为单个分类及其包含的模板，供按分类组织的整体浏览使用。
type CategoryView struct {
	// Category 为分类标识。
	Category Category `json:"category"`
	// DisplayName 为分类的中文显示名。
	DisplayName string `json:"displayName"`
	// Templates 为该分类下的模板列表，可能为空。
	Templates []Template `json:"templates"`
}

// ListByCategories 返回按分类组织的全部模板视图（Req 14.2）。
//
// 视图按稳定的分类展示顺序排列，覆盖全部受支持分类；某分类无模板时其 Templates 为空列表
// 而非缺省，便于前端完整呈现分类导航。
func (m *Market) ListByCategories() []CategoryView {
	views := make([]CategoryView, 0, len(orderedCategories))
	for _, c := range orderedCategories {
		views = append(views, CategoryView{
			Category:    c,
			DisplayName: CategoryDisplayName(c),
			Templates:   m.ListByCategory(c),
		})
	}
	return views
}

// copyAll 返回模板切片的深拷贝，保证调用方无法通过返回值修改内部数据。
func (m *Market) copyAll(src []Template) []Template {
	out := make([]Template, 0, len(src))
	for i := range src {
		out = append(out, src[i].clone())
	}
	return out
}

// SortByName 对模板切片按名称就地升序排序，供调用方按需获取稳定有序的展示列表。
//
// 该工具函数不修改市场内部数据（市场返回的均为副本），便于测试与展示层复用。
func SortByName(ts []Template) {
	sort.SliceStable(ts, func(i, j int) bool {
		return ts[i].Name < ts[j].Name
	})
}
