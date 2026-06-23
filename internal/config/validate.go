package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// rangeCheck 校验某个整数字段是否落在闭区间 [min, max] 内；越界时向 fields 写入错误说明。
func rangeCheck(fields map[string]string, name string, value, min, max int) {
	if value < min || value > max {
		fields[name] = fmt.Sprintf("取值 %d 超出合法范围 [%d, %d]", value, min, max)
	}
}

// ValidateYAMLConfig 校验 YAML 常规配置各字段的取值范围（Req 18.6 的语义校验部分）。
//
// 该函数为独立纯函数，便于单元测试；返回的错误为携带字段级说明的校验类
// APIError，调用方据此记录错误并终止启动。校验通过时返回 nil。
//
// 注意：管理员凭证（Admin）在首次初始化前为空，故此处不强制校验，
// 由认证服务在注册时按 Req 1.2 校验用户名与密码长度。
func ValidateYAMLConfig(cfg YAMLConfig) error {
	fields := make(map[string]string)

	// server.admin_addr 是管理台监听地址，必须可被 net/http 作为监听地址解析。
	if err := validateListenAddr(cfg.Server.AdminAddr, true); err != nil {
		fields["server.admin_addr"] = err.Error()
	}
	// server.public_mcp_addr 为空表示不启用独立端口；非空时需合法且不能与管理端口相同。
	if err := validateListenAddr(cfg.Server.PublicMCPAddr, false); err != nil {
		fields["server.public_mcp_addr"] = err.Error()
	}
	if strings.TrimSpace(cfg.Server.PublicMCPAddr) != "" && cfg.Server.PublicMCPAddr == cfg.Server.AdminAddr {
		fields["server.public_mcp_addr"] = "独立 MCP 监听地址不能与管理监听地址相同"
	}
	if strings.TrimSpace(cfg.Server.PublicMCPAddr) == "" && !cfg.Server.ExposeMCPOnAdminAddr {
		fields["server.public_mcp_addr"] = "关闭管理端口 MCP 入口前，必须先配置独立 MCP 监听地址"
	}
	// server.log_level 取值 debug/info/warn/error，默认 info；空串视为默认放行。
	if cfg.Server.LogLevel != "" && !ValidLogLevel(cfg.Server.LogLevel) {
		fields["server.log_level"] = "日志级别取值非法（应为 debug/info/warn/error）"
	}

	// auth.session_timeout_s 范围 300-86400（Req 1.4、1.7）。
	rangeCheck(fields, "auth.session_timeout_s", cfg.Auth.SessionTimeoutS, 300, 86400)

	// sync.cron 不能为空；标准 cron 格式的严格校验在同步服务任务（10.1）中处理（Req 7.3）。
	if strings.TrimSpace(cfg.Sync.Cron) == "" {
		fields["sync.cron"] = "cron 表达式不能为空"
	}
	// sync.timeout_s 范围 5-300（Req 7.5）。
	rangeCheck(fields, "sync.timeout_s", cfg.Sync.TimeoutS, 5, 300)

	// connection.connect_timeout_s 需为正数（Req 4.9）。
	if cfg.Connection.ConnectTimeoutS < 1 {
		fields["connection.connect_timeout_s"] = "连接建立超时需为正整数"
	}
	// connection.retry_initial_backoff_s 范围 1-60（Req 5.1）。
	rangeCheck(fields, "connection.retry_initial_backoff_s", cfg.Connection.RetryInitialBackoffS, 1, 60)
	// connection.retry_multiplier 需大于等于 1（Req 5.1）。
	if cfg.Connection.RetryMultiplier < 1 {
		fields["connection.retry_multiplier"] = "退避倍数需大于等于 1"
	}
	// connection.retry_max_backoff_s 范围 1-86400（Req 5.3）。
	rangeCheck(fields, "connection.retry_max_backoff_s", cfg.Connection.RetryMaxBackoffS, 1, 86400)
	// connection.failure_threshold 范围 1-100（Req 5.6）。
	rangeCheck(fields, "connection.failure_threshold", cfg.Connection.FailureThreshold, 1, 100)

	// aggregation.upstream_call_timeout_s 范围 1-600（Req 10.8）。
	rangeCheck(fields, "aggregation.upstream_call_timeout_s", cfg.Aggregation.UpstreamCallTimeoutS, 1, 600)
	if cfg.Aggregation.ToolRoutingStrategy == "" {
		cfg.Aggregation.ToolRoutingStrategy = domain.ToolRoutingRoundRobin
	}
	if !domain.ValidToolRoutingStrategy(cfg.Aggregation.ToolRoutingStrategy) {
		fields["aggregation.tool_routing_strategy"] = "工具调用策略取值非法（应为 priority_fill 或 round_robin）"
	}

	// xiaozhi.mode 取值 smart 或 full，默认 full。
	if cfg.XiaoZhi.Mode != "" && cfg.XiaoZhi.Mode != ModeSmart && cfg.XiaoZhi.Mode != ModeFull {
		fields["xiaozhi.mode"] = fmt.Sprintf("小智接入模式取值非法（应为 %q 或 %q）", ModeSmart, ModeFull)
	}
	// mcp_api.smart_discovery_limit 范围 1-200（Req 11.4）。
	rangeCheck(fields, "mcp_api.smart_discovery_limit", cfg.MCPAPI.SmartDiscoveryLimit, 1, 200)

	// statistics.top_limit_default 范围 1-100（Req 16.3）。
	rangeCheck(fields, "statistics.top_limit_default", cfg.Statistics.TopLimitDefault, 1, 100)
	// statistics.retention_days 范围 1-3650（Req 16.10）。
	rangeCheck(fields, "statistics.retention_days", cfg.Statistics.RetentionDays, 1, 3650)

	// audit.page_size_default 范围 1-200（Req 22.4）。
	rangeCheck(fields, "audit.page_size_default", cfg.Audit.PageSizeDefault, 1, 200)
	// audit.retention_days 范围 1-3650（Req 22.5）。
	rangeCheck(fields, "audit.retention_days", cfg.Audit.RetentionDays, 1, 3650)

	// xiaozhi.endpoint：启用时必须为 ws:// 或 wss:// 合法 URL（Req 15.6）。
	if cfg.XiaoZhi.Enabled {
		if err := validateXiaoZhiEndpoint(cfg.XiaoZhi.Endpoint); err != nil {
			fields["xiaozhi.endpoint"] = err.Error()
		}
	}

	if len(fields) > 0 {
		return domain.NewValidationError("YAML 配置校验失败", fields)
	}
	return nil
}

