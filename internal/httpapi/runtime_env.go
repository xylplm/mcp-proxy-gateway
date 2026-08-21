package httpapi

import (
	"context"
	"errors"
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

type runtimeDepRequest struct {
	Kind string `json:"kind"`
	Spec string `json:"spec"`
	Name string `json:"name"`
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
	rt.GET("/tools", r.runtimeTools)
	rt.POST("/directory/inspect", r.runtimeDirectoryInspect)
	rt.POST("/preflight", r.runtimePreflight)
	// 依赖管理：npm/pip 第三方包的列表/安装/卸载。
	rt.GET("/deps", r.runtimeListDeps)
	rt.POST("/deps/install", r.runtimeInstallDep)
	rt.POST("/deps/uninstall", r.runtimeUninstallDep)
}

func (r *Router) runtimeSummary(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	respondOK(c, r.runtimeEnv.Summary())
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

// runtimeListDeps 列出某类运行时已安装的第三方包。
func (r *Router) runtimeListDeps(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	kindStr := strings.TrimSpace(c.Query("kind"))
	kind, err := rtenv.NormalizeDepKind(kindStr)
	if err != nil {
		respondError(c, domain.NewValidationError(err.Error(), map[string]string{"kind": err.Error()}))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	result, err := r.runtimeEnv.ListDeps(ctx, kind)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, rtenv.ErrLocalRuntimeUnsupported) {
			respondError(c, domain.NewValidationError(msg, map[string]string{"kind": msg}))
			return
		}
		respondError(c, domain.NewError(domain.CodeInternal, "查询依赖失败："+msg))
		return
	}
	respondOK(c, result)
}

// runtimeInstallDep 安装/升级一个第三方包。
func (r *Router) runtimeInstallDep(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	var req runtimeDepRequest
	if !bindJSON(c, &req) {
		return
	}
	kind, kErr := rtenv.NormalizeDepKind(req.Kind)
	if kErr != nil {
		respondError(c, domain.NewValidationError(kErr.Error(), map[string]string{"kind": kErr.Error()}))
		return
	}
	spec := strings.TrimSpace(req.Spec)
	if spec == "" {
		respondError(c, domain.NewValidationError("spec 不能为空", map[string]string{"spec": "必填"}))
		return
	}
	if len(spec) > 256 {
		respondError(c, domain.NewValidationError("spec 过长", map[string]string{"spec": "上限 256 字符"}))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()
	result, err := r.runtimeEnv.InstallDep(ctx, kind, spec)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, rtenv.ErrLocalRuntimeUnsupported) || strings.Contains(msg, "不能为空") || strings.Contains(msg, "非法") {
			respondError(c, domain.NewValidationError(msg, map[string]string{"spec": msg}))
			return
		}
		respondError(c, domain.NewError(domain.CodeInternal, "安装依赖失败："+msg))
		return
	}
	r.recordUpdate(c, audit.ResourceSetting, "runtime:dep:"+string(kind)+":install:"+result.Name)
	respondOK(c, result)
}

// runtimeUninstallDep 卸载一个第三方包。
func (r *Router) runtimeUninstallDep(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	var req runtimeDepRequest
	if !bindJSON(c, &req) {
		return
	}
	kind, kErr := rtenv.NormalizeDepKind(req.Kind)
	if kErr != nil {
		respondError(c, domain.NewValidationError(kErr.Error(), map[string]string{"kind": kErr.Error()}))
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		respondError(c, domain.NewValidationError("name 不能为空", map[string]string{"name": "必填"}))
		return
	}
	if len(name) > 214 {
		respondError(c, domain.NewValidationError("name 过长", map[string]string{"name": "上限 214 字符"}))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()
	result, err := r.runtimeEnv.UninstallDep(ctx, kind, name)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, rtenv.ErrLocalRuntimeUnsupported) || strings.Contains(msg, "不能为空") || strings.Contains(msg, "非法") {
			respondError(c, domain.NewValidationError(msg, map[string]string{"name": msg}))
			return
		}
		respondError(c, domain.NewError(domain.CodeInternal, "卸载依赖失败："+msg))
		return
	}
	r.recordUpdate(c, audit.ResourceSetting, "runtime:dep:"+string(kind)+":uninstall:"+name)
	respondOK(c, result)
}
