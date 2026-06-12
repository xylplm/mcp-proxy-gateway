package app

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/apikey"
	"github.com/myGithub/mcp-proxy-gateway/internal/health"
	"github.com/myGithub/mcp-proxy-gateway/internal/httpapi"
	"github.com/myGithub/mcp-proxy-gateway/internal/mcpapi"
	"github.com/myGithub/mcp-proxy-gateway/internal/static"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// routerWiring 聚合装配入站路由分面所需的各组件。
type routerWiring struct {
	adminRouter    *httpapi.Router
	adminAuth      gin.HandlerFunc
	mcpEndpoints   *mcpapi.Endpoints
	authenticator  *apikey.Authenticator
	aclGuard       *apikey.ACLGuard
	rateLimiter    *apikey.RateLimiter
	detailReporter *health.DetailReporter
}

// buildRouter 装配兼容旧单端口部署的完整路由分面。
func (a *App) buildRouter(w routerWiring) *gin.Engine {
	return a.buildAdminRouter(w, true)
}

// buildAdminRouter 装配管理端口的路由分面：
//
//   - 公开存活探针 /healthz                          —— 无鉴权（Req 20.6）
//   - 管理 REST API /api/auth/*、/api/admin/*        —— 管理员 JWT（Req 17.5）
//   - 详细健康 /api/admin/health                     —— 管理员 JWT（Req 20.7/20.8）
//   - 对外 MCP API /mcp/sse、/mcp/http、/mcp/ws       —— 可选，API Key + 来源白名单 + 限流（Req 11.8）
//   - 静态 SPA /、/assets/*（NoRoute 兜底）           —— 无鉴权（Req 17.1/17.2）
//
// 管理面与服务面在前缀与中间件链上互不交叉：JWT 链只挂在 /api/admin 组，API Key 链只挂在
// /mcp 组，避免两套鉴权互相污染（Req 11.8）。当 exposeMCP 为 false 时，管理端口完全不注册
// /mcp/*，便于把对外 MCP 服务迁移到独立端口后收紧公网暴露面。
func (a *App) buildAdminRouter(w routerWiring, exposeMCP bool) *gin.Engine {
	engine := newBaseEngine()

	// 公开存活探针（无鉴权，Req 20.6）。
	engine.GET("/healthz", health.LivenessHandler())

	// 管理面：公开认证端点 + JWT 保护的管理端点（Req 17.5）。
	// httpapi.Router.Register 内部注册 /api/auth/*（公开）与 /api/admin/*（adminAuth 之下）。
	w.adminRouter.Register(engine, w.adminAuth)

	// 详细健康端点 /api/admin/health：置于管理员 JWT 之下（Req 20.7/20.8）。
	engine.GET("/api/admin/health", w.adminAuth, w.detailReporter.Handler())

	if exposeMCP {
		registerMCPRoutes(engine, w)
	}

	// 静态 SPA：作为 NoRoute 兜底处理「其余」非 API/非文件路径（Req 17.1/17.2）。
	//
	// 前端 SPA 产物经构建步骤同步至 internal/static/dist 并内嵌进二进制（embed），由
	// static.New() 直接从内嵌 FS 提供。若 dist 缺失（未执行前端构建/同步）导致装载失败，
	// 仅记录告警并跳过，不阻断后端启动，便于纯后端联调。
	if srv, err := static.New(); err != nil {
		a.logger.Warn("未能装载前端静态资源，跳过 SPA 兜底", "error", err)
	} else {
		a.logger.Info("已装载前端静态资源", "source", "embed")
		engine.NoRoute(srv.GinHandler())
	}

	return engine
}

// buildMCPRouter 装配独立对外 MCP 端口。该端口只注册 /mcp/* 与可选 /healthz，不包含管理 API
// 与 SPA 兜底，适合直接暴露到公网并在反向代理层叠加更严格的安全策略。
func (a *App) buildMCPRouter(w routerWiring, exposeHealthz bool) *gin.Engine {
	engine := newBaseEngine()
	if exposeHealthz {
		engine.GET("/healthz", health.LivenessHandler())
	}
	registerMCPRoutes(engine, w)
	return engine
}

func newBaseEngine() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	return engine
}

func registerMCPRoutes(engine *gin.Engine, w routerWiring) {
	// 服务面：对外 MCP API，API Key 鉴权 → 来源白名单 → 限流，三者依次前置（Req 11.8、11.9、13、21）。
	mcpGroup := engine.Group("/")
	mcpGroup.Use(w.authenticator.Middleware())
	mcpGroup.Use(w.aclGuard.Middleware(apikey.MetadataFromContext))
	mcpGroup.Use(w.rateLimiter.Middleware(apikey.MetadataFromContext))
	w.mcpEndpoints.Register(mcpGroup)

	// 智能模式端点：与全量模式共享同一套鉴权链。
	w.mcpEndpoints.RegisterSmart(mcpGroup)
}

// repoUpstreamGet 经连接管理器无法直接取回加密凭证行，故由本方法直接走仓储读取单条上游行。
//
// 仅供启动探测路径复用（其余调用路径已持有解密后的配置）。
func (a *App) repoUpstreamGet(ctx context.Context, id string) (*store.UpstreamRow, error) {
	return store.NewUpstreamRepo(a.pool).Get(ctx, id)
}