// validateListenAddr 校验监听地址是否适合作为 net/http Server.Addr。
func validateListenAddr(addr string, required bool) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		if required {
			return fmt.Errorf("监听地址不能为空")
		}
		return nil
	}
	if strings.Contains(addr, "://") {
		return fmt.Errorf("监听地址只填写 host:port 或 :port，不要包含协议")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("监听地址格式应为 host:port 或 :port")
	}
	if port == "" {
		return fmt.Errorf("监听地址必须包含端口")
	}
	if _, err := net.LookupPort("tcp", port); err != nil {
		return fmt.Errorf("监听端口非法")
	}
	if host != "" && net.ParseIP(host) == nil && strings.Contains(host, " ") {
		return fmt.Errorf("监听主机名非法")
	}
	return nil
}

// validateXiaoZhiEndpoint 校验小智接入点地址是否为 ws:// 或 wss:// 合法 WebSocket URL（Req 15.6）。
func validateXiaoZhiEndpoint(endpoint string) error {
	if strings.TrimSpace(endpoint) == "" {
		return fmt.Errorf("启用小智接入时接入点地址不能为空")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("接入点地址不是合法 URL：%v", err)
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return fmt.Errorf("接入点地址协议必须为 ws:// 或 wss://")
	}
	if u.Host == "" {
		return fmt.Errorf("接入点地址缺少主机名")
	}
	return nil
}
