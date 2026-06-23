package httpapi

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
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
	g.GET("/audit/export", r.exportAudit)
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
	query, ok := parseAuditQuery(c)
	if !ok {
		return
	}
	result, err := r.audit.List(c.Request.Context(), page, pageSize, query)
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{
		"records":  result.Records,
		"page":     result.Page,
		"pageSize": result.PageSize,
		"total":    result.Total,
	})
}

func (r *Router) exportAudit(c *gin.Context) {
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
	query, ok := parseAuditQuery(c)
	if !ok {
		return
	}
	result, err := r.audit.List(c.Request.Context(), page, pageSize, query)
	if err != nil {
		respondError(c, err)
		return
	}
	respondJSONDownload(c, "mpg-audit-"+time.Now().Format("20060102-150405")+".json", result.Records)
}

func parseAuditQuery(c *gin.Context) (audit.Query, bool) {
	var query audit.Query
	query.EventType = c.Query("eventType")
	if !validAuditEventType(query.EventType) {
		respondError(c, domain.NewValidationError("审计事件类型参数非法", map[string]string{
			"eventType": "仅支持 login、create、update、delete、access_denied",
		}))
		return audit.Query{}, false
	}

	start, ok := parseOptionalRFC3339(c, "start")
	if !ok {
		return audit.Query{}, false
	}
	end, ok := parseOptionalRFC3339(c, "end")
	if !ok {
		return audit.Query{}, false
	}
	if !start.IsZero() && !end.IsZero() && start.After(end) {
		respondError(c, domain.NewValidationError("审计时间范围参数非法", map[string]string{
			"start": "开始时间不得晚于结束时间",
		}))
		return audit.Query{}, false
	}
	query.Start = start
	query.End = end
	return query, true
}

func validAuditEventType(eventType string) bool {
	switch eventType {
	case "", store.AuditEventLogin, store.AuditEventCreate, store.AuditEventUpdate, store.AuditEventDelete, store.AuditEventAccessDenied:
		return true
	default:
		return false
	}
}

func parseOptionalRFC3339(c *gin.Context, name string) (time.Time, bool) {
	value := c.Query(name)
	if value == "" {
		return time.Time{}, true
	}
	t, err := time.Parse(timeLayout, value)
	if err != nil {
		respondError(c, domain.NewValidationError("时间参数非法", map[string]string{
			name: "时间格式需为 RFC3339",
		}))
		return time.Time{}, false
	}
	return t, true
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
