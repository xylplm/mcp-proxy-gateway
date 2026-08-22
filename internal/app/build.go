package app

import (
	"context"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/myGithub/mcp-proxy-gateway/internal/aggregation"
	"github.com/myGithub/mcp-proxy-gateway/internal/apikey"
	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/auth"
	"github.com/myGithub/mcp-proxy-gateway/internal/backup"
	"github.com/myGithub/mcp-proxy-gateway/internal/cache"
	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/health"
	"github.com/myGithub/mcp-proxy-gateway/internal/httpapi"
	"github.com/myGithub/mcp-proxy-gateway/internal/manager"
	"github.com/myGithub/mcp-proxy-gateway/internal/mcpapi"
	"github.com/myGithub/mcp-proxy-gateway/internal/risk"
	rtenv "github.com/myGithub/mcp-proxy-gateway/internal/runtime"
	"github.com/myGithub/mcp-proxy-gateway/internal/scripts"
	"github.com/myGithub/mcp-proxy-gateway/internal/security"
	"github.com/myGithub/mcp-proxy-gateway/internal/stats"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
	syncsvc "github.com/myGithub/mcp-proxy-gateway/internal/sync"
	"github.com/myGithub/mcp-proxy-gateway/internal/template"
	"github.com/myGithub/mcp-proxy-gateway/internal/transport"
	"github.com/myGithub/mcp-proxy-gateway/internal/xiaozhi"
)

