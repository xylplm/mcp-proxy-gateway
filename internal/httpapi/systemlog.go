package httpapi

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/syslog"
)

func (r *Router) registerSystemLogRoutes(g *gin.RouterGroup) {
	g.GET("/system-logs", r.querySystemLogs)
	g.DELETE("/system-logs", r.clearSystemLogs)
}

func (r *Router) querySystemLogs(c *gin.Context) {
	if r.systemLogs == nil {
		respondServiceUnavailable(c, "系统日志服务未就绪")
		return
	}

	limit := 200
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			respondError(c, domain.NewValidationError("系统日志条数参数非法", map[string]string{
				"limit": "需为非负整数",
			}))
			return
		}
		limit = n
	}

	var afterID int64
	if raw := c.Query("afterId"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			respondError(c, domain.NewValidationError("系统日志游标参数非法", map[string]string{
				"afterId": "需为非负整数",
			}))
			return
		}
		afterID = n
	}

	level := syslog.NormalizeLevel(c.Query("level"))
	if !syslog.ValidLevel(level) {
		respondError(c, domain.NewValidationError("系统日志级别参数非法", map[string]string{
			"level": "仅支持 debug、info、warn、error",
		}))
		return
	}

	logs := r.systemLogs.List(afterID, level, limit)
	respondOK(c, gin.H{"logs": logs})
}

func (r *Router) clearSystemLogs(c *gin.Context) {
	if r.systemLogs == nil {
		respondServiceUnavailable(c, "系统日志服务未就绪")
		return
	}
	deleted := r.systemLogs.Clear()
	respondOK(c, gin.H{"deleted": deleted})
}
