package config

// YAMLConfig 表示存放在 data 目录下 YAML 文件中的常规配置（Req 18.2、23.1）。
//
// 数据库与 Redis 连接、加密主密钥来自环境变量，不包含在此结构内。
type YAMLConfig struct {
	// Server 为本进程监听地址与端口隔离配置。
	Server ServerConfig `yaml:"server" json:"server"`
	// Admin 为管理员凭证配置（Req 1）。
	Admin AdminConfig `yaml:"admin" json:"admin"`
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
	// XiaoZhi 为小智接入配置（Req 15）。
	XiaoZhi XiaoZhiConfig `yaml:"xiaozhi" json:"xiaozhi"`
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
}

// MCPAPIConfig 为对外 MCP API 配置（Req 11）。
type MCPAPIConfig struct {
	// SmartDiscoveryLimit 为智能模式工具发现返回数，范围 1 至 200，默认 50（Req 11.4）。
	SmartDiscoveryLimit int `yaml:"smart_discovery_limit" json:"smart_discovery_limit"`
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

// XiaoZhiConfig 为小智接入配置（Req 15）。
type XiaoZhiConfig struct {
	// Enabled 表示是否启用小智接入。
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Endpoint 为小智 MCP 接入点地址，需为 ws:// 或 wss:// 合法 URL（Req 15.6）。
	Endpoint string `yaml:"endpoint" json:"endpoint"`
	// Mode 为小智使用的对外 MCP 模式：smart 或 full，默认 full。
	Mode     string `yaml:"mode" json:"mode"`
}

// 对外模式取值常量（Req 11）。
const (
	// ModeSmart 为智能模式，仅暴露少量网关工具。
	ModeSmart = "smart"
	// ModeFull 为全量模式，一次性暴露全部聚合工具。
	ModeFull = "full"
)

// 日志级别取值常量。与 slog 级别对应，空串视为默认 info。
const (
	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"
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
		},
		MCPAPI: MCPAPIConfig{
			SmartDiscoveryLimit: 50,
		},
		Statistics: StatisticsConfig{
			TopLimitDefault: 10,
			RetentionDays:   90,
		},
		Audit: AuditConfig{
			PageSizeDefault: 20,
			RetentionDays:   180,
		},
		XiaoZhi: XiaoZhiConfig{
			Enabled:  false,
			Endpoint: "",
			Mode:     ModeFull,
		},
	}
}
