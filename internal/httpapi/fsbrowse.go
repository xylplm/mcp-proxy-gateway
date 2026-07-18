package httpapi

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 路径浏览 API：仅管理员可读，且只能在 data/runtime/global_file_roots/上下文路径内列举。
// 不做写操作，不返回文件内容。

func (r *Router) registerFSBrowseRoutes(g *gin.RouterGroup) {
	fs := g.Group("/fs")
	fs.GET("/roots", r.fsBrowseRoots)
	fs.GET("/list", r.fsBrowseList)
	fs.GET("/stat", r.fsBrowseStat)
}

type fsContextQuery struct {
	// Context 为逗号分隔的额外上下文路径（表单 cwd / 文件根等）。
	Context string `form:"context"`
	Path    string `form:"path"`
	Mode    string `form:"mode"`
	Limit   string `form:"limit"`
}

func parseContextRoots(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (r *Router) fsBrowseRoots(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	var q fsContextQuery
	_ = c.ShouldBindQuery(&q)
	respondOK(c, r.runtimeEnv.BrowseRoots(parseContextRoots(q.Context)))
}

func (r *Router) fsBrowseList(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	var q fsContextQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		respondError(c, domain.NewValidationError("查询参数非法", map[string]string{"query": err.Error()}))
		return
	}
	path := strings.TrimSpace(q.Path)
	if path == "" {
		respondError(c, domain.NewValidationError("path 不能为空", map[string]string{"path": "必填"}))
		return
	}
	limit := 0
	if strings.TrimSpace(q.Limit) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(q.Limit))
		if err != nil {
			respondError(c, domain.NewValidationError("limit 必须为整数", map[string]string{"limit": "格式非法"}))
			return
		}
		limit = n
	}
	result, err := r.runtimeEnv.BrowseList(path, q.Mode, limit, parseContextRoots(q.Context))
	if err != nil {
		respondError(c, mapBrowseError(err, "path"))
		return
	}
	respondOK(c, result)
}

func (r *Router) fsBrowseStat(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	var q fsContextQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		respondError(c, domain.NewValidationError("查询参数非法", map[string]string{"query": err.Error()}))
		return
	}
	path := strings.TrimSpace(q.Path)
	if path == "" {
		respondError(c, domain.NewValidationError("path 不能为空", map[string]string{"path": "必填"}))
		return
	}
	result, err := r.runtimeEnv.BrowseStat(path, parseContextRoots(q.Context))
	if err != nil {
		respondError(c, mapBrowseError(err, "path"))
		return
	}
	respondOK(c, result)
}

func mapBrowseError(err error, field string) error {
	if err == nil {
		return domain.NewError(domain.CodeInternal, "路径浏览失败")
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "不存在"):
		return domain.NewError(domain.CodeNotFound, msg)
	case strings.Contains(msg, "允许浏览范围") || strings.Contains(msg, "允许范围内") || strings.Contains(msg, "没有读取权限"):
		return domain.NewError(domain.CodeForbidden, msg)
	case strings.Contains(msg, "不能为空") ||
		strings.Contains(msg, "非法") ||
		strings.Contains(msg, "绝对路径") ||
		strings.Contains(msg, "不是目录") ||
		strings.Contains(msg, "符号链接"):
		return domain.NewValidationError(msg, map[string]string{field: msg})
	default:
		return domain.NewValidationError(msg, map[string]string{field: msg})
	}
}
