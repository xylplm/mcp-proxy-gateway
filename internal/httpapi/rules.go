package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 本文件实现别名规则与 MCP 级屏蔽规则的管理端点（Req 8.1、9.1、9.2、9.9、9.11）。
//
// 别名规则（绑定上游 MCP）：
//   GET    /api/admin/upstreams/:id/aliases   列出某上游的别名规则
//   POST   /api/admin/upstreams/:id/aliases   创建别名规则
//   PUT    /api/admin/aliases/:ruleId         更新别名规则
//   DELETE /api/admin/aliases/:ruleId         删除别名规则
//
// MCP 级屏蔽规则（绑定上游 MCP）：
//   GET    /api/admin/upstreams/:id/filters         列出某上游的屏蔽规则
//   POST   /api/admin/upstreams/:id/filters         创建屏蔽规则
//   PUT    /api/admin/filters/:ruleId               更新屏蔽规则
//   POST   /api/admin/filters/:ruleId/enable        启用屏蔽规则
//   POST   /api/admin/filters/:ruleId/disable       停用屏蔽规则
//   DELETE /api/admin/filters/:ruleId               删除屏蔽规则
//
// 别名/屏蔽规则尚无独立应用服务，故由本层接线仓储并复用领域规则引擎做保存前字段校验
// （正则合法性、模式长度、目标字段非空、屏蔽规则数量上限），与设计「规则引擎为纯函数式」一致。

// aliasRuleRequest 为创建/更新别名规则的请求体（Req 8.1）。
type aliasRuleRequest struct {
	// Pattern 为匹配模式，长度需在 1 至 200 个字符之间。
	Pattern string `json:"pattern"`
	// IsRegex 表示是否启用正则匹配（完整匹配）。
	IsRegex bool `json:"isRegex"`
	// TargetName 为目标名称（1-100），与目标描述至少提供其一。
	TargetName string `json:"targetName"`
	// TargetDesc 为目标描述（≤1024）。
	TargetDesc string `json:"targetDesc"`
	// SortOrder 为规则排序顺序，多规则匹配时仅应用首条。
	SortOrder int `json:"sortOrder"`
}

// filterRuleRequest 为创建/更新 MCP 级屏蔽规则的请求体（Req 9.1）。
type filterRuleRequest struct {
	// Pattern 为匹配模式，长度需在 1 至 200 个字符之间。
	Pattern string `json:"pattern"`
	// IsRegex 表示是否启用正则匹配（完整匹配）。
	IsRegex bool `json:"isRegex"`
	// Enabled 表示该规则是否启用。
	Enabled bool `json:"enabled"`
	// SortOrder 为规则排序顺序。
	SortOrder int `json:"sortOrder"`
}

// registerRuleRoutes 在管理分组下注册别名与 MCP 级屏蔽规则管理端点。
func (r *Router) registerRuleRoutes(g *gin.RouterGroup) {
	// 别名规则：列表/创建按上游分组，更新/删除按规则标识。
	g.GET("/upstreams/:id/aliases", r.listAliases)
	g.POST("/upstreams/:id/aliases", r.createAlias)
	g.PUT("/aliases/:ruleId", r.updateAlias)
	g.DELETE("/aliases/:ruleId", r.deleteAlias)

	// MCP 级屏蔽规则。
	g.GET("/upstreams/:id/filters", r.listMCPFilters)
	g.POST("/upstreams/:id/filters", r.createMCPFilter)
	g.PUT("/filters/:ruleId", r.updateMCPFilter)
	g.POST("/filters/:ruleId/enable", r.enableMCPFilter)
	g.POST("/filters/:ruleId/disable", r.disableMCPFilter)
	g.DELETE("/filters/:ruleId", r.deleteMCPFilter)
}

// listAliases 返回某上游 MCP 的全部别名规则（Req 8.1）。
func (r *Router) listAliases(c *gin.Context) {
	if r.aliasStore == nil {
		respondServiceUnavailable(c, "别名规则服务未就绪")
		return
	}
	rules, err := r.aliasStore.ListByUpstream(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"aliases": rules})
}

// createAlias 在某上游 MCP 上创建一条别名规则（Req 8.1、8.9）。
//
// 流程：构造领域规则 → 复用规则引擎 ValidateAlias 做保存前字段校验（正则合法性、
// 模式长度、目标字段非空）→ 持久化。校验失败返回字段级 VALIDATION 错误且不持久化。
func (r *Router) createAlias(c *gin.Context) {
	if r.aliasStore == nil || r.ruleValidator == nil {
		respondServiceUnavailable(c, "别名规则服务未就绪")
		return
	}
	var req aliasRuleRequest
	if !bindJSON(c, &req) {
		return
	}
	rule := domain.AliasRule{
		UpstreamID: c.Param("id"),
		Pattern:    req.Pattern,
		IsRegex:    req.IsRegex,
		TargetName: req.TargetName,
		TargetDesc: req.TargetDesc,
		SortOrder:  req.SortOrder,
	}
	if err := r.ruleValidator.ValidateAlias(rule); err != nil {
		respondError(c, err)
		return
	}
	created, err := r.aliasStore.Create(c.Request.Context(), rule)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, created)
}