// build 构造各应用服务、领域核心与入站路由，并把它们接线到 *App。
//
// 顺序：仓储 → 缓存/传输/连接管理 → 规则引擎/聚合服务 → 调用路由器接入 →
// 各横切应用服务（同步、统计、审计、API Key、认证、模板、小智、健康）→ 入站路由分面。
func (a *App) build(envCfg config.EnvConfig) error {
	yamlCfg := a.cfg.Config()
	a.adminAddr = yamlCfg.Server.AdminAddr
	a.publicMCPAddr = yamlCfg.Server.PublicMCPAddr
	a.exposeMCPOnAdminAddr = yamlCfg.Server.ExposeMCPOnAdminAddr

	// --- 仓储层 ---
	repos := store.NewRepositories(a.db)
	backupSvc := backup.NewService(a.cfg, backup.NewStoreAdapter(repos))

	// --- 出站适配：工具缓存、传输工厂、连接拨号/会话注册 ---
	toolCache := cache.New(a.rdb, repos.ToolCache, a.logger)
	// stdio 安全策略：每次从配置快照读取，保存设置后无需重启即可生效。
	policyFromCfg := func() rtenv.Policy {
		rt := a.cfg.Config().Runtime
		hardening := true
		if rt.ProcessHardening != nil {
			hardening = *rt.ProcessHardening
		}
		pathOnly := true
		if rt.StrictPathOnlyRuntime != nil {
			pathOnly = *rt.StrictPathOnlyRuntime
		}
		policyOnly := true
		if rt.StrictAllowPolicyOnly != nil {
			policyOnly = *rt.StrictAllowPolicyOnly
		}
		return rtenv.Policy{
			StdioEnabled:              rt.StdioEnabled,
			CommandAllowlist:          append([]string{}, rt.CommandAllowlist...),
			ExtraSensitiveEnvPrefixes: append([]string{}, rt.ExtraSensitiveEnvPrefixes...),
			ProcessHardening:          hardening,
			DefaultStdioSecurityMode:  rtenv.StdioSecurityMode(rt.DefaultStdioSecurityMode),
			StrictCommandAllowlist:    append([]string{}, rt.StrictCommandAllowlist...),
			StrictPackageAllowlist:    append([]string{}, rt.StrictPackageAllowlist...),
			GlobalFileRoots:           append([]string{}, rt.GlobalFileRoots...),
			BrowseExtraRoots:          append([]string{}, rt.BrowseExtraRoots...),
			StrictPathOnlyRuntime:     pathOnly,
			StrictNetworkDefault:      rtenv.NetworkAccessMode(rt.StrictNetworkDefault),
			StrictAllowPolicyOnly:     policyOnly,
			NpmRegistry:               rt.NpmRegistry,
			PipIndexURL:               rt.PipIndexURL,
			UvIndexURL:                rt.UvIndexURL,
		}
	}
	transport.SetPolicyProvider(policyFromCfg)
	// 远程与本地上游建立连接时热读取全局连接超时；保存后重启时新会话会使用新值，
	// 避免连接管理策略与真实握手超时不一致。
	transport.SetConnectTimeoutProvider(func() time.Duration {
		return time.Duration(a.cfg.Config().Connection.ConnectTimeoutS) * time.Second
	})
	// 卷内运行时目录：优先 MPG_RUNTIME_DIR，否则 {DataDir}/runtime。
	runtimeDirFn := func() string {
		env := a.cfg.Env()
		return rtenv.ResolveRuntimeDir(env.DataDir, env.RuntimeDir)
	}
	transport.SetRuntimeDirProvider(runtimeDirFn)
	// 卷内运行时目录只存放用户自放的可执行文件与 npm/pip 共享依赖，任何平台都可创建；
	// 只读卷或权限不足时忽略失败，远程上游与只读探测仍可用。
	_ = rtenv.EnsureRuntimeLayout(runtimeDirFn())
	runtimeSvc := rtenv.NewService(
		policyFromCfg,
		func() string { return a.cfg.Env().DataDir },
		runtimeDirFn,
	)
	scriptSvc := scripts.NewService(envCfg.DataDir, runtimeSvc)
	transport.SetScriptService(scriptSvc)
	factory := transport.NewFactory()
	dialer := newSessionDialer(factory)
	a.dialer = dialer

	// --- 连接管理器（MCP_Manager）：注入退避策略与拨号器 ---
	retry := retryPolicyFromConfig(yamlCfg.Connection)
	mgr := manager.New(
		repos.Upstream,
		toolCache,
		transport.ValidateConnParams,
		a.logger,
		manager.WithRetryPolicy(retry),
		manager.WithDialer(dialer),
	)
	a.mgr = mgr

	// --- 领域核心：规则引擎 + 聚合服务 ---
	ruleEngine := domain.NewRuleEngine()
	agg := aggregation.NewService(
		toolCache,
		ruleEngine,
		upstreamListerAdapter{repo: repos.Upstream},
		aliasListerAdapter{repo: repos.Alias},
		mcpFilterListerAdapter{repo: repos.FilterMCP},
		apiKeyFilterListerAdapter{repo: repos.FilterAPIKey},
		toolPolicyListerAdapter{repo: repos.ToolPolicy},
	)
	agg.SetLogger(a.logger)
	agg.SetRiskAuthorizer(risk.NewAuthorizer(riskProfileReaderAdapter{repo: repos.APIKey}, repos.ToolRisk))
	upstreamAccessMgr := apikey.NewUpstreamAccessManager(repos.APIKeyUpstreamAccess)
	agg.SetUpstreamAccessAuthorizer(upstreamAccessMgr)
	agg.SetRoutingStrategy(yamlCfg.Aggregation.ToolRoutingStrategy)
	agg.SetQuotaManager(aggregation.NewQuotaManager(aggregation.NewRedisQuotaCounter(a.rdb), a.logger))
	a.agg = agg

	// --- 聚合调用路由：连接状态来自 manager，会话来自 dialer 注册表（Req 10.3/10.5/10.8）---
	callTimeout := time.Duration(yamlCfg.Aggregation.UpstreamCallTimeoutS) * time.Second
	invoker := aggregation.NewSessionInvoker(mgr, dialer, callTimeout, a.logger)
	agg.SetInvoker(invoker)

	// --- 统计服务：异步写入 worker（Redis 缓冲）+ 多维查询 + 保留期清理 ---
	statRecent := stats.NewRedisRecentRecordStore(a.rdb)
	statWriter := stats.NewCompositeWriter(repos.CallStat, statRecent)
	statRecorder := stats.New(stats.NewRedisStatBuffer(a.rdb), statWriter, stats.WithLogger(a.logger))
	agg.SetRecorder(statRecorder)
	a.statRecorder = statRecorder
	statQuery, err := stats.NewQueryService(stats.NewCombinedQuerier(repos.CallStat, statRecent), a.cfg, stats.WithPendingDropper(statRecorder))
	if err != nil {
		return err
	}
	a.statCleaner = stats.NewCleaner(repos.CallStat, a.cfg, stats.WithCleanerLogger(a.logger))

	// --- 审计服务：事件记录 + 倒序分页查询 + 保留期清理 ---
	auditSvc, err := audit.New(repos.Audit, a.cfg)
	if err != nil {
		return err
	}
	a.auditSvc = auditSvc
	a.auditRecorder = audit.NewRecorder(repos.Audit, audit.WithLogger(a.logger))
	var riskCipher *risk.Cipher
	if encodedKey := os.Getenv("MPG_SECRET_ENCRYPTION_KEY"); encodedKey != "" {
		var err error
		riskCipher, err = risk.NewCipher(encodedKey)
		if err != nil {
			return err
		}
	}
	riskGovernance := risk.NewGovernanceService(repos.AIProvider, repos.ToolRisk, repos.RiskJob, riskCipher)
	if err := riskGovernance.Resume(context.Background()); err != nil {
		return err
	}

	// --- 同步服务：工具拉取、周期同步与 cron 调度（Req 6、7）---
	fetcher := &toolFetcher{
		dialer:  dialer,
		factory: factory,
		repo:    repos.Upstream,
	}
	syncTimeout := time.Duration(yamlCfg.Sync.TimeoutS) * time.Second
	a.syncTimeout = syncTimeout
	a.syncer = syncsvc.NewPeriodicSyncer(fetcher, toolCache, mgr, syncTimeout, a.logger)
	refresher := syncsvc.NewRefresher(fetcher, toolCache, syncTimeout, a.logger)
	catalogObserver := riskCatalogObserver{repo: repos.ToolRisk, governance: riskGovernance}
	a.syncer.SetObserver(catalogObserver)
	refresher.SetObserver(catalogObserver)
	a.scheduler = syncsvc.NewScheduler()

	// --- API Key 管理与对外鉴权链路组件（Req 11.9、12、13、21）---
	apiKeyMgr := apikey.New(repos.APIKey)
	apiKeyFilterMgr := apikey.NewFilterManager(repos.FilterAPIKey, ruleEngine)
	securityGuard := security.NewGuard(repos.Security, security.NewRedisCache(a.rdb), a.cfg, a.logger)
	a.securityGuard = securityGuard
	authenticator := apikey.NewAuthenticator(repos.APIKey, a.logger).WithFailureRecorder(securityGuard)
	aclGuard := apikey.NewACLGuard(repos.ACL, a.logger).WithDenyRecorder(securityGuard)
	rateLimiter := apikey.NewRateLimiter(apikey.NewRedisRateCounter(a.rdb), a.logger)

	// --- 认证服务（Auth_Service）+ 管理员 JWT 中间件 ---
	// 装配前先检查离线密码重置标记（data/.reset-admin）：存在即生成随机新密码、写回 YAML、
	// 在 stderr 打印一次性新密码并删除标记文件；不存在则 no-op。该机制供管理员忘记密码时
	// 通过本地文件触发重置，不引入远程攻击面。
	if err := auth.MaybeResetAdminPassword(a.cfg, envCfg.DataDir, a.logger); err != nil {
		return err
	}
	// 确保 config.yaml 中存在非空 JWT 签名密钥；为空时首启自动生成并写回。
	// 必须在 auth.New 之前完成，否则空密钥会导致认证服务初始化失败。
	if err := auth.EnsureJWTSecret(a.cfg, a.logger); err != nil {
		return err
	}
	jwtCfg := a.cfg.Config()
	authSvc, err := auth.New(a.cfg, []byte(jwtCfg.JWTSecret))
	if err != nil {
		return err
	}
	adminAuth := auth.RequireAdmin(authSvc, auth.WithAccessDeniedHook(func(c *gin.Context, reason string) {
		a.auditRecorder.RecordAccessDenied(c.Request.Context(), c.Request.URL.Path, reason)
	}))

	// --- 模板市场服务（Template_Market）：内置分类化快捷模板的只读查询（Req 14）---
	templateMarket := template.New()

	// --- 对外 MCP API 服务（MCP_API_Service）：按模式构建 server，多传输暴露 ---
	mcpService := mcpapi.NewService(agg, yamlCfg.MCPAPI.SmartDiscoveryLimit, a.logger)
	a.mcpService = mcpService
	mcpEndpoints := mcpapi.NewEndpoints(mcpService, resolveAPIKeyID, a.logger)
	mcpEndpoints.SetRequestBodyLimitMiB(yamlCfg.MCPAPI.RequestBodyLimitMiB)
	a.mcpEndpoints = mcpEndpoints

	// --- 小智接入服务（XiaoZhi_Connector）：出站 WS 客户端，按配置启停 ---
	xzBackoff := xiaozhiBackoffFromConfig(yamlCfg.Connection)
	a.xiaozhiConn = xiaozhi.NewConnector(
		yamlCfg.XiaoZhi.Endpoint,
		yamlCfg.XiaoZhi.Enabled,
		nil,
		xiaozhi.WithServerBuildFn(func(ctx context.Context, mode string) (*mcp.Server, error) {
			return mcpService.BuildServerWithSource(ctx, "", mode, "xiaozhi")
		}),
		xiaozhi.WithBackoffPolicy(xzBackoff),
		xiaozhi.WithLogger(a.logger),
	)

	// --- 健康检查：启动连通性探测器 + 详细健康汇总器（Req 20）---
	pg := pinger{db: a.db, rdb: a.rdb}
	a.prober = health.NewStartupProber(health.Options{
		Pinger:        pg,
		ListUpstreams: mgr.List,
		ProbeUpstream: a.probeUpstreamFunc(factory),
		Config:        a.cfg,
		ProbeXiaoZhi:  probeXiaoZhi,
		Logger:        a.logger,
	})
	detailReporter := health.NewDetailReporter(health.DetailReporterOptions{
		Pinger:        pg,
		ListUpstreams: mgr.List,
		XiaoZhi:       a.xiaozhiConn,
	})

	// --- 管理 REST API 路由器（httpapi）：从各服务装配 Deps ---
	adminRouter := httpapi.NewRouter(httpapi.Deps{
		Upstream:        mgr,
		Refresher:       refresher,
		ToolCache:       toolCache,
		CacheEnsurer:    a.syncer,
		UpstreamTester:  upstreamTester{factory: factory},
		Aggregation:     agg,
		RuleValidator:   ruleEngine,
		AliasStore:      repos.Alias,
		FilterMCPStore:  repos.FilterMCP,
		ToolPolicyStore: repos.ToolPolicy,
		APIKeys:         apiKeyMgr,
		APIKeyFilters:   apiKeyFilterMgr,
		APIKeyUpstreams: upstreamAccessMgr,
		ACLStore:        repos.ACL,
		RateLimitStore:  repos.APIKey,
		AIRisk:          riskGovernance,
		ToolRiskStore:   repos.ToolRisk,
		Auth:            authSvc,
		Settings:        a.cfg,
		SettingsRuntime: a,
		Backup:          backupSvc,
		ValidateCron:    syncsvc.ValidateCron,
		Stats:           statQuery,
		Audit:           auditSvc,
		AuditRecorder:   a.auditRecorder,
		Security:        securityGuard,
		SystemLogs:      a.systemLogs,
		Templates:       templateMarket,
		RuntimeEnv:      runtimeSvc,
		Scripts:         scriptSvc,
	})

	// --- 入站路由分面装配 ---
	wiring := routerWiring{
		adminRouter:    adminRouter,
		adminAuth:      adminAuth,
		mcpEndpoints:   mcpEndpoints,
		authenticator:  authenticator,
		securityGuard:  securityGuard,
		aclGuard:       aclGuard,
		rateLimiter:    rateLimiter,
		detailReporter: detailReporter,
	}
	a.adminEngine = a.buildAdminRouter(wiring, a.exposeMCPOnAdminAddr)
	if a.publicMCPAddr != "" {
		a.publicMCPEngine = a.buildMCPRouter(wiring, true)
	}
	return nil
}

