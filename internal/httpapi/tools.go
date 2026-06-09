package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/mcpapi"
)

type gatewayToolView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// registerToolRoutes 注册管理台工具列表查询端点。
func (r *Router) registerToolRoutes(g *gin.RouterGroup) {
	t := g.Group("/tools")
	t.GET("/aggregated", r.listAggregatedTools)
}

// listAggregatedTools 返回当前全局视角下的真实聚合工具列表。
func (r *Router) listAggregatedTools(c *gin.Context) {
	if r.aggregation == nil {
		respondServiceUnavailable(c, "聚合工具服务未就绪")
		return
	}
	tools, err := r.aggregation.BuildToolSet(c.Request.Context(), "")
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{
		"tools":        tools,
		"count":        len(tools),
		"gatewayTools": gatewayTools(),
	})
}

func gatewayTools() []gatewayToolView {
	return []gatewayToolView{
		{
			Name:        mcpapi.GatewayToolListTools,
			Description: "分页列出当前可见的聚合工具摘要。",
		},
		{
			Name:        mcpapi.GatewayToolSearchTools,
			Description: "按关键字检索可见聚合工具。",
		},
		{
			Name:        mcpapi.GatewayToolGetTool,
			Description: "获取单个聚合工具的完整定义。",
		},
		{
			Name:        mcpapi.GatewayToolCallTool,
			Description: "按名称调用具体聚合工具并返回执行结果。",
		},
	}
}
