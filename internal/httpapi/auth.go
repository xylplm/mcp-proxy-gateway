package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/auth"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件实现管理员认证端点（Req 1、17.5）。
//
// 公开认证组 /api/auth/*（无需 JWT，Req 1.1、1.4），供未持令牌的浏览器完成首次
// 初始化与登录：
//   GET    /api/auth/status     报告是否已完成管理员初始化（决定展示注册或登录入口）
//   POST   /api/auth/register   注册唯一管理员账号并完成首次初始化（Req 1.2、1.3、1.9）
//   POST   /api/auth/login      校验凭证并签发会话令牌（Req 1.4、1.5）
//
// 受保护认证组 /api/admin/auth/*（置于管理员 JWT 中间件之下，Req 1.8、1.10）：
//   POST   /api/admin/auth/change-password   校验当前密码后更新密码（Req 1.8、1.10）
//   GET    /api/admin/auth/me                返回当前登录管理员的基本信息（用于头部用户菜单）
//
// 凭证字段级校验与冲突/鉴权语义由应用服务（auth.Service）强制，本层仅做请求解析、
// 接线与统一错误映射；登录响应仅返回令牌与过期时刻，绝不回显密码或哈希。

// authCredentialsRequest 为注册/登录的请求体（Req 1.2、1.4）。
type authCredentialsRequest struct {
	// Username 为管理员用户名，长度需在 3 至 32 个字符之间。
	Username string `json:"username"`
	// Password 为管理员密码，长度需在 6 至 128 个字符之间。
	Password string `json:"password"`
}

// changePasswordRequest 为改密的请求体（Req 1.8、1.10）。
type changePasswordRequest struct {
	// CurrentPassword 为当前密码，校验匹配后方可改密。
	CurrentPassword string `json:"currentPassword"`
	// NewPassword 为新密码，长度需在 6 至 128 个字符之间。
	NewPassword string `json:"newPassword"`
}

// loginResponse 为登录成功的响应体，仅暴露令牌与其过期时刻（Req 1.4）。
type loginResponse struct {
	// Token 为签发的会话访问令牌（JWT）。
	Token string `json:"token"`
	// ExpiresAt 为令牌过期时刻（RFC3339）。
	ExpiresAt string `json:"expiresAt"`
}

// registerPublicAuthRoutes 注册无需 JWT 的公开认证端点（Req 1.1、1.4）。
//
// 该组始终注册，不受管理员鉴权中间件是否就绪影响，以便未持令牌的浏览器完成初始化与登录。
func (r *Router) registerPublicAuthRoutes(router gin.IRouter) {
	g := router.Group("/api/auth")
	g.GET("/status", r.authStatus)
	g.POST("/register", r.register)
	g.POST("/login", r.login)
}

// registerProtectedAuthRoutes 在管理分组下注册受 JWT 保护的认证端点（Req 1.8、1.10）。
func (r *Router) registerProtectedAuthRoutes(g *gin.RouterGroup) {
	g.POST("/auth/change-password", r.changePassword)
	g.GET("/auth/me", r.currentAdmin)
}

// currentAdmin 返回当前登录管理员的基本信息，供前端头部用户菜单展示。
//
// 用户名取自 JWT 主体（subject）：管理员 JWT 中间件（auth.RequireAdmin）校验通过后
// 将会话信息以 auth.Claims 存入 gin.Context（键 auth.claims），此处经导出的
// auth.ClaimsFromContext 受控读取。上下文缺少会话信息（通常意味着未经鉴权中间件）
// 时返回 UNAUTHORIZED（401）。
func (r *Router) currentAdmin(c *gin.Context) {
	claims, ok := auth.ClaimsFromContext(c)
	if !ok {
		respondError(c, domain.NewError(domain.CodeUnauthorized, "未获取到当前登录会话信息"))
		return
	}
	respondOK(c, gin.H{"username": claims.Username})
}

// authStatus 报告是否已完成管理员初始化（Req 1.1）。
//
// initialized 为 false 时前端应展示注册入口，否则展示登录入口。
// 同时返回离线密码重置的标记文件名，供前端「忘记密码」弹窗动态展示，避免前后端文案脱节。
func (r *Router) authStatus(c *gin.Context) {
	if r.auth == nil {
		respondServiceUnavailable(c, "认证服务未就绪")
		return
	}
	respondOK(c, gin.H{
		"initialized":     r.auth.IsInitialized(),
		"resetMarkerFile": auth.ResetMarkerFileName(),
	})
}

// register 注册唯一管理员账号并完成首次初始化（Req 1.2、1.3、1.9）。
//
// 已存在管理员时由应用服务返回 CONFLICT（映射 409）；字段长度越界返回字段级 VALIDATION（400）。
func (r *Router) register(c *gin.Context) {
	if r.auth == nil {
		respondServiceUnavailable(c, "认证服务未就绪")
		return
	}
	var req authCredentialsRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := r.auth.Register(req.Username, req.Password); err != nil {
		respondError(c, err)
		return
	}
	r.recordCreate(c, audit.ResourceAdmin, req.Username)
	respondCreated(c, gin.H{"username": req.Username, "initialized": true})
}

// login 校验管理员凭证并签发会话令牌（Req 1.4、1.5）。
//
// 凭证不匹配由应用服务返回 UNAUTHORIZED（映射 401），不创建任何会话。
func (r *Router) login(c *gin.Context) {
	if r.auth == nil {
		respondServiceUnavailable(c, "认证服务未就绪")
		return
	}
	var req authCredentialsRequest
	if !bindJSON(c, &req) {
		return
	}
	token, expiresAt, err := r.auth.Login(req.Username, req.Password)
	if err != nil {
		r.recordLogin(c, req.Username, false)
		respondError(c, err)
		return
	}
	r.recordLogin(c, req.Username, true)
	respondOK(c, loginResponse{
		Token:     token,
		ExpiresAt: expiresAt.Format(timeLayout),
	})
}

// changePassword 校验当前密码后更新管理员密码（Req 1.8、1.10）。
//
// 当前密码不匹配返回 UNAUTHORIZED（401）；新密码长度越界返回字段级 VALIDATION（400），
// 两种情形均保留原密码不变。
func (r *Router) changePassword(c *gin.Context) {
	if r.auth == nil {
		respondServiceUnavailable(c, "认证服务未就绪")
		return
	}
	var req changePasswordRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := r.auth.ChangePassword(req.CurrentPassword, req.NewPassword); err != nil {
		respondError(c, err)
		return
	}
	if claims, ok := auth.ClaimsFromContext(c); ok {
		r.recordUpdate(c, audit.ResourceAdmin, claims.Username)
	} else {
		r.recordUpdate(c, audit.ResourceAdmin, "unknown")
	}
	respondOK(c, gin.H{"changed": true})
}
