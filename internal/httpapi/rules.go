package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 本文件实现别名规则与 MCP 级屏蔽规则的管理端点（Req 8.1、9.1、9.2、9.9、9.11）。
//
// 别名规则：
//   GET    /api/admin/aliases           列出全部别名规则
//   POST   /api/admin/aliases           创建别名规则
//   PUT    /api/admin/aliases/:ruleId   更新别名规则
//   DELETE /api/admin/aliases/:ruleId   删除别名规则
//
// MCP 级屏蔽规则：
//   GET    /api/admin/filters                  列出全部屏蔽规则
//   POST   /api/admin/filters                  创建屏蔽规则
//   PUT    /api/admin/filters/:ruleId          更新屏蔽规则
//   POST   /api/admin/filters/:ruleId/enable   启用屏蔽规则
//   POST   /api/admin/filters/:ruleId/disable  停用屏蔽规则
//   DELETE /api/admin/filters/:ruleId          删除屏蔽规则
//
// 别名/屏蔽规则尚无独立应用服务，故由本层接线仓储并复用领域规则引擎做保存前字段校验
// （正则合法性、模式长度、目标字段非空、屏蔽规则数量上限），与设计「规则引擎为纯函数式」一致。

// aliasRuleRequest 为创建/更新别名规则的请求体（Req 8.1）。
type aliasRuleRequest struct {
	ScopeType   string   `json:"scopeType"`
	UpstreamIDs []string `json:"upstreamIds"`
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
	ScopeType   string   `json:"scopeType"`
	UpstreamIDs []string `json:"upstreamIds"`
	// Pattern 为匹配模式，长度需在 1 至 200 个字符之间。
	Pattern string `json:"pattern"`
	// IsRegex 表示是否启用正则匹配（完整匹配）。
	IsRegex bool `json:"isRegex"`
	// Enabled 表示该规则是否启用。
	Enabled bool `json:"enabled"`
	// SortOrder 为规则排序顺序。
	SortOrder int `json:"sortOrder"`
}

// toolPolicyRuleRequest 为创建/更新工具策略规则的请求体。
type toolPolicyRuleRequest struct {
	Pattern         string   `json:"pattern"`
	IsRegex         bool     `json:"isRegex"`
	Enabled         bool     `json:"enabled"`
	SortOrder       int      `json:"sortOrder"`
	RoutingStrategy string   `json:"routingStrategy"`
	CacheEnabled    bool     `json:"cacheEnabled"`
	CacheTTLSeconds int      `json:"cacheTtlSeconds"`
	RiskTags        []string `json:"riskTags"`
	IgnoredRiskTags []string `json:"ignoredRiskTags"`
}

// registerRuleRoutes 在管理分组下注册别名与 MCP 级屏蔽规则管理端点。
func (r *Router) registerRuleRoutes(g *gin.RouterGroup) {
	// 别名规则：规则独立管理，作用范围支持全部上游或指定多个上游。
	g.GET("/aliases", r.listAliases)
	g.POST("/aliases", r.createAlias)
	g.PUT("/aliases/:ruleId", r.updateAlias)
	g.DELETE("/aliases/:ruleId", r.deleteAlias)

	// MCP 级屏蔽规则。
	g.GET("/filters", r.listMCPFilters)
	g.POST("/filters", r.createMCPFilter)
	g.PUT("/filters/:ruleId", r.updateMCPFilter)
	g.POST("/filters/:ruleId/enable", r.enableMCPFilter)
	g.POST("/filters/:ruleId/disable", r.disableMCPFilter)
	g.DELETE("/filters/:ruleId", r.deleteMCPFilter)

	// 工具策略规则：按对外工具名动态匹配，覆盖路由/缓存/提示标签。
	g.GET("/tool-policies", r.listToolPolicies)
	g.POST("/tool-policies", r.createToolPolicy)
	g.PUT("/tool-policies/:ruleId", r.updateToolPolicy)
	g.POST("/tool-policies/:ruleId/enable", r.enableToolPolicy)
	g.POST("/tool-policies/:ruleId/disable", r.disableToolPolicy)
	g.DELETE("/tool-policies/:ruleId", r.deleteToolPolicy)
}

// listAliases 返回全部别名规则（Req 8.1）。
func (r *Router) listAliases(c *gin.Context) {
	if r.aliasStore == nil {
		respondServiceUnavailable(c, "别名规则服务未就绪")
		return
	}
	rules, err := r.aliasStore.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"aliases": rules})
}

