package httpapi

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	rtenv "github.com/myGithub/mcp-proxy-gateway/internal/runtime"
)

type runtimeInstallRequest struct {
	PackageID string `json:"packageId"`
}

type runtimePreflightRequest struct {
	Transport        string                     `json:"transport"`
	Command          string                     `json:"command"`
	Requirements     *rtenv.RuntimeRequirements `json:"requirements"`
	TemplateRuntimes []string                   `json:"templateRuntimes"`
}

func (r *Router) registerRuntimeRoutes(g *gin.RouterGroup) {
	rt := g.Group("/runtime")
	rt.GET("/summary", r.runtimeSummary)
	rt.GET("/catalog", r.runtimeCatalog)
	rt.GET("/tools", r.runtimeTools)
	rt.POST("/preflight", r.runtimePreflight)
	rt.POST("/install/preview", r.runtimeInstallPreview)
	rt.POST("/install", r.runtimeInstall)
	rt.POST("/uninstall", r.runtimeUninstall)
}

func (r *Router) runtimeSummary(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	respondOK(c, r.runtimeEnv.Summary())
}

func (r *Router) runtimeCatalog(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	respondOK(c, r.runtimeEnv.Catalog())
}

func (r *Router) runtimeTools(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	respondOK(c, r.runtimeEnv.KnownToolCatalog())
}

func (r *Router) runtimePreflight(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	var req runtimePreflightRequest
	if !bindJSON(c, &req) {
		return
	}
	result := r.runtimeEnv.Preflight(rtenv.PreflightRequest{
		Transport:        strings.TrimSpace(req.Transport),
		Command:          strings.TrimSpace(req.Command),
		Requirements:     req.Requirements,
		TemplateRuntimes: req.TemplateRuntimes,
	})
	respondOK(c, result)
}

func (r *Router) runtimeInstallPreview(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	var req runtimeInstallRequest
	if !bindJSON(c, &req) {
		return
	}
	pkgID := strings.TrimSpace(req.PackageID)
	if pkgID == "" {
		respondError(c, domain.NewValidationError("packageId 不能为空", map[string]string{
			"packageId": "必填",
		}))
		return
	}
	item, err := r.runtimeEnv.PreviewInstall(pkgID)
	if err != nil {
		respondError(c, domain.NewValidationError(err.Error(), map[string]string{
			"packageId": err.Error(),
		}))
		return
	}
	respondOK(c, item)
}

func (r *Router) runtimeInstall(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	var req runtimeInstallRequest
	if !bindJSON(c, &req) {
		return
	}
	pkgID := strings.TrimSpace(req.PackageID)
	if pkgID == "" {
		respondError(c, domain.NewValidationError("packageId 不能为空", map[string]string{
			"packageId": "必填",
		}))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()
	result, err := r.runtimeEnv.InstallPackage(ctx, pkgID)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "未知") || strings.Contains(msg, "不支持") || strings.Contains(msg, "校验和") {
			respondError(c, domain.NewValidationError(msg, map[string]string{"packageId": msg}))
			return
		}
		respondError(c, domain.NewError(domain.CodeInternal, "安装失败："+msg))
		return
	}
	r.recordUpdate(c, audit.ResourceSetting, "runtime:install:"+result.ID)
	respondOK(c, result)
}

func (r *Router) runtimeUninstall(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	var req runtimeInstallRequest
	if !bindJSON(c, &req) {
		return
	}
	pkgID := strings.TrimSpace(req.PackageID)
	if pkgID == "" {
		respondError(c, domain.NewValidationError("packageId 不能为空", map[string]string{
			"packageId": "必填",
		}))
		return
	}
	if err := r.runtimeEnv.UninstallPackage(pkgID); err != nil {
		msg := err.Error()
		respondError(c, domain.NewValidationError(msg, map[string]string{"packageId": msg}))
		return
	}
	r.recordUpdate(c, audit.ResourceSetting, "runtime:uninstall:"+pkgID)
	respondOK(c, gin.H{"uninstalled": true, "packageId": pkgID})
}
