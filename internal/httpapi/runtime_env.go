package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	rtenv "github.com/myGithub/mcp-proxy-gateway/internal/runtime"
)

// 编译期确认 *rtenv.Service 满足运行环境窄接口（含路径浏览）。
var _ RuntimeEnvironmentService = (*rtenv.Service)(nil)

type runtimeInstallRequest struct {
	PackageID string `json:"packageId"`
}

type runtimePreflightRequest struct {
	Transport        string                     `json:"transport"`
	Command          string                     `json:"command"`
	Args             []string                   `json:"args"`
	Cwd              string                     `json:"cwd"`
	Requirements     *rtenv.RuntimeRequirements `json:"requirements"`
	SecurityProfile  *rtenv.SecurityProfile     `json:"securityProfile"`
	TemplateRuntimes []string                   `json:"templateRuntimes"`
}

func (r *Router) registerRuntimeRoutes(g *gin.RouterGroup) {
	rt := g.Group("/runtime")
	rt.GET("/summary", r.runtimeSummary)
	rt.GET("/catalog", r.runtimeCatalog)
	rt.GET("/tools", r.runtimeTools)
	rt.POST("/directory/inspect", r.runtimeDirectoryInspect)
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

func (r *Router) runtimeDirectoryInspect(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	var body struct {
		Path            string   `json:"path"`
		FileAccessRoots []string `json:"fileAccessRoots"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	if !bindJSON(c, &body) {
		return
	}
	path := strings.TrimSpace(body.Path)
	if path == "" {
		respondError(c, domain.NewValidationError("path 不能为空", map[string]string{"path": "必填"}))
		return
	}
	// 先通过受控 fsbrowse 根校验，避免 inspect 任意主机目录。
	stat, err := r.runtimeEnv.BrowseStat(path, nil)
	if err != nil || !stat.Allowed || !stat.Exists || stat.Type != "dir" {
		respondError(c, domain.NewError(domain.CodeForbidden, "目录不在允许浏览范围内或不可访问"))
		return
	}
	policy := r.runtimeEnv.Policy()
	launchRoots := append([]string{}, policy.GlobalFileRoots...)
	launchRoots = append(launchRoots, body.FileAccessRoots...)
	if _, allowed, resolveErr := rtenv.ResolveExistingPathWithinRoots(stat.Path, launchRoots); resolveErr != nil || !allowed {
		respondError(c, domain.NewValidationError(
			"目录可浏览但不可启动，请将其加入系统设置的 global_file_roots，或在本上游文件允许路径中声明该目录",
			map[string]string{"path": "缺少可启动的文件允许根"},
		))
		return
	}
	result, err := rtenv.InspectDirectoryLaunch(stat.Path, policy)
	if err != nil {
		respondError(c, domain.NewValidationError(err.Error(), map[string]string{"path": err.Error()}))
		return
	}
	respondOK(c, result)
}

func (r *Router) runtimePreflight(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024)
	var req runtimePreflightRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := validateRuntimePreflightRequest(req); err != nil {
		respondError(c, domain.NewValidationError("运行环境预检参数非法", map[string]string{"preflight": err.Error()}))
		return
	}
	result := r.runtimeEnv.Preflight(rtenv.PreflightRequest{
		Transport:        strings.TrimSpace(req.Transport),
		Command:          strings.TrimSpace(req.Command),
		Args:             req.Args,
		Cwd:              strings.TrimSpace(req.Cwd),
		Requirements:     req.Requirements,
		SecurityProfile:  req.SecurityProfile,
		TemplateRuntimes: req.TemplateRuntimes,
	})
	respondOK(c, result)
}

func validateRuntimePreflightRequest(req runtimePreflightRequest) error {
	transport := strings.ToLower(strings.TrimSpace(req.Transport))
	if transport != "stdio" && transport != "sse" && transport != "streamable-http" && transport != "websocket" && transport != "openapi" {
		return fmt.Errorf("transport 非法")
	}
	if len(req.Command) > 2048 || len(req.Cwd) > 4096 {
		return fmt.Errorf("command 或 cwd 过长")
	}
	if len(req.Args) > 128 || len(req.TemplateRuntimes) > 32 {
		return fmt.Errorf("args 或 templateRuntimes 项数过多")
	}
	for i, arg := range req.Args {
		if len(arg) > 2048 || strings.ContainsRune(arg, 0) {
			return fmt.Errorf("args[%d] 非法", i)
		}
	}
	for i, tag := range req.TemplateRuntimes {
		if len(tag) > 64 || strings.ContainsRune(tag, 0) {
			return fmt.Errorf("templateRuntimes[%d] 非法", i)
		}
	}
	if req.Requirements != nil {
		if _, err := rtenv.ValidateRequirements(*req.Requirements); err != nil {
			return err
		}
	}
	if req.SecurityProfile != nil {
		if _, err := rtenv.ValidateSecurityProfile(*req.SecurityProfile); err != nil {
			return err
		}
	}
	return nil
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
