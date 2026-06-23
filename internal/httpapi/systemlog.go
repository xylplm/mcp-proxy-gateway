package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/syslog"
)

func (r *Router) registerSystemLogRoutes(g *gin.RouterGroup) {
	g.GET("/system-logs", r.querySystemLogs)
	g.GET("/system-logs/export", r.exportSystemLogs)
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

	level, ok := parseSystemLogLevel(c)
	if !ok {
		return
	}

	logs := r.systemLogs.List(afterID, level, limit)
	respondOK(c, gin.H{"logs": logs})
}

func (r *Router) exportSystemLogs(c *gin.Context) {
	if r.systemLogs == nil {
		respondServiceUnavailable(c, "系统日志服务未就绪")
		return
	}

	level, ok := parseSystemLogLevel(c)
	if !ok {
		return
	}
	logs := r.systemLogs.Export(level)
	data, err := json.MarshalIndent(logs, "", "  ")
	if err != nil {
		respondError(c, err)
		return
	}

	suffix := ""
	if level != "" {
		suffix = "-" + level
	}
	filename := "mpg-system-logs" + suffix + "-" + time.Now().Format("20060102-150405") + ".json"
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "application/json; charset=utf-8", data)
}

func (r *Router) clearSystemLogs(c *gin.Context) {
	if r.systemLogs == nil {
		respondServiceUnavailable(c, "系统日志服务未就绪")
		return
	}
	deleted := r.systemLogs.Clear()
	r.recordUpdate(c, audit.ResourceSetting, "system-logs:clear")
	respondOK(c, gin.H{"deleted": deleted})
}

func parseSystemLogLevel(c *gin.Context) (string, bool) {
	level := syslog.NormalizeLevel(c.Query("level"))
	if !syslog.ValidLevel(level) {
		respondError(c, domain.NewValidationError("系统日志级别参数非法", map[string]string{
			"level": "仅支持 debug、info、warn、error",
		}))
		return "", false
	}
	return level, true
}
