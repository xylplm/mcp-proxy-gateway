package httpapi

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件实现审计日志分页查询端点（Req 22.4、17.5）：
//
//   GET /api/admin/audit?page=&pageSize=   按发生时间倒序分页返回审计记录及总数
//
// 分页参数通过查询参数 page/pageSize 传入，均为可选整数：
//   - page 缺省或非正由审计服务归正为第 1 页；
//   - pageSize 缺省或非正由审计服务取配置默认值（audit.page_size_default），
//     越界值由其收敛到 [1,200]（Req 22.4）。
// 本层仅负责整数解析（非整数返回字段级 VALIDATION），倒序与收敛由审计服务保证。

// registerAuditRoutes 在管理分组下注册审计日志查询端点（Req 22.4）。
func (r *Router) registerAuditRoutes(g *gin.RouterGroup) {
	g.GET("/audit", r.queryAudit)
}

// queryAudit 按发生时间倒序分页返回审计记录（Req 22.4）。
func (r *Router) queryAudit(c *gin.Context) {
	if r.audit == nil {
		respondServiceUnavailable(c, "审计查询服务未就绪")
		return
	}
	page, ok := parseOptionalInt(c, "page")
	if !ok {
		return
	}
	pageSize, ok := parseOptionalInt(c, "pageSize")
	if !ok {
		return
	}
	result, err := r.audit.List(c.Request.Context(), page, pageSize)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"records":  result.Records,
		"page":     result.Page,
		"pageSize": result.PageSize,
		"total":    result.Total,
	})
}

// parseOptionalInt 解析可选整数查询参数；缺省返回 0（交由下层收敛），非整数则以
// 字段级 VALIDATION 响应并返回 ok=false（调用方据此中止）。
func parseOptionalInt(c *gin.Context, name string) (int, bool) {
	v := c.Query(name)
	if v == "" {
		return 0, true
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		respondError(c, domain.NewValidationError("分页参数非法", map[string]string{
			name: "需为整数",
		}))
		return 0, false
	}
	return n, true
}