// createAlias 创建一条别名规则（Req 8.1、8.9）。
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
	scopeType, upstreamIDs, err := normalizeHTTPScope(req.ScopeType, req.UpstreamIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	rule := domain.AliasRule{
		ScopeType:   scopeType,
		UpstreamIDs: upstreamIDs,
		Pattern:     req.Pattern,
		IsRegex:     req.IsRegex,
		TargetName:  req.TargetName,
		TargetDesc:  req.TargetDesc,
		SortOrder:   req.SortOrder,
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
	r.invalidateToolSetCache()
	r.recordCreate(c, audit.ResourceRule, req.Pattern)
	respondCreated(c, created)
}

// updateAlias 更新一条别名规则（Req 8.1、8.9）。
func (r *Router) updateAlias(c *gin.Context) {
	if r.aliasStore == nil || r.ruleValidator == nil {
		respondServiceUnavailable(c, "别名规则服务未就绪")
		return
	}
	var req aliasRuleRequest
	if !bindJSON(c, &req) {
		return
	}
	scopeType, upstreamIDs, err := normalizeHTTPScope(req.ScopeType, req.UpstreamIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	rule := domain.AliasRule{
		ID:          c.Param("ruleId"),
		ScopeType:   scopeType,
		UpstreamIDs: upstreamIDs,
		Pattern:     req.Pattern,
		IsRegex:     req.IsRegex,
		TargetName:  req.TargetName,
		TargetDesc:  req.TargetDesc,
		SortOrder:   req.SortOrder,
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
	r.invalidateToolSetCache()
	r.recordUpdate(c, audit.ResourceRule, c.Param("ruleId"))
	respondOK(c, updated)
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
	r.invalidateToolSetCache()
	r.recordDelete(c, audit.ResourceRule, c.Param("ruleId"))
	respondNoContent(c)
}

// listMCPFilters 返回全部 MCP 级屏蔽规则（Req 9.1）。
func (r *Router) listMCPFilters(c *gin.Context) {
	if r.filterMCPStore == nil {
		respondServiceUnavailable(c, "屏蔽规则服务未就绪")
		return
	}
	rows, err := r.filterMCPStore.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"filters": rows})
}

// createMCPFilter 创建一条 MCP 级屏蔽规则（Req 9.1、9.2、9.9）。
//
// 流程：字段级校验（复用 ValidateFilter）→ 数量上限校验（全部 MCP 级屏蔽规则至多 100 条，
// 复用 domain.ValidateFilterCount）→ 按请求中的排序值持久化。
func (r *Router) createMCPFilter(c *gin.Context) {
	if r.filterMCPStore == nil || r.ruleValidator == nil {
		respondServiceUnavailable(c, "屏蔽规则服务未就绪")
		return
	}
	var req filterRuleRequest
	if !bindJSON(c, &req) {
		return
	}
	scopeType, upstreamIDs, err := normalizeHTTPScope(req.ScopeType, req.UpstreamIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	rule := domain.FilterRule{
		ScopeType:   scopeType,
		UpstreamIDs: upstreamIDs,
		Pattern:     req.Pattern,
		IsRegex:     req.IsRegex,
		Enabled:     req.Enabled,
		SortOrder:   req.SortOrder,
	}
	// 1) 字段级校验（正则合法性、模式长度 1-200），不通过即拒绝且不持久化（Req 9.7、9.8）。
	if err := r.ruleValidator.ValidateFilter(rule); err != nil {
		respondError(c, err)
		return
	}
	current, err := r.filterMCPStore.Count(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	if err := domain.ValidateFilterCount(current); err != nil {
		respondError(c, err)
		return
	}
	created, err := r.filterMCPStore.Create(c.Request.Context(), store.FilterMCPRow{
		FilterRule: rule,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	r.invalidateToolSetCache()
	r.recordCreate(c, audit.ResourceRule, req.Pattern)
	respondCreated(c, created)
}

// updateMCPFilter 更新一条 MCP 级屏蔽规则（Req 9.1、9.7、9.8）。
func (r *Router) updateMCPFilter(c *gin.Context) {
	if r.filterMCPStore == nil || r.ruleValidator == nil {
		respondServiceUnavailable(c, "屏蔽规则服务未就绪")
		return
	}
	var req filterRuleRequest
	if !bindJSON(c, &req) {
		return
	}
	scopeType, upstreamIDs, err := normalizeHTTPScope(req.ScopeType, req.UpstreamIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	rule := domain.FilterRule{
		ID:          c.Param("ruleId"),
		ScopeType:   scopeType,
		UpstreamIDs: upstreamIDs,
		Pattern:     req.Pattern,
		IsRegex:     req.IsRegex,
		Enabled:     req.Enabled,
		SortOrder:   req.SortOrder,
	}
	if err := r.ruleValidator.ValidateFilter(rule); err != nil {
		respondError(c, err)
		return
	}
	updated, err := r.filterMCPStore.Update(c.Request.Context(), store.FilterMCPRow{
		FilterRule: rule,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	r.invalidateToolSetCache()
	r.recordUpdate(c, audit.ResourceRule, c.Param("ruleId"))
	respondOK(c, updated)
}

func normalizeHTTPScope(scopeType string, upstreamIDs []string) (string, []string, error) {
	switch scopeType {
	case "", "all":
		return "all", nil, nil
	case "upstreams":
		if len(upstreamIDs) == 0 {
			return "", nil, domain.NewValidationError("作用范围校验失败", map[string]string{"upstreamIds": "选择指定上游时至少选择一个上游 MCP"})
		}
		return "upstreams", upstreamIDs, nil
	default:
		return "", nil, domain.NewValidationError("作用范围校验失败", map[string]string{"scopeType": "作用范围只能是 all 或 upstreams"})
	}
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
	r.invalidateToolSetCache()
	r.recordUpdate(c, audit.ResourceRule, c.Param("ruleId"))
	respondOK(c, gin.H{"id": c.Param("ruleId"), "enabled": enabled})
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
	r.invalidateToolSetCache()
	r.recordDelete(c, audit.ResourceRule, c.Param("ruleId"))
	respondNoContent(c)
}

func (r *Router) listToolPolicies(c *gin.Context) {
	if r.toolPolicyStore == nil {
		respondServiceUnavailable(c, "工具策略服务未就绪")
		return
	}
	rules, err := r.toolPolicyStore.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"toolPolicies": rules})
}

func (r *Router) createToolPolicy(c *gin.Context) {
	if r.toolPolicyStore == nil || r.ruleValidator == nil {
		respondServiceUnavailable(c, "工具策略服务未就绪")
		return
	}
	var req toolPolicyRuleRequest
	if !bindJSON(c, &req) {
		return
	}
	rule := toolPolicyFromRequest(req)
	if err := r.ruleValidator.ValidateToolPolicy(rule); err != nil {
		respondError(c, err)
		return
	}
	current, err := r.toolPolicyStore.Count(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	if current >= domain.MaxToolPolicyRules {
		respondError(c, domain.NewValidationError("工具策略规则数量已达上限", map[string]string{
			"count": "工具策略规则最多维护 100 条",
		}))
		return
	}
	created, err := r.toolPolicyStore.Create(c.Request.Context(), rule)
	if err != nil {
		respondError(c, err)
		return
	}
	r.invalidateToolResultCache()
	r.recordCreate(c, audit.ResourceRule, req.Pattern)
	respondCreated(c, created)
}

func (r *Router) updateToolPolicy(c *gin.Context) {
	if r.toolPolicyStore == nil || r.ruleValidator == nil {
		respondServiceUnavailable(c, "工具策略服务未就绪")
		return
	}
	var req toolPolicyRuleRequest
	if !bindJSON(c, &req) {
		return
	}
	rule := toolPolicyFromRequest(req)
	rule.ID = c.Param("ruleId")
	if err := r.ruleValidator.ValidateToolPolicy(rule); err != nil {
		respondError(c, err)
		return
	}
	updated, err := r.toolPolicyStore.Update(c.Request.Context(), rule)
	if err != nil {
		respondError(c, err)
		return
	}
	r.invalidateToolResultCache()
	r.recordUpdate(c, audit.ResourceRule, c.Param("ruleId"))
	respondOK(c, updated)
}

func (r *Router) enableToolPolicy(c *gin.Context) {
	r.setToolPolicyEnabled(c, true)
}

func (r *Router) disableToolPolicy(c *gin.Context) {
	r.setToolPolicyEnabled(c, false)
}

func (r *Router) setToolPolicyEnabled(c *gin.Context, enabled bool) {
	if r.toolPolicyStore == nil {
		respondServiceUnavailable(c, "工具策略服务未就绪")
		return
	}
	if err := r.toolPolicyStore.SetEnabled(c.Request.Context(), c.Param("ruleId"), enabled); err != nil {
		respondError(c, err)
		return
	}
	r.invalidateToolResultCache()
	r.recordUpdate(c, audit.ResourceRule, c.Param("ruleId"))
	respondOK(c, gin.H{"id": c.Param("ruleId"), "enabled": enabled})
}

func (r *Router) deleteToolPolicy(c *gin.Context) {
	if r.toolPolicyStore == nil {
		respondServiceUnavailable(c, "工具策略服务未就绪")
		return
	}
	if err := r.toolPolicyStore.Delete(c.Request.Context(), c.Param("ruleId")); err != nil {
		respondError(c, err)
		return
	}
	r.invalidateToolResultCache()
	r.recordDelete(c, audit.ResourceRule, c.Param("ruleId"))
	respondNoContent(c)
}

func toolPolicyFromRequest(req toolPolicyRuleRequest) domain.ToolPolicyRule {
	return domain.ToolPolicyRule{
		Pattern:         req.Pattern,
		IsRegex:         req.IsRegex,
		Enabled:         req.Enabled,
		SortOrder:       req.SortOrder,
		RoutingStrategy: domain.ToolRoutingStrategy(req.RoutingStrategy),
		CacheEnabled:    req.CacheEnabled,
		CacheTTLSeconds: req.CacheTTLSeconds,
		RiskTags:        req.RiskTags,
		IgnoredRiskTags: req.IgnoredRiskTags,
	}
}
