package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/template"
)

// 本文件实现模板市场（Template_Market）管理端点（Req 14.1-14.7、14.13、17.5）。
//
// 端点均挂载于管理员 JWT 中间件之下、统一前缀 /api/admin/templates，满足前端
// web/src/api/templates.ts 约定的拟定路由契约：
//
//   GET /api/admin/templates                 列出全部模板，支持 ?category=&keyword= 过滤
//   GET /api/admin/templates/categories       按分类组织的模板视图（分类导航）
//   GET /api/admin/templates/:id              模板详情
//   GET /api/admin/templates/:id/prefill      基于模板的表单预填充数据
//
// 响应直接序列化 internal/template 包的领域结构：其 json tag 已与前端 TS 类型
// （Template / CategoryView / Placeholder / PrefillForm）逐字段对齐（camelCase），
// 故本层无需额外 DTO 映射。无匹配时统一返回非 nil 空列表（Req 14.5、14.13）。

// registerTemplateRoutes 在管理分组下注册模板市场查询端点（Req 14.1-14.7）。
//
// 注意：静态子路由 /templates/categories 须先于参数路由 /templates/:id 注册，
// 二者在 gin 的路由树中可共存（静态段优先匹配）。
func (r *Router) registerTemplateRoutes(g *gin.RouterGroup) {
	t := g.Group("/templates")
	t.GET("", r.listTemplates)
	t.GET("/categories", r.listTemplateCategories)
	t.GET("/:id", r.getTemplate)
	t.GET("/:id/prefill", r.prefillTemplate)
}

// listTemplates 列出模板，支持按分类与关键字过滤（Req 14.3、14.4、14.5）。
//
// 同时给定 category 与 keyword 时，先按关键字检索（名称或简介匹配），再按分类筛选，
// 二者共同生效；任一过滤不命中均返回非 nil 空列表而非错误（Req 14.5、14.13）。
func (r *Router) listTemplates(c *gin.Context) {
	if r.templates == nil {
		respondServiceUnavailable(c, "模板市场服务未就绪")
		return
	}
	category := c.Query("category")
	keyword := c.Query("keyword")

	var list []template.Template
	switch {
	case category != "" && keyword != "":
		// 先关键字检索（复用市场的名称/简介匹配），再按分类筛选，使两项查询参数同时生效。
		list = filterByCategory(r.templates.Search(keyword), template.Category(category))
	case category != "":
		list = r.templates.ListByCategory(template.Category(category))
	case keyword != "":
		list = r.templates.Search(keyword)
	default:
		list = r.templates.List()
	}

	respondOK(c, gin.H{"templates": ensureTemplates(list)})
}

// listTemplateCategories 返回按分类组织的模板视图，用于分类导航浏览（Req 14.2）。
func (r *Router) listTemplateCategories(c *gin.Context) {
	if r.templates == nil {
		respondServiceUnavailable(c, "模板市场服务未就绪")
		return
	}
	respondOK(c, gin.H{"categories": r.templates.ListByCategories()})
}

// getTemplate 返回模板详情（Req 14.6）；模板不存在时由服务返回 NOT_FOUND（映射 404）。
func (r *Router) getTemplate(c *gin.Context) {
	if r.templates == nil {
		respondServiceUnavailable(c, "模板市场服务未就绪")
		return
	}
	tpl, err := r.templates.Get(c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, tpl)
}

// prefillTemplate 返回基于模板的表单预填充数据（Req 14.7）；模板不存在时映射 404。
func (r *Router) prefillTemplate(c *gin.Context) {
	if r.templates == nil {
		respondServiceUnavailable(c, "模板市场服务未就绪")
		return
	}
	form, err := r.templates.Prefill(c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, form)
}

// filterByCategory 从模板切片中筛选出指定分类的模板，返回非 nil 切片（无匹配为空切片）。
func filterByCategory(ts []template.Template, c template.Category) []template.Template {
	out := make([]template.Template, 0, len(ts))
	for i := range ts {
		if ts[i].Category == c {
			out = append(out, ts[i])
		}
	}
	return out
}

// ensureTemplates 保证返回给前端的模板列表为非 nil 切片，使 JSON 序列化为 [] 而非 null。
func ensureTemplates(ts []template.Template) []template.Template {
	if ts == nil {
		return []template.Template{}
	}
	return ts
}