// updateAlias 更新一条别名规则（Req 8.1、8.9）。
//
// 绑定的上游 MCP 不可变更：先读取既有规则以沿用其 UpstreamID，再以提交字段覆盖并校验。
func (r *Router) updateAlias(c *gin.Context) {
	if r.aliasStore == nil || r.ruleValidator == nil {
		respondServiceUnavailable(c, "别名规则服务未就绪")
		return
	}
	var req aliasRuleRequest
	if !bindJSON(c, &req) {
		return
	}
	existing, err := r.aliasStore.Get(c.Request.Context(), c.Param("ruleId"))
	if err != nil {
		respondError(c, err)
		return
	}
	rule := domain.AliasRule{
		ID:         existing.ID,
		UpstreamID: existing.UpstreamID,
		Pattern:    req.Pattern,
		IsRegex:    req.IsRegex,
		TargetName: req.TargetName,
		TargetDesc: req.TargetDesc,
		SortOrder:  req.SortOrder,
	}
	if err := r.ruleValidator.ValidateAlias(rule); err != nil {
		respondError(c, err)
		return
	}
	updated, err := r.aliasStore.Update(c.Request.Context(), rule)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

// deleteAlias 删除一条别名规则（Req 8.1）。
func (r *Router) deleteAlias(c *gin.Context) {
	if r.aliasStore == nil {
		respondServiceUnavailable(c, "别名规则服务未就绪")
		return
	}
	if err := r.aliasStore.Delete(c.Request.Context(), c.Param("ruleId")); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// listMCPFilters 返回某上游 MCP 的全部屏蔽规则（Req 9.1）。
func (r *Router) listMCPFilters(c *gin.Context) {
	if r.filterMCPStore == nil {
		respondServiceUnavailable(c, "屏蔽规则服务未就绪")
		return
	}
	rows, err := r.filterMCPStore.ListByUpstream(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"filters": rows})
}

// createMCPFilter 在某上游 MCP 上创建一条屏蔽规则（Req 9.1、9.2、9.9）。
//
// 流程：字段级校验（复用 ValidateFilter）→ 数量上限校验（单上游至多 100 条，
// 复用 domain.ValidateFilterCount）→ 以当前规则数作为排序值追加持久化。
func (r *Router) createMCPFilter(c *gin.Context) {
	if r.filterMCPStore == nil || r.ruleValidator == nil {
		respondServiceUnavailable(c, "屏蔽规则服务未就绪")
		return
	}
	var req filterRuleRequest
	if !bindJSON(c, &req) {
		return
	}
	upstreamID := c.Param("id")
	rule := domain.FilterRule{
		Pattern: req.Pattern,
		IsRegex: req.IsRegex,
		Enabled: req.Enabled,
	}
	// 1) 字段级校验（正则合法性、模式长度 1-200），不通过即拒绝且不持久化（Req 9.7、9.8）。
	if err := r.ruleValidator.ValidateFilter(rule); err != nil {
		respondError(c, err)
		return
	}
	// 2) 数量上限校验：单上游至多 100 条（Req 9.2、9.9）。
	current, err := r.filterMCPStore.CountByUpstream(c.Request.Context(), upstreamID)
	if err != nil {
		respondError(c, err)
		return
	}
	if err := domain.ValidateFilterCount(current); err != nil {
		respondError(c, err)
		return
	}
	// 3) 以当前规则数作为排序值追加到末尾，保证 List 的稳定升序。
	rule.SortOrder = current
	created, err := r.filterMCPStore.Create(c.Request.Context(), store.FilterMCPRow{
		FilterRule: rule,
		UpstreamID: upstreamID,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, created)
}

// updateMCPFilter 更新一条 MCP 级屏蔽规则（Req 9.1、9.7、9.8）。
//
// 绑定的上游 MCP 不可变更：先读取既有规则以沿用其 UpstreamID，再以提交字段覆盖并校验。
func (r *Router) updateMCPFilter(c *gin.Context) {
	if r.filterMCPStore == nil || r.ruleValidator == nil {
		respondServiceUnavailable(c, "屏蔽规则服务未就绪")
		return
	}
	var req filterRuleRequest
	if !bindJSON(c, &req) {
		return
	}
	existing, err := r.filterMCPStore.Get(c.Request.Context(), c.Param("ruleId"))
	if err != nil {
		respondError(c, err)
		return
	}
	rule := domain.FilterRule{
		ID:        existing.ID,
		Pattern:   req.Pattern,
		IsRegex:   req.IsRegex,
		Enabled:   req.Enabled,
		SortOrder: req.SortOrder,
	}
	if err := r.ruleValidator.ValidateFilter(rule); err != nil {
		respondError(c, err)
		return
	}
	updated, err := r.filterMCPStore.Update(c.Request.Context(), store.FilterMCPRow{
		FilterRule: rule,
		UpstreamID: existing.UpstreamID,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

// enableMCPFilter 启用一条 MCP 级屏蔽规则（Req 9.11）。
func (r *Router) enableMCPFilter(c *gin.Context) {
	r.setMCPFilterEnabled(c, true)
}

// disableMCPFilter 停用一条 MCP 级屏蔽规则（Req 9.11）。
func (r *Router) disableMCPFilter(c *gin.Context) {
	r.setMCPFilterEnabled(c, false)
}

// setMCPFilterEnabled 是 MCP 级屏蔽规则启停的共用实现。
func (r *Router) setMCPFilterEnabled(c *gin.Context, enabled bool) {
	if r.filterMCPStore == nil {
		respondServiceUnavailable(c, "屏蔽规则服务未就绪")
		return
	}
	if err := r.filterMCPStore.SetEnabled(c.Request.Context(), c.Param("ruleId"), enabled); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": c.Param("ruleId"), "enabled": enabled})
}

// deleteMCPFilter 删除一条 MCP 级屏蔽规则（Req 9.1）。
func (r *Router) deleteMCPFilter(c *gin.Context) {
	if r.filterMCPStore == nil {
		respondServiceUnavailable(c, "屏蔽规则服务未就绪")
		return
	}
	if err := r.filterMCPStore.Delete(c.Request.Context(), c.Param("ruleId")); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
