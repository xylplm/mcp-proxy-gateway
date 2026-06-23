// Package app 负责把 MCP Proxy Gateway 的全部组件装配为一个可运行系统（任务 27.2）。
//
// 装配遵循依赖链：Config_Manager → DB/Redis/schema 初始化 → 各应用服务 →
// 领域核心 → 入站路由（静态 SPA / 管理 REST API / 对外 MCP API / healthz）。管理面
// （JWT）与服务面（API Key + 限流 + 来源白名单）在路由前缀与中间件链上完全分离
// （设计「路由分面」，Req 11.8、17.1）。启动时先做连通性探测再对外提供服务（Req 20.1）。
//
// 设计取舍：本包是组合层，不持有业务逻辑；它只负责按正确顺序构造各组件、用窄接口/适配器
// 把它们接线起来，并管理 HTTP 服务与后台服务（同步 cron、统计 worker/清理、审计清理、
// 小智连接）的启停与优雅停机。
package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/myGithub/mcp-proxy-gateway/internal/aggregation"
	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/health"
	"github.com/myGithub/mcp-proxy-gateway/internal/manager"
	"github.com/myGithub/mcp-proxy-gateway/internal/mcpapi"
	"github.com/myGithub/mcp-proxy-gateway/internal/security"
	"github.com/myGithub/mcp-proxy-gateway/internal/stats"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
	syncsvc "github.com/myGithub/mcp-proxy-gateway/internal/sync"
	"github.com/myGithub/mcp-proxy-gateway/internal/syslog"
	"github.com/myGithub/mcp-proxy-gateway/internal/xiaozhi"
)

// defaultAdminAddr 为管理端默认监听地址。
const defaultAdminAddr = ":8080"

// shutdownTimeout 为优雅停机时等待在途请求结束的最长时长。
const shutdownTimeout = 15 * time.Second

// Option customizes application wiring.
type Option func(*App)

// WithSystemLogs injects the process-local runtime log store exposed by the admin API.
func WithSystemLogs(store *syslog.Store) Option {
	return func(a *App) {
		a.systemLogs = store
	}
}

// WithLevelVar 注入可变的日志级别变量，供 ApplySettings 在运行时调整日志级别。
//
// console handler（stdout）的过滤级别绑定该 LevelVar；设置后所有共用同一 handler 的
// logger（含经 slog.SetDefault 设置的全局 logger 及注入到各服务的 logger）即时生效。
func WithLevelVar(lv *slog.LevelVar) Option {
	return func(a *App) {
		a.levelVar = lv
	}
}

// App 持有装配完成的全部组件与后台服务句柄，提供启动连通性探测、对外服务与优雅停机。
type App struct {
	logger *slog.Logger

	adminAddr            string
	publicMCPAddr        string
	exposeMCPOnAdminAddr bool

	cfg *config.Manager
	db  *gorm.DB
	rdb *redis.Client

	adminEngine     *gin.Engine
	publicMCPEngine *gin.Engine
	adminServer     *http.Server
	publicMCPServer *http.Server
	restartCh       chan struct{}

	systemLogs *syslog.Store
	// levelVar 为可变日志级别变量，绑定到 console handler，供 ApplySettings 热更新。
	levelVar *slog.LevelVar

	// 后台服务（生命周期由 Run 管理）。
	scheduler     *syncsvc.Scheduler
	statRecorder  *stats.Recorder
	statCleaner   *stats.Cleaner
	auditSvc      *audit.Service
	auditRecorder *audit.Recorder
	xiaozhiConn   *xiaozhi.Connector
	mcpService    *mcpapi.Service
	agg           *aggregation.Service
	securityGuard *security.Guard
	mgr           *manager.Manager
	dialer        *sessionDialer

	// 启动连通性探测器与同步逻辑。
	prober      *health.StartupProber
	syncer      *syncsvc.PeriodicSyncer
	syncTimeout time.Duration
}

// New 按依赖顺序装配整个系统：加载配置 → 连接 DB/Redis 并初始化 schema → 校验加密密钥 →
// 构造各应用服务、领域核心与入站路由。任一前置步骤失败（缺失/非法环境变量、YAML 非法、
// 加密密钥无效、schema 初始化失败、连接失败）都会返回错误，调用方据此记录日志并以非零码退出
// （Req 18.3/18.6、19.4、20.1）。
//
// 注意：New 完成「构造与接线」，但不启动后台服务，也不开始对外服务；启动连通性探测与
// 服务循环由 Run 负责，从而保证「先探测、后服务」的启动顺序（Req 20.1）。
func New(ctx context.Context, logger *slog.Logger, opts ...Option) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// 1) 加载配置：环境变量 + YAML（缺失/非法即返回错误，Req 18.1/18.3/18.6）。
	envCfg, err := config.LoadEnvConfig()
	if err != nil {
		return nil, err
	}
	cfgMgr, err := config.Load(logger, envCfg.DataDir)
	if err != nil {
		return nil, err
	}

	// 2) 数据层：GORM PG 连接 + schema 初始化 + Redis 客户端（初始化在连接成功后、对外服务前，Req 23.3）。
	db, err := store.NewDB(ctx, envCfg.PGDSN)
	if err != nil {
		return nil, err
	}
	if err := store.AutoMigrate(ctx, db, logger); err != nil {
		_ = store.CloseDB(db)
		return nil, err
	}
	rdb, err := store.NewRedisClient(envCfg.RedisAddr, envCfg.RedisPassword)
	if err != nil {
		_ = store.CloseDB(db)
		return nil, err
	}

	a := &App{
		logger:               logger,
		adminAddr:            defaultAdminAddr,
		exposeMCPOnAdminAddr: true,
		cfg:                  cfgMgr,
		db:                   db,
		rdb:                  rdb,
		restartCh:            make(chan struct{}, 1),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}

	// 3) 构造各应用服务、领域核心与入站路由。
	if err := a.build(envCfg); err != nil {
		_ = a.closeInfra()
		return nil, err
	}
	return a, nil
}

// closeInfra 释放基础设施连接（用于构造失败时回滚或停机时清理）。
func (a *App) closeInfra() error {
	if a.db != nil {
		_ = store.CloseDB(a.db)
	}
	if a.rdb != nil {
		return a.rdb.Close()
	}
	return nil
}
