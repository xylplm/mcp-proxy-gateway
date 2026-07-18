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

func (r *Router) registerRuntimeRoutes(g *gin.RouterGroup) {
	rt := g.Group("/runtime")
	rt.GET("/summary", r.runtimeSummary)
	rt.GET("/catalog", r.runtimeCatalog)
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
	// 安装可能较久：使用独立超时，不绑死请求体默认时间。
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()
	result, err := r.runtimeEnv.InstallPackage(ctx, pkgID)
	if err != nil {
		// 区分校验类与通用错误
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

// 编译期确认 Service 满足扩展接口（在 router 中定义）。
var _ interface {
	Summary() rtenv.Summary
	Catalog() []rtenv.CatalogPackage
	PreviewInstall(packageID string) (rtenv.CatalogPackage, error)
	InstallPackage(ctx context.Context, packageID string) (rtenv.InstallResult, error)
	UninstallPackage(packageID string) error
} = (*rtenv.Service)(nil)
