package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件实现系统设置读写端点（Req 7.3、18.4、17.5）：
//
//   GET    /api/admin/settings   读取当前 YAML 常规配置快照（不含数据库/密钥等环境配置）
//   PUT    /api/admin/settings   校验并回写常规配置（同步 cron、各超时、模式、保留期等）
//
// 写入流程：先做 cron 专项校验（Req 7.3、7.4：非法 cron 返回 VALIDATION 且不持久化），
// 再交由配置管理器（config.Manager.Save）做字段范围校验与原子落盘（Req 18.4）。
// 字段范围校验（会话超时、各超时、模式枚举、保留期等）由配置层统一强制，本层仅接线。
//
// 安全：常规配置含管理员凭证哈希（Admin.PasswordHash）。读取与写入均不经由本端点暴露
// 或改动管理员凭证——读取时清空敏感字段，写入时沿用既有管理员配置，避免越权改密或泄露哈希。

// settingsResponse 为系统设置的对外视图。
//
// 内嵌完整 YAML 配置便于一次性读写，但管理员凭证（用户名/哈希/初始化标志）不在此暴露，
// 改密走专用的 /api/admin/auth/change-password 端点。
type settingsResponse struct {
	Settings config.YAMLConfig       `json:"settings"`
	Runtime  settingsRuntimeResponse `json:"runtime"`
}

type settingsRuntimeResponse struct {
	Server config.ServerConfig `json:"server"`
}

// registerSettingsRoutes 在管理分组下注册系统设置读写端点（Req 18.4）。
func (r *Router) registerSettingsRoutes(g *gin.RouterGroup) {
	g.GET("/settings", r.getSettings)
	g.PUT("/settings", r.updateSettings)
}

// getSettings 读取当前常规配置快照（Req 18.4）。
//
// 返回前清空管理员凭证字段，避免向前端泄露密码哈希。
func (r *Router) getSettings(c *gin.Context) {
	if r.settings == nil {
		respondServiceUnavailable(c, "系统设置服务未就绪")
		return
	}
	cfg := r.settings.Config()
	respondOK(c, r.settingsView(cfg))
}

// updateSettings 校验并回写常规配置（Req 7.3、7.4、18.4）。
//
// 流程：
//  1. 解析请求体为完整 YAML 配置；
//  2. 沿用既有管理员凭证（用户名/哈希/初始化标志），避免经由本端点越权改密；
//  3. 对同步 cron 做专项校验（非法返回字段级 VALIDATION，不持久化，Req 7.3、7.4）；
//  4. 交由配置管理器做字段范围校验与原子落盘（Req 18.4）。
func (r *Router) updateSettings(c *gin.Context) {
	if r.settings == nil {
		respondServiceUnavailable(c, "系统设置服务未就绪")
		return
	}
	restartRequested := c.Query("restart") == "true"
	var req config.YAMLConfig
	if !bindJSON(c, &req) {
		return
	}
	// 沿用既有管理员凭证，本端点不参与改密，杜绝凭证经设置写入被篡改（Req 1.8 走专用端点）。
	req.Admin = r.settings.Config().Admin

	// 同步 cron 专项校验：非法表达式返回字段级 VALIDATION 且不持久化（Req 7.3、7.4）。
	if r.validateCron != nil {
		if err := r.validateCron(req.Sync.Cron); err != nil {
			respondError(c, wrapCronError(err))
			return
		}
	}

	if err := r.settings.Save(req); err != nil {
		respondError(c, err)
		return
	}
	if r.settingsRuntime != nil {
		if err := r.settingsRuntime.ApplySettings(req); err != nil {
			respondError(c, err)
			return
		}
	}

	saved := r.settings.Config()
	r.recordUpdate(c, audit.ResourceSetting, "settings")
	respondOK(c, r.settingsView(saved))
	if restartRequested && r.settingsRuntime != nil {
		r.settingsRuntime.RequestRestart()
	}
}

func (r *Router) settingsView(cfg config.YAMLConfig) settingsResponse {
	cfg.Admin = config.AdminConfig{Initialized: cfg.Admin.Initialized}
	runtimeServer := cfg.Server
	if r.settingsRuntime != nil {
		runtimeServer = r.settingsRuntime.RuntimeServerConfig()
	}
	return settingsResponse{
		Settings: cfg,
		Runtime:  settingsRuntimeResponse{Server: runtimeServer},
	}
}

// wrapCronError 将 cron 校验错误归一为携带字段定位的 VALIDATION 错误。
//
// 若底层已是 *domain.APIError（cron 校验器即返回该类型），直接透传以保留其字段级说明；
// 否则包装为指向 sync.cron 字段的 VALIDATION，便于前端定位无效字段。
func wrapCronError(err error) error {
	if _, ok := err.(*domain.APIError); ok {
		return err
	}
	return domain.NewValidationError("同步 cron 表达式非法", map[string]string{
		"sync.cron": err.Error(),
	})
}
