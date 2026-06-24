package httpapi

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/aggregation"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/mcpapi"
)

type gatewayToolView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type toolPlaygroundRequest struct {
	APIKeyID string          `json:"apiKeyId"`
	Name     string          `json:"name"`
	Args     json.RawMessage `json:"args"`
}

type toolPlaygroundResponse struct {
	ToolName   string          `json:"toolName"`
	APIKeyID   string          `json:"apiKeyId,omitempty"`
	LatencyMS  int64           `json:"latencyMs"`
	Success    bool            `json:"success"`
	IsError    bool            `json:"isError"`
	Content    json.RawMessage `json:"content,omitempty"`
	ErrorCode  string          `json:"errorCode,omitempty"`
	Error      string          `json:"error,omitempty"`
	CalledAt   time.Time       `json:"calledAt"`
	FinishedAt time.Time       `json:"finishedAt"`
}

// registerToolRoutes 注册管理台工具列表查询端点。
func (r *Router) registerToolRoutes(g *gin.RouterGroup) {
	t := g.Group("/tools")
	t.GET("/summary", r.getAggregatedToolSummary)
	t.GET("/aggregated", r.listAggregatedTools)
	t.POST("/playground", r.invokeToolPlayground)
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
	apiKeyID := c.Query("apiKeyId")
	details, err := r.aggregation.BuildToolDetails(c.Request.Context(), apiKeyID)
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

// invokeToolPlayground 按管理台选择的视角发起一次真实工具调用，用于排障与参数验证。
//
// 调用失败（上游不可用、工具不可见、限流等）也返回 200，并把错误信息放入 data，避免调试台
// 把"被调试对象失败"混同为"调试接口请求失败"。只有请求体/字段非法或服务未接线才走统一错误响应。
func (r *Router) invokeToolPlayground(c *gin.Context) {
	if r.aggregation == nil {
		respondServiceUnavailable(c, "聚合工具服务未就绪")
		return
	}
	var req toolPlaygroundRequest
	if !bindJSON(c, &req) {
		return
	}
	if req.Name == "" {
		respondError(c, domain.NewValidationError("调试参数校验失败", map[string]string{
			"name": "请选择要调用的工具",
		}))
		return
	}
	if len(req.Args) == 0 {
		req.Args = json.RawMessage(`{}`)
	}
	var argsObj map[string]any
	if err := json.Unmarshal(req.Args, &argsObj); err != nil {
		respondError(c, domain.NewValidationError("调试参数校验失败", map[string]string{
			"args": "必须为合法 JSON 对象",
		}))
		return
	}
	if argsObj == nil {
		respondError(c, domain.NewValidationError("调试参数校验失败", map[string]string{
			"args": "必须为合法 JSON 对象",
		}))
		return
	}

	ctx := aggregation.ContextWithMode(c.Request.Context(), "full")
	ctx = aggregation.ContextWithSource(ctx, "api")
	startedAt := time.Now()
	result, err := r.aggregation.InvokeTool(ctx, req.APIKeyID, req.Name, req.Args)
	finishedAt := time.Now()

	resp := toolPlaygroundResponse{
		ToolName:   req.Name,
		APIKeyID:   req.APIKeyID,
		LatencyMS:  finishedAt.Sub(startedAt).Milliseconds(),
		Success:    err == nil && !result.IsError,
		IsError:    result.IsError,
		Content:    result.Content,
		CalledAt:   startedAt.UTC(),
		FinishedAt: finishedAt.UTC(),
	}
	if err != nil {
		resp.Error = err.Error()
		var apiErr *domain.APIError
		if errors.As(err, &apiErr) {
			resp.ErrorCode = string(apiErr.Code)
			resp.Error = apiErr.Message
		}
	}
	respondOK(c, resp)
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
