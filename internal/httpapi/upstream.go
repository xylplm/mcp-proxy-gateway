package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件实现上游 MCP 管理端点（Req 2.1、3.1、3.4、5.6、6.4）：
//
//   GET    /api/admin/upstreams              列出全部上游及连接状态
//   POST   /api/admin/upstreams              创建上游
//   POST   /api/admin/upstreams/test         临时测试上游连接并预览工具
//   PUT    /api/admin/upstreams/:id          更新上游
//   DELETE /api/admin/upstreams/:id          删除上游
//   POST   /api/admin/upstreams/:id/enable   启用上游
//   POST   /api/admin/upstreams/:id/disable  停用上游
//   POST   /api/admin/upstreams/reorder      重排序上游
//   POST   /api/admin/upstreams/:id/reconnect 手动重连上游
//   POST   /api/admin/upstreams/:id/refresh  手动刷新工具列表
//
// 凭证（Credential）以明文存储，创建/更新入参与列表/详情响应均携带明文，便于前端编辑回显。

// upstreamConfigRequest 为创建/更新上游 MCP 的请求体。
//
// 字段与 domain.UpstreamConfig 对齐。SortOrder 在创建时可缺省（由应用层置于末尾或按提交值），
// 在更新时沿用提交值。
type upstreamConfigRequest struct {
	// Name 为服务名称，长度需在 1 至 100 个字符之间（Req 2.1、2.2）。
	Name string `json:"name"`
	// Tags 为用户自定义标签，用于管理台分组与识别。
	Tags []string `json:"tags"`
	// Transport 为传输类型（stdio/sse/streamable-http/websocket）。
	Transport domain.TransportType `json:"transport"`
	// ConnParams 为传输类型相关的连接参数。
	ConnParams map[string]any `json:"connParams"`
	// Credential 为鉴权凭证明文，明文存储，响应原样回显。
	Credential string `json:"credential"`
	// Enabled 表示该上游是否启用并参与聚合。
	Enabled bool `json:"enabled"`
	// SortOrder 为排序顺序。
	SortOrder int `json:"sortOrder"`
	// AutoSync 表示是否对该上游开启工具列表自动同步。
	AutoSync bool `json:"autoSync"`
	// RateLimits 表示该上游的调用限流与额度配置。
	RateLimits domain.UpstreamRateLimits `json:"rateLimits"`
}

// toConfig 将请求体转换为领域配置。
func (req upstreamConfigRequest) toConfig() domain.UpstreamConfig {
	return domain.UpstreamConfig{
		Name:       req.Name,
		Tags:       req.Tags,
		Transport:  req.Transport,
		ConnParams: req.ConnParams,
		Credential: req.Credential,
		Enabled:    req.Enabled,
		SortOrder:  req.SortOrder,
		AutoSync:   req.AutoSync,
		RateLimits: req.RateLimits,
	}
}

// reorderRequest 为上游重排序请求体（Req 3.4、3.5）。
type reorderRequest struct {
	// OrderedIDs 为新的上游标识顺序，须为已注册标识的恰好一次排列。
	OrderedIDs []string `json:"orderedIds"`
}

type upstreamToolSummary struct {
	ID            string                    `json:"id"`
	Count         int                       `json:"count"`
	UpdatedAt     *time.Time                `json:"updatedAt"`
	ChangeSummary *domain.ToolChangeSummary `json:"changeSummary,omitempty"`
}

// registerUpstreamRoutes 在管理分组下注册上游 MCP 管理端点。
func (r *Router) registerUpstreamRoutes(g *gin.RouterGroup) {
	ups := g.Group("/upstreams")
	ups.GET("", r.listUpstreams)
	ups.POST("", r.createUpstream)
	ups.POST("/test", r.testUpstream)
	ups.POST("/import/preview", r.previewUpstreamImport)
	ups.POST("/import", r.importUpstreams)
	ups.GET("/export/mcp-json", r.exportUpstreamsMCPJSON)
	ups.PUT("/:id", r.updateUpstream)
	ups.DELETE("/:id", r.deleteUpstream)
	ups.POST("/:id/enable", r.enableUpstream)
	ups.POST("/:id/disable", r.disableUpstream)
	ups.POST("/reorder", r.reorderUpstreams)
	ups.POST("/:id/reconnect", r.reconnectUpstream)
	ups.POST("/:id/refresh", r.refreshUpstream)
	ups.GET("/tool-summaries", r.listUpstreamToolSummaries)
	ups.GET("/:id/tools", r.listUpstreamTools)
}

