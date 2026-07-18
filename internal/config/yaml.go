package config

import (
	"strings"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// YAMLConfig 表示存放在 data 目录下 YAML 文件中的常规配置（Req 18.2、23.1）。
//
// 数据库与 Redis 连接来自环境变量，不包含在此结构内。
type YAMLConfig struct {
	// Server 为本进程监听地址与端口隔离配置。
	Server ServerConfig `yaml:"server" json:"server"`
	// Admin 为管理员凭证配置（Req 1）。
	Admin AdminConfig `yaml:"admin" json:"admin"`
	// JWTSecret 为管理员登录 JWT 的 HS256 签名密钥；为空时首启自动生成并写回本文件。
	JWTSecret string `yaml:"jwt_secret" json:"jwt_secret"`
	// Auth 为认证会话配置（Req 1.4、1.7）。
	Auth AuthConfig `yaml:"auth" json:"auth"`
	// Sync 为工具同步调度配置（Req 7）。
	Sync SyncConfig `yaml:"sync" json:"sync"`
	// Connection 为上游连接与重试配置（Req 4、5）。
	Connection ConnectionConfig `yaml:"connection" json:"connection"`
	// Aggregation 为聚合调用配置（Req 10）。
	Aggregation AggregationConfig `yaml:"aggregation" json:"aggregation"`
	// MCPAPI 为对外 MCP API 配置（Req 11）。
	MCPAPI MCPAPIConfig `yaml:"mcp_api" json:"mcp_api"`
	// Statistics 为统计服务配置（Req 16）。
	Statistics StatisticsConfig `yaml:"statistics" json:"statistics"`
	// Audit 为审计日志配置（Req 22）。
	Audit AuditConfig `yaml:"audit" json:"audit"`
	// Security 为对外 MCP API 的安全防护配置。
	Security SecurityConfig `yaml:"security" json:"security"`
	// Runtime 为本地 stdio 运行时安全策略（命令白名单、启停等）。
	Runtime RuntimeConfig `yaml:"runtime" json:"runtime"`
	// XiaoZhi 为小智接入配置（Req 15）。
	XiaoZhi XiaoZhiConfig `yaml:"xiaozhi" json:"xiaozhi"`
}

// RuntimeConfig 控制网关进程内 stdio 上游的本地执行策略。
//
// 热生效：transport / 运行环境页每次读取配置快照，无需重启进程。
// 远程 SSE/HTTP/WS/OpenAPI 不受本段配置影响。
type RuntimeConfig struct {
	// StdioEnabled 为 false 时拒绝创建与连接本地 stdio 上游，默认 true。
	StdioEnabled bool `yaml:"stdio_enabled" json:"stdio_enabled"`
	// CommandAllowlist 为允许的 stdio 可执行文件基名；空则使用内置默认列表。
	CommandAllowlist []string `yaml:"command_allowlist" json:"command_allowlist"`
	// ExtraSensitiveEnvPrefixes 追加到内置敏感环境变量前缀（仅剥离父进程继承项）。
	ExtraSensitiveEnvPrefixes []string `yaml:"extra_sensitive_env_prefixes" json:"extra_sensitive_env_prefixes"`
	// ProcessHardening 为 true 时对 stdio 子进程应用平台可用的进程隔离（默认 true）。
	// 使用指针以区分「缺省」与显式 false；Normalize 时缺省回填 true。
	ProcessHardening *bool `yaml:"process_hardening" json:"process_hardening"`
	// DefaultStdioSecurityMode 为上游未声明档位时的默认：standard | strict | unrestricted。
	DefaultStdioSecurityMode string `yaml:"default_stdio_security_mode" json:"default_stdio_security_mode"`
	// StrictCommandAllowlist 为严格档命令子集（与 CommandAllowlist 取交集）；空则内置默认。
	StrictCommandAllowlist []string `yaml:"strict_command_allowlist" json:"strict_command_allowlist"`
	// StrictPackageAllowlist 为严格档允许 npx/uvx 执行的包/工具名；支持 @scope/*；空则内置默认。
	StrictPackageAllowlist []string `yaml:"strict_package_allowlist" json:"strict_package_allowlist"`
	// GlobalFileRoots 为文件允许路径的全局默认根。
	GlobalFileRoots []string `yaml:"global_file_roots" json:"global_file_roots"`
	// BrowseExtraRoots 为管理台路径选择器额外可浏览根（仅扩大浏览范围，不改变 stdio 安全策略）。
	BrowseExtraRoots []string `yaml:"browse_extra_roots" json:"browse_extra_roots"`
	// StrictPathOnlyRuntime 严格档是否仅从 runtime 卷解析命令。
	StrictPathOnlyRuntime *bool `yaml:"strict_path_only_runtime" json:"strict_path_only_runtime"`
	// StrictNetworkDefault 严格档默认网络策略：allowlist | deny。
	StrictNetworkDefault string `yaml:"strict_network_default" json:"strict_network_default"`
	// StrictAllowPolicyOnly 无内核隔离时是否允许严格档仅策略运行。
	StrictAllowPolicyOnly *bool `yaml:"strict_allow_policy_only" json:"strict_allow_policy_only"`
}

// ServerConfig 为 HTTP 服务监听配置。
type ServerConfig struct {
	// AdminAddr 为管理台与管理 API 监听地址，默认 :8080。
	AdminAddr string `yaml:"admin_addr" json:"admin_addr"`
	// PublicMCPAddr 为独立对外 MCP 服务监听地址；为空表示不单独监听。
	PublicMCPAddr string `yaml:"public_mcp_addr" json:"public_mcp_addr"`
	// ExposeMCPOnAdminAddr 表示是否仍在管理端口暴露 /mcp/*，默认 true 兼容旧部署。
	ExposeMCPOnAdminAddr bool `yaml:"expose_mcp_on_admin_addr" json:"expose_mcp_on_admin_addr"`
	// LogLevel 为进程日志级别：debug/info/warn/error，默认 info。
	// 通过管理台保存设置后即时生效（基于 slog.LevelVar），无需重启进程。
	LogLevel string `yaml:"log_level" json:"log_level"`
}

// AdminConfig 为管理员凭证配置（Req 1）。
type AdminConfig struct {
	// Username 为管理员用户名，长度需在 3 至 32 个字符之间（Req 1.2）。
	Username string `yaml:"username" json:"username"`
	// PasswordHash 为管理员密码的 bcrypt 加盐哈希（Req 1.2）。
	PasswordHash string `yaml:"password_hash" json:"password_hash"`
	// Initialized 为首次初始化标志；为 false 时进入注册入口（Req 1.1）。
	Initialized bool `yaml:"initialized" json:"initialized"`
}

// AuthConfig 为认证会话配置（Req 1.4、1.7）。
type AuthConfig struct {
	// SessionTimeoutS 为会话超时秒数，范围 300 至 86400，默认 3600。
	SessionTimeoutS int `yaml:"session_timeout_s" json:"session_timeout_s"`
}

// SyncConfig 为工具同步调度配置（Req 7）。
type SyncConfig struct {
	// Cron 为标准 cron 表达式，校验通过后才持久化（Req 7.3）。
	Cron string `yaml:"cron" json:"cron"`
	// TimeoutS 为同步超时秒数，范围 5 至 300，默认 30（Req 7.5）。
	TimeoutS int `yaml:"timeout_s" json:"timeout_s"`
}

// ConnectionConfig 为上游连接与重试配置（Req 4、5）。
type ConnectionConfig struct {
	// ConnectTimeoutS 为连接建立超时秒数，默认 30（Req 4.9）。
	ConnectTimeoutS int `yaml:"connect_timeout_s" json:"connect_timeout_s"`
	// RetryInitialBackoffS 为初始退避秒数，范围 1 至 60，默认 5（Req 5.1）。
	RetryInitialBackoffS int `yaml:"retry_initial_backoff_s" json:"retry_initial_backoff_s"`
	// RetryMultiplier 为退避倍数，需大于等于 1，默认 5（Req 5.1）。
	RetryMultiplier int `yaml:"retry_multiplier" json:"retry_multiplier"`
	// RetryMaxBackoffS 为退避上限秒数，范围 1 至 86400，默认 3600（Req 5.3）。
	RetryMaxBackoffS int `yaml:"retry_max_backoff_s" json:"retry_max_backoff_s"`
	// FailureThreshold 为连续失败阈值，范围 1 至 100，默认 10（Req 5.6）。
	FailureThreshold int `yaml:"failure_threshold" json:"failure_threshold"`
}

// AggregationConfig 为聚合调用配置（Req 10）。
type AggregationConfig struct {
	// UpstreamCallTimeoutS 为上游调用超时秒数，范围 1 至 600，默认 30（Req 10.8）。
	UpstreamCallTimeoutS int `yaml:"upstream_call_timeout_s" json:"upstream_call_timeout_s"`
	// ToolRoutingStrategy 为同名工具多来源时的调用选择策略。
	ToolRoutingStrategy domain.ToolRoutingStrategy `yaml:"tool_routing_strategy" json:"tool_routing_strategy"`
}

// MCPAPIConfig 为对外 MCP API 配置（Req 11）。
type MCPAPIConfig struct {
	// SmartDiscoveryLimit 为智能模式工具发现返回数，范围 1 至 200，默认 50（Req 11.4）。
	SmartDiscoveryLimit int `yaml:"smart_discovery_limit" json:"smart_discovery_limit"`
	// RequestBodyLimitMiB 为单次对外 MCP POST 请求体上限，单位 MiB。
	RequestBodyLimitMiB int `yaml:"request_body_limit_mib" json:"request_body_limit_mib"`
}

// StatisticsConfig 为统计服务配置（Req 16）。
type StatisticsConfig struct {
	// TopLimitDefault 为工具排行默认条数，范围 1 至 100，默认 10（Req 16.3）。
	TopLimitDefault int `yaml:"top_limit_default" json:"top_limit_default"`
	// RetentionDays 为统计保留天数，范围 1 至 3650，默认 90（Req 16.10）。
	RetentionDays int `yaml:"retention_days" json:"retention_days"`
}

// AuditConfig 为审计日志配置（Req 22）。
type AuditConfig struct {
	// PageSizeDefault 为审计分页默认每页条数，范围 1 至 200，默认 20（Req 22.4）。
	PageSizeDefault int `yaml:"page_size_default" json:"page_size_default"`
	// RetentionDays 为审计保留天数，范围 1 至 3650，默认 180（Req 22.5）。
	RetentionDays int `yaml:"retention_days" json:"retention_days"`
}

// SecurityConfig 为对外 MCP API 的鉴权失败防护配置。
type SecurityConfig struct {
	// Mode 为防护模式：off/monitor/enforce。
	Mode string `yaml:"mode" json:"mode"`
	// FailureWindowS 为失败计数窗口秒数。
	FailureWindowS int `yaml:"failure_window_s" json:"failure_window_s"`
	// MaxFailuresPerIP 为单 IP 在窗口内允许的鉴权失败次数。
	MaxFailuresPerIP int `yaml:"max_failures_per_ip" json:"max_failures_per_ip"`
	// MaxFailuresPerKeyFingerprint 为同一疑似 Key 指纹在窗口内允许的失败次数。
	MaxFailuresPerKeyFingerprint int `yaml:"max_failures_per_key_fingerprint" json:"max_failures_per_key_fingerprint"`
	// MaxACLDeniesPerKeyIP 为同一 API Key + IP 在窗口内允许的 ACL 拒绝次数。
	MaxACLDeniesPerKeyIP int `yaml:"max_acl_denies_per_key_ip" json:"max_acl_denies_per_key_ip"`
	// FirstBlockDurationS 为首次自动封禁秒数。
	FirstBlockDurationS int `yaml:"first_block_duration_s" json:"first_block_duration_s"`
	// MaxBlockDurationS 为自动封禁最长秒数。
	MaxBlockDurationS int `yaml:"max_block_duration_s" json:"max_block_duration_s"`
	// EscalationWindowS 为重复封禁升级观察窗口秒数。
	EscalationWindowS int `yaml:"escalation_window_s" json:"escalation_window_s"`
	// TrustedProxyCIDRs 为可信代理出口；只有这些来源的转发头会被安全中心采信。
	TrustedProxyCIDRs []string `yaml:"trusted_proxy_cidrs" json:"trusted_proxy_cidrs"`
	// ExemptCIDRs 为自动封禁豁免来源。
	ExemptCIDRs []string `yaml:"exempt_cidrs" json:"exempt_cidrs"`
}

// XiaoZhiConfig 为小智接入配置（Req 15）。
type XiaoZhiConfig struct {
	// Enabled 表示是否启用小智接入。
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Endpoint 为小智 MCP 接入点地址，需为 ws:// 或 wss:// 合法 URL（Req 15.6）。
	Endpoint string `yaml:"endpoint" json:"endpoint"`
	// Mode 为小智使用的对外 MCP 模式：smart 或 full，默认 full。
	Mode string `yaml:"mode" json:"mode"`
}

// 对外模式取值常量（Req 11）。
const (
	// ModeSmart 为智能模式，仅暴露少量网关工具。
	ModeSmart = "smart"
	// ModeFull 为全量模式，一次性暴露全部聚合工具。
	ModeFull = "full"
)

const (
	DefaultMCPRequestBodyLimitMiB = 8
	MinMCPRequestBodyLimitMiB     = 1
	MaxMCPRequestBodyLimitMiB     = 256
)

// 日志级别取值常量。与 slog 级别对应，空串视为默认 info。
const (
	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"
)

const (
	SecurityModeOff     = "off"
	SecurityModeMonitor = "monitor"
	SecurityModeEnforce = "enforce"
)

// ValidLogLevel 判断给定字符串是否为合法的日志级别取值（不含空串）。
func ValidLogLevel(s string) bool {
	switch s {
	case LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
		return true
	default:
		return false
	}
}

// NormalizeLogLevel 把日志级别归一化：空串或非法值回退为默认 info。
func NormalizeLogLevel(s string) string {
	if ValidLogLevel(s) {
		return s
	}
	return LogLevelInfo
}

// ValidSecurityMode 判断给定值是否为合法安全防护模式。
func ValidSecurityMode(s string) bool {
	switch s {
	case SecurityModeOff, SecurityModeMonitor, SecurityModeEnforce:
		return true
	default:
		return false
	}
}

func defaultSecurityConfig() SecurityConfig {
	return SecurityConfig{
		Mode:                         SecurityModeMonitor,
		FailureWindowS:               300,
		MaxFailuresPerIP:             30,
		MaxFailuresPerKeyFingerprint: 8,
		MaxACLDeniesPerKeyIP:         5,
		FirstBlockDurationS:          900,
		MaxBlockDurationS:            86400,
		EscalationWindowS:            86400,
		TrustedProxyCIDRs:            []string{},
		ExemptCIDRs:                  []string{},
	}
}

func defaultRuntimeConfig() RuntimeConfig {
	hardening := true
	pathOnly := true
	policyOnly := true
	return RuntimeConfig{
		StdioEnabled: true,
		// 与模板市场常用命令对齐；空列表在 Normalize 时也会回填。
		CommandAllowlist: []string{
			"node", "npx", "npm", "python", "python3", "uv", "uvx", "docker",
		},
		ExtraSensitiveEnvPrefixes: []string{},
		ProcessHardening:          &hardening,
		DefaultStdioSecurityMode:  "standard",
		StrictCommandAllowlist: []string{
			"node", "npx", "python", "python3", "uv", "uvx",
		},
		// 与内置模板常用 npx 包对齐；支持 @scope/*。
		StrictPackageAllowlist: []string{
			"@modelcontextprotocol/*",
			"@playwright/mcp",
			"@notionhq/notion-mcp-server",
			"firecrawl-mcp",
			"exa-mcp-server",
		},
		GlobalFileRoots:       []string{},
		BrowseExtraRoots:      []string{},
		StrictPathOnlyRuntime: &pathOnly,
		StrictNetworkDefault:  "allowlist",
		StrictAllowPolicyOnly: &policyOnly,
	}
}

// DefaultYAMLConfig 返回带有设计文档约定默认值的 YAML 配置（Req 18.5）。
//
// 该默认配置在 YAML 文件不存在时用于创建初始配置文件；其中管理员凭证为空、
// initialized 为 false，以触发首次初始化注册流程（Req 1.1）。
func DefaultYAMLConfig() YAMLConfig {
	return YAMLConfig{
		Server: ServerConfig{
			AdminAddr:            ":8080",
			PublicMCPAddr:        "",
			ExposeMCPOnAdminAddr: true,
			LogLevel:             LogLevelInfo,
		},
		Admin: AdminConfig{
			Username:     "",
			PasswordHash: "",
			Initialized:  false,
		},
		// JWTSecret 为空，由首启 EnsureJWTSecret 自动生成并写回。
		JWTSecret: "",
		Auth: AuthConfig{
			SessionTimeoutS: 3600,
		},
		Sync: SyncConfig{
			Cron:     "0 */30 * * * *",
			TimeoutS: 30,
		},
		Connection: ConnectionConfig{
			ConnectTimeoutS:      30,
			RetryInitialBackoffS: 5,
			RetryMultiplier:      5,
			RetryMaxBackoffS:     3600,
			FailureThreshold:     10,
		},
		Aggregation: AggregationConfig{
			UpstreamCallTimeoutS: 30,
			ToolRoutingStrategy:  domain.ToolRoutingSmartBalance,
		},
		MCPAPI: MCPAPIConfig{
			SmartDiscoveryLimit: 50,
			RequestBodyLimitMiB: DefaultMCPRequestBodyLimitMiB,
		},
		Statistics: StatisticsConfig{
			TopLimitDefault: 10,
			RetentionDays:   90,
		},
		Audit: AuditConfig{
			PageSizeDefault: 20,
			RetentionDays:   180,
		},
		Security: defaultSecurityConfig(),
		Runtime:  defaultRuntimeConfig(),
		XiaoZhi: XiaoZhiConfig{
			Enabled:  false,
			Endpoint: "",
			Mode:     ModeFull,
		},
	}
}

// NormalizeYAMLConfig 补齐旧配置文件中可能缺省的枚举类字段，同时保留显式合法取值。
func NormalizeYAMLConfig(cfg YAMLConfig) YAMLConfig {
	cfg.Aggregation.ToolRoutingStrategy = domain.NormalizeToolRoutingStrategy(cfg.Aggregation.ToolRoutingStrategy)
	if cfg.MCPAPI.RequestBodyLimitMiB == 0 {
		cfg.MCPAPI.RequestBodyLimitMiB = DefaultMCPRequestBodyLimitMiB
	}
	defSecurity := defaultSecurityConfig()
	if cfg.Security.Mode == "" {
		cfg.Security.Mode = defSecurity.Mode
	}
	if cfg.Security.FailureWindowS == 0 {
		cfg.Security.FailureWindowS = defSecurity.FailureWindowS
	}
	if cfg.Security.MaxFailuresPerIP == 0 {
		cfg.Security.MaxFailuresPerIP = defSecurity.MaxFailuresPerIP
	}
	if cfg.Security.MaxFailuresPerKeyFingerprint == 0 {
		cfg.Security.MaxFailuresPerKeyFingerprint = defSecurity.MaxFailuresPerKeyFingerprint
	}
	if cfg.Security.MaxACLDeniesPerKeyIP == 0 {
		cfg.Security.MaxACLDeniesPerKeyIP = defSecurity.MaxACLDeniesPerKeyIP
	}
	if cfg.Security.FirstBlockDurationS == 0 {
		cfg.Security.FirstBlockDurationS = defSecurity.FirstBlockDurationS
	}
	if cfg.Security.MaxBlockDurationS == 0 {
		cfg.Security.MaxBlockDurationS = defSecurity.MaxBlockDurationS
	}
	if cfg.Security.EscalationWindowS == 0 {
		cfg.Security.EscalationWindowS = defSecurity.EscalationWindowS
	}
	if cfg.Security.TrustedProxyCIDRs == nil {
		cfg.Security.TrustedProxyCIDRs = []string{}
	}
	if cfg.Security.ExemptCIDRs == nil {
		cfg.Security.ExemptCIDRs = []string{}
	}
	defRuntime := defaultRuntimeConfig()
	// 注意：bool 零值 false 与「显式关闭」无法区分；仅在 allowlist 为空时回填默认命令列表。
	// StdioEnabled 默认 true 写在 DefaultYAMLConfig；旧文件缺字段时 Go 零值为 false，
	// 这里对「整段 runtime 未配置」的兼容：若 allowlist 与 extra 皆空且未显式写过，
	// 仍保持文件中的 false 可能误伤。故：allowlist 为空时补默认列表，但不强制改 StdioEnabled。
	// 新装默认 true；从无 Runtime 字段的旧配置升级时，StdioEnabled 会是 false——
	// 为兼容旧部署，当 CommandAllowlist 为 nil（缺省）时视为沿用默认启用。
	if cfg.Runtime.CommandAllowlist == nil {
		cfg.Runtime.StdioEnabled = true
		cfg.Runtime.CommandAllowlist = append([]string{}, defRuntime.CommandAllowlist...)
	} else if len(cfg.Runtime.CommandAllowlist) == 0 {
		// 显式空数组：回退默认列表，避免管理员误配成「允许一切仅 denylist」。
		cfg.Runtime.CommandAllowlist = append([]string{}, defRuntime.CommandAllowlist...)
	}
	if cfg.Runtime.ExtraSensitiveEnvPrefixes == nil {
		cfg.Runtime.ExtraSensitiveEnvPrefixes = []string{}
	}
	if cfg.Runtime.ProcessHardening == nil {
		v := true
		cfg.Runtime.ProcessHardening = &v
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Runtime.DefaultStdioSecurityMode)) {
	case "standard", "strict", "unrestricted":
		cfg.Runtime.DefaultStdioSecurityMode = strings.ToLower(strings.TrimSpace(cfg.Runtime.DefaultStdioSecurityMode))
	default:
		cfg.Runtime.DefaultStdioSecurityMode = defRuntime.DefaultStdioSecurityMode
	}
	if cfg.Runtime.StrictCommandAllowlist == nil {
		cfg.Runtime.StrictCommandAllowlist = append([]string{}, defRuntime.StrictCommandAllowlist...)
	} else if len(cfg.Runtime.StrictCommandAllowlist) == 0 {
		cfg.Runtime.StrictCommandAllowlist = append([]string{}, defRuntime.StrictCommandAllowlist...)
	}
	if cfg.Runtime.StrictPackageAllowlist == nil {
		cfg.Runtime.StrictPackageAllowlist = append([]string{}, defRuntime.StrictPackageAllowlist...)
	} else if len(cfg.Runtime.StrictPackageAllowlist) == 0 {
		cfg.Runtime.StrictPackageAllowlist = append([]string{}, defRuntime.StrictPackageAllowlist...)
	}
	if cfg.Runtime.GlobalFileRoots == nil {
		cfg.Runtime.GlobalFileRoots = []string{}
	}
	if cfg.Runtime.BrowseExtraRoots == nil {
		cfg.Runtime.BrowseExtraRoots = []string{}
	}
	if cfg.Runtime.StrictPathOnlyRuntime == nil {
		v := true
		cfg.Runtime.StrictPathOnlyRuntime = &v
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Runtime.StrictNetworkDefault)) {
	case "deny", "allowlist":
		cfg.Runtime.StrictNetworkDefault = strings.ToLower(strings.TrimSpace(cfg.Runtime.StrictNetworkDefault))
	default:
		cfg.Runtime.StrictNetworkDefault = defRuntime.StrictNetworkDefault
	}
	if cfg.Runtime.StrictAllowPolicyOnly == nil {
		v := true
		cfg.Runtime.StrictAllowPolicyOnly = &v
	}
	return cfg
}