// retryPolicyFromConfig 把连接配置映射为连接管理器的退避策略（Req 5）。
func retryPolicyFromConfig(c config.ConnectionConfig) manager.RetryPolicy {
	return manager.RetryPolicy{
		ConnectTimeout:          time.Duration(c.ConnectTimeoutS) * time.Second,
		InitialBackoff:          time.Duration(c.RetryInitialBackoffS) * time.Second,
		MaxBackoff:              time.Duration(c.RetryMaxBackoffS) * time.Second,
		Multiplier:              c.RetryMultiplier,
		FailureThreshold:        c.FailureThreshold,
		DemandReconnectCooldown: time.Duration(c.DemandReconnectCooldownS) * time.Second,
		DemandReconnectWait:     time.Duration(c.DemandReconnectWaitS) * time.Second,
	}
}

// xiaozhiBackoffFromConfig 复用连接退避配置作为小智重连退避策略（Req 15.4）。
func xiaozhiBackoffFromConfig(c config.ConnectionConfig) xiaozhi.BackoffPolicy {
	return xiaozhi.BackoffPolicy{
		Initial:    time.Duration(c.RetryInitialBackoffS) * time.Second,
		Max:        time.Duration(c.RetryMaxBackoffS) * time.Second,
		Multiplier: c.RetryMultiplier,
	}
}

// resolveAPIKeyID 从已鉴权的 gin 上下文取出 API Key 标识，供对外 MCP 端点按 Key 视角构建 server。
func resolveAPIKeyID(c *gin.Context) (string, bool) {
	m, ok := apikey.MetadataFromContext(c)
	if !ok {
		return "", false
	}
	return m.ID, true
}

// probeUpstreamFunc 返回启动连通性探测使用的单上游探测函数：临时建立会话以验证连通性。
func (a *App) probeUpstreamFunc(factory transport.TransportFactory) health.UpstreamProbeFunc {
	return func(ctx context.Context, up domain.Upstream) error {
		// up 来自 manager.List；从仓储取回完整行（含明文凭证）后再拨号探测。
		row, err := a.repoUpstreamGet(ctx, up.ID)
		if err != nil {
			return err
		}
		sess, err := factory.NewSession(row.Config)
		if err != nil {
			return err
		}
		defer func() { _ = sess.Close() }()
		return sess.Connect(ctx)
	}
}