// listUpstreams 返回全部上游 MCP 及其当前连接状态（Req 2.3、2.8）。
func (r *Router) listUpstreams(c *gin.Context) {
	if r.upstream == nil {
		respondServiceUnavailable(c, "上游管理服务未就绪")
		return
	}
	ups, err := r.upstream.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"upstreams": ups})
}

// testUpstream 对未落库配置建立临时会话并拉取工具列表，用于创建/编辑前排障。
func (r *Router) testUpstream(c *gin.Context) {
	if r.upstreamTester == nil {
		respondServiceUnavailable(c, "上游测试服务未就绪")
		return
	}
	var req upstreamConfigRequest
	if !bindJSON(c, &req) {
		return
	}
	result, err := r.upstreamTester.Test(c.Request.Context(), req.toConfig())
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, result)
}

// createUpstream 创建上游 MCP 服务（Req 2.1）。
func (r *Router) createUpstream(c *gin.Context) {
	if r.upstream == nil {
		respondServiceUnavailable(c, "上游管理服务未就绪")
		return
	}
	var req upstreamConfigRequest
	if !bindJSON(c, &req) {
		return
	}
	r.scriptRefMu.Lock()
	defer r.scriptRefMu.Unlock()
	up, err := r.upstream.Create(c.Request.Context(), req.toConfig())
	if err != nil {
		respondError(c, err)
		return
	}
	r.recordCreate(c, audit.ResourceUpstream, req.Name)
	respondCreated(c, up)
}

// updateUpstream 更新某个已存在的上游 MCP 配置（Req 2.4）。
func (r *Router) updateUpstream(c *gin.Context) {
	if r.upstream == nil {
		respondServiceUnavailable(c, "上游管理服务未就绪")
		return
	}
	var req upstreamConfigRequest
	if !bindJSON(c, &req) {
		return
	}
	cfg := req.toConfig()

	r.scriptRefMu.Lock()
	defer r.scriptRefMu.Unlock()
	up, err := r.upstream.Update(c.Request.Context(), c.Param("id"), cfg)
	if err != nil {
		respondError(c, err)
		return
	}
	r.recordUpdate(c, audit.ResourceUpstream, req.Name)
	respondOK(c, up)
}

// deleteUpstream 删除某个上游 MCP 服务并级联清理（Req 2.5）。
func (r *Router) deleteUpstream(c *gin.Context) {
	if r.upstream == nil {
		respondServiceUnavailable(c, "上游管理服务未就绪")
		return
	}
	if err := r.upstream.Delete(c.Request.Context(), c.Param("id")); err != nil {
		respondError(c, err)
		return
	}
	r.recordDelete(c, audit.ResourceUpstream, c.Param("id"))
	respondNoContent(c)
}

// enableUpstream 启用某个上游 MCP 服务（Req 3.1）。
func (r *Router) enableUpstream(c *gin.Context) {
	r.setUpstreamEnabled(c, true)
}

// disableUpstream 停用某个上游 MCP 服务（Req 3.2）。
func (r *Router) disableUpstream(c *gin.Context) {
	r.setUpstreamEnabled(c, false)
}

// setUpstreamEnabled 是启用/停用上游的共用实现。
func (r *Router) setUpstreamEnabled(c *gin.Context, enabled bool) {
	if r.upstream == nil {
		respondServiceUnavailable(c, "上游管理服务未就绪")
		return
	}
	if err := r.upstream.SetEnabled(c.Request.Context(), c.Param("id"), enabled); err != nil {
		respondError(c, err)
		return
	}
	r.recordUpdate(c, audit.ResourceUpstream, c.Param("id"))
	respondOK(c, gin.H{"id": c.Param("id"), "enabled": enabled})
}

// reorderUpstreams 重排序上游 MCP（Req 3.4、3.5）。
func (r *Router) reorderUpstreams(c *gin.Context) {
	if r.upstream == nil {
		respondServiceUnavailable(c, "上游管理服务未就绪")
		return
	}
	var req reorderRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := r.upstream.Reorder(c.Request.Context(), req.OrderedIDs); err != nil {
		respondError(c, err)
		return
	}
	r.recordUpdate(c, audit.ResourceUpstream, "reorder")
	respondOK(c, gin.H{"orderedIds": req.OrderedIDs})
}

