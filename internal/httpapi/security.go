package httpapi

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

func (r *Router) registerSecurityRoutes(g *gin.RouterGroup) {
	sec := g.Group("/security")
	sec.GET("/summary", r.securitySummary)
	sec.GET("/events", r.listSecurityEvents)
	sec.GET("/blocks", r.listSecurityBlocks)
	sec.POST("/blocks/:id/release", r.releaseSecurityBlock)
}

func (r *Router) securitySummary(c *gin.Context) {
	if r.security == nil {
		respondServiceUnavailable(c, "安全中心服务未就绪")
		return
	}
	summary, err := r.security.Summary(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, summary)
}

func (r *Router) listSecurityEvents(c *gin.Context) {
	if r.security == nil {
		respondServiceUnavailable(c, "安全中心服务未就绪")
		return
	}
	limit, ok := parseSecurityLimit(c)
	if !ok {
		return
	}
	events, err := r.security.ListEvents(c.Request.Context(), store.SecurityEventQuery{
		EventType:   c.Query("eventType"),
		ClientIP:    c.Query("clientIP"),
		APIKeyID:    c.Query("apiKeyID"),
		SubjectType: c.Query("subjectType"),
		Limit:       limit,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"events": events})
}

func (r *Router) listSecurityBlocks(c *gin.Context) {
	if r.security == nil {
		respondServiceUnavailable(c, "安全中心服务未就绪")
		return
	}
	limit, ok := parseSecurityLimit(c)
	if !ok {
		return
	}
	blocks, err := r.security.ListBlocks(c.Request.Context(), store.SecurityBlockQuery{
		Status: c.Query("status"),
		Limit:  limit,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"blocks": blocks})
}

func (r *Router) releaseSecurityBlock(c *gin.Context) {
	if r.security == nil {
		respondServiceUnavailable(c, "安全中心服务未就绪")
		return
	}
	block, err := r.security.ReleaseBlock(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	r.recordUpdate(c, audit.ResourceSetting, "security:block:"+block.ID)
	respondOK(c, block)
}

func parseSecurityLimit(c *gin.Context) (int, bool) {
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			respondError(c, domain.NewValidationError("查询参数非法", map[string]string{"limit": "需为整数"}))
			return 0, false
		}
		limit = n
	}
	return limit, true
}
