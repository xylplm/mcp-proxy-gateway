package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/mcpapi"
)

type gatewayToolView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// registerToolRoutes 注册管理台工具列表查询端点。
func (r *Router) registerToolRoutes(g *gin.RouterGroup) {
	t := g.Group("/tools")
	t.GET("/summary", r.getAggregatedToolSummary)
	t.GET("/aggregated", r.listAggregatedTools)
}

// getAggregatedToolSummary 返回当前全局视角下的聚合工具摘要，避免概览页拉取完整详情。
func (r *Router) getAggregatedToolSummary(c *gin.Context) {
	if r.aggregation == nil {
		respondServiceUnavailable(c, "聚合工具服务未就绪")
		return
	}
	tools, err := r.aggregation.BuildToolSet(c.Request.Context(), "")
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"count": len(tools)})
}

// listAggregatedTools 返回当前全局视角下的真实聚合工具列表。
func (r *Router) listAggregatedTools(c *gin.Context) {
	if r.aggregation == nil {
		respondServiceUnavailable(c, "聚合工具服务未就绪")
		return
	}
	details, err := r.aggregation.BuildToolDetails(c.Request.Context(), "")
	if err != nil {
		respondError(c, err)
		return
	}
	tools := make([]domain.ToolDef, 0, len(details))
	for _, detail := range details {
		tools = append(tools, detail.Tool)
	}
	respondOK(c, gin.H{
		"tools":        tools,
		"toolDetails":  details,
		"count":        len(tools),
		"gatewayTools": gatewayTools(),
	})
}

func gatewayTools() []gatewayToolView {
	return []gatewayToolView{
		{
			Name:        mcpapi.GatewayToolListTools,
			Description: mcpapi.GatewayToolListToolsDescription,
		},
		{
			Name:        mcpapi.GatewayToolSearchTools,
			Description: mcpapi.GatewayToolSearchToolsDescription,
		},
		{
			Name:        mcpapi.GatewayToolGetTool,
			Description: mcpapi.GatewayToolGetToolDescription,
		},
		{
			Name:        mcpapi.GatewayToolCallTool,
			Description: mcpapi.GatewayToolCallToolDescription,
		},
	}
}