// reconnectUpstream 由管理员手动发起重连（Req 5.6）。
func (r *Router) reconnectUpstream(c *gin.Context) {
	if r.upstream == nil {
		respondServiceUnavailable(c, "上游管理服务未就绪")
		return
	}
	if err := r.upstream.Reconnect(c.Request.Context(), c.Param("id")); err != nil {
		respondError(c, err)
		return
	}
	r.recordUpdate(c, audit.ResourceUpstream, c.Param("id"))
	respondOK(c, gin.H{"id": c.Param("id"), "status": "reconnecting"})
}

// refreshUpstream 手动刷新某上游 MCP 的工具列表（Req 6.4、6.5）。
func (r *Router) refreshUpstream(c *gin.Context) {
	if r.refresher == nil {
		respondServiceUnavailable(c, "工具刷新服务未就绪")
		return
	}
	tools, err := r.refresher.Refresh(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"id": c.Param("id"), "tools": tools, "count": len(tools)})
}

// listUpstreamToolSummaries 批量返回全部上游的工具缓存摘要，不触发缓存补拉。
func (r *Router) listUpstreamToolSummaries(c *gin.Context) {
	if r.upstream == nil {
		respondServiceUnavailable(c, "上游管理服务未就绪")
		return
	}
	if r.toolCache == nil {
		respondServiceUnavailable(c, "工具缓存服务未就绪")
		return
	}
	upstreams, err := r.upstream.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	summaries := make([]upstreamToolSummary, 0, len(upstreams))
	for _, up := range upstreams {
		tools, updatedAt, found := r.toolCache.Get(c.Request.Context(), up.ID)
		var updatedAtPtr *time.Time
		if found {
			updatedAtPtr = &updatedAt
		}
		changeSummary, changeFound := r.toolCache.GetChangeSummary(c.Request.Context(), up.ID)
		var changeSummaryPtr *domain.ToolChangeSummary
		if changeFound {
			changeSummaryPtr = &changeSummary
		}
		summaries = append(summaries, upstreamToolSummary{
			ID:            up.ID,
			Count:         len(tools),
			UpdatedAt:     updatedAtPtr,
			ChangeSummary: changeSummaryPtr,
		})
	}
	respondOK(c, gin.H{"summaries": summaries})
}

// listUpstreamTools 返回某上游当前缓存的工具列表；缓存缺失时按需补拉一次。
// 追加 ensure=false 可只读缓存，避免预览类页面触发上游拉取。
func (r *Router) listUpstreamTools(c *gin.Context) {
	if r.toolCache == nil {
		respondServiceUnavailable(c, "工具缓存服务未就绪")
		return
	}
	id := c.Param("id")
	tools, updatedAt, found := r.toolCache.Get(c.Request.Context(), id)
	if !found {
		ensureCache := true
		if raw, ok := c.GetQuery("ensure"); ok {
			ensureCache = raw != "false" && raw != "0"
		}
		if !ensureCache {
			respondOK(c, gin.H{"id": id, "tools": []domain.ToolDef{}, "count": 0, "updatedAt": nil})
			return
		}
		if r.cacheEnsurer == nil {
			respondOK(c, gin.H{"id": id, "tools": []domain.ToolDef{}, "count": 0, "updatedAt": nil})
			return
		}
		if _, err := r.cacheEnsurer.EnsureCached(c.Request.Context(), id); err != nil {
			respondError(c, err)
			return
		}
		tools, updatedAt, found = r.toolCache.Get(c.Request.Context(), id)
		if !found {
			respondOK(c, gin.H{"id": id, "tools": []domain.ToolDef{}, "count": 0, "updatedAt": nil})
			return
		}
	}
	respondOK(c, gin.H{"id": id, "tools": tools, "count": len(tools), "updatedAt": updatedAt})
}

// respondServiceUnavailable 以 503 返回服务未就绪错误，用于依赖未接线时的防御性拒绝。
func respondServiceUnavailable(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusServiceUnavailable, envelope{
		Code:    50300,
		Message: message,
		Data:    nil,
	})
}
