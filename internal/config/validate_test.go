package config

import (
	"errors"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// TestValidateYAMLConfigAcceptsDefault 验证默认配置应通过校验（Req 18.5）。
func TestValidateYAMLConfigAcceptsDefault(t *testing.T) {
	if err := ValidateYAMLConfig(DefaultYAMLConfig()); err != nil {
		t.Fatalf("默认配置应通过校验，却返回错误：%v", err)
	}
}

// TestValidateYAMLConfigReportsFieldErrors 验证多个字段越界时，返回的校验错误
// 在 Fields 中逐一标注出对应字段（Req 18.6 的语义校验部分）。
func TestValidateYAMLConfigReportsFieldErrors(t *testing.T) {
	cfg := DefaultYAMLConfig()
	// 同时构造多处越界，验证字段级错误能聚合返回。
	cfg.Auth.SessionTimeoutS = 100          // < 300
	cfg.Sync.TimeoutS = 1                   // < 5
	cfg.Connection.RetryInitialBackoffS = 0 // < 1
	cfg.Server.AdminAddr = "http://:8080"   // 含协议非法
	cfg.MCPAPI.SmartDiscoveryLimit = 0      // < 1
	cfg.MCPAPI.RequestBodyLimitMiB = 999    // > 256
	cfg.Statistics.RetentionDays = 99999    // > 3650
	cfg.Audit.PageSizeDefault = 0           // < 1

	err := ValidateYAMLConfig(cfg)
	if err == nil {
		t.Fatal("越界配置应返回校验错误，却返回 nil")
	}

	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望错误类型为 *domain.APIError，实际为 %T", err)
	}
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
	}

	wantFields := []string{
		"auth.session_timeout_s",
		"sync.timeout_s",
		"connection.retry_initial_backoff_s",
		"server.admin_addr",
		"mcp_api.smart_discovery_limit",
		"mcp_api.request_body_limit_mib",
		"statistics.retention_days",
		"audit.page_size_default",
	}
	for _, f := range wantFields {
		if _, ok := apiErr.Fields[f]; !ok {
			t.Errorf("期望字段级错误包含 %q，实际 Fields=%v", f, apiErr.Fields)
		}
	}
}

// TestValidateYAMLConfigEmptyCron 验证 sync.cron 为空时返回字段级错误（Req 7.3）。
func TestValidateYAMLConfigEmptyCron(t *testing.T) {
	cfg := DefaultYAMLConfig()
	cfg.Sync.Cron = "   " // 仅空白也视为空

	err := ValidateYAMLConfig(cfg)
	if err == nil {
		t.Fatal("空 cron 应返回校验错误，却返回 nil")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望错误类型为 *domain.APIError，实际为 %T", err)
	}
	if _, ok := apiErr.Fields["sync.cron"]; !ok {
		t.Errorf("期望字段级错误包含 sync.cron，实际 Fields=%v", apiErr.Fields)
	}
}

// TestValidateYAMLConfigBoundaryValues 验证范围校验的上下边界为闭区间（含端点）。
func TestValidateYAMLConfigBoundaryValues(t *testing.T) {
	// 取各字段合法范围的端点值，应当全部通过校验。
	cfg := DefaultYAMLConfig()
	cfg.Auth.SessionTimeoutS = 300             // 下限
	cfg.Sync.TimeoutS = 300                    // 上限
	cfg.Connection.RetryInitialBackoffS = 60   // 上限
	cfg.Connection.RetryMaxBackoffS = 86400    // 上限
	cfg.Connection.FailureThreshold = 100      // 上限
	cfg.Aggregation.UpstreamCallTimeoutS = 600 // 上限
	cfg.MCPAPI.SmartDiscoveryLimit = 1         // 下限
	cfg.MCPAPI.RequestBodyLimitMiB = MaxMCPRequestBodyLimitMiB
	cfg.Statistics.TopLimitDefault = 100 // 上限
	cfg.Audit.RetentionDays = 3650       // 上限

	if err := ValidateYAMLConfig(cfg); err != nil {
		t.Fatalf("范围端点值应通过校验，却返回错误：%v", err)
	}
}

func TestValidateYAMLConfigRoutingStrategies(t *testing.T) {
	for _, strategy := range []domain.ToolRoutingStrategy{
		domain.ToolRoutingSmartBalance,
		domain.ToolRoutingPriorityFill,
		domain.ToolRoutingRoundRobin,
	} {
		t.Run(string(strategy), func(t *testing.T) {
			cfg := DefaultYAMLConfig()
			cfg.Aggregation.ToolRoutingStrategy = strategy
			if err := ValidateYAMLConfig(cfg); err != nil {
				t.Fatalf("路由策略 %q 应通过校验：%v", strategy, err)
			}
		})
	}

	cfg := DefaultYAMLConfig()
	cfg.Aggregation.ToolRoutingStrategy = "weighted"
	err := ValidateYAMLConfig(cfg)
	if err == nil {
		t.Fatal("非法路由策略应返回校验错误")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望错误类型为 *domain.APIError，实际为 %T", err)
	}
	if _, ok := apiErr.Fields["aggregation.tool_routing_strategy"]; !ok {
		t.Fatalf("期望字段级错误包含 aggregation.tool_routing_strategy，实际 Fields=%v", apiErr.Fields)
	}
}

// TestValidateServerListenConfig 验证管理端口与独立 MCP 端口监听配置校验。
func TestValidateServerListenConfig(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*YAMLConfig)
		field   string
		wantErr bool
	}{
		{name: "默认配置合法", mutate: func(*YAMLConfig) {}, wantErr: false},
		{name: "独立 MCP 端口为空表示不启用", mutate: func(c *YAMLConfig) { c.Server.PublicMCPAddr = "" }, wantErr: false},
		{name: "独立 MCP 端口合法", mutate: func(c *YAMLConfig) { c.Server.PublicMCPAddr = ":8081" }, wantErr: false},
		{name: "管理端口不能为空", mutate: func(c *YAMLConfig) { c.Server.AdminAddr = "" }, field: "server.admin_addr", wantErr: true},
		{name: "独立 MCP 地址不能带协议", mutate: func(c *YAMLConfig) { c.Server.PublicMCPAddr = "http://127.0.0.1:8081" }, field: "server.public_mcp_addr", wantErr: true},
		{name: "独立 MCP 地址不能与管理地址相同", mutate: func(c *YAMLConfig) { c.Server.PublicMCPAddr = c.Server.AdminAddr }, field: "server.public_mcp_addr", wantErr: true},
		{name: "关闭管理端口 MCP 前必须配置独立端口", mutate: func(c *YAMLConfig) { c.Server.ExposeMCPOnAdminAddr = false }, field: "server.public_mcp_addr", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultYAMLConfig()
			tc.mutate(&cfg)
			err := ValidateYAMLConfig(cfg)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("配置应通过校验，却返回错误：%v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("期望返回校验错误，却返回 nil")
			}
			var apiErr *domain.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("期望错误类型为 *domain.APIError，实际为 %T", err)
			}
			if _, ok := apiErr.Fields[tc.field]; !ok {
				t.Fatalf("期望字段级错误包含 %q，实际 Fields=%v", tc.field, apiErr.Fields)
			}
		})
	}
}

// TestValidateServerLogLevel 验证 server.log_level 取值校验：合法枚举通过，非法值报字段错误。
func TestValidateServerLogLevel(t *testing.T) {
	cases := []struct {
		name    string
		level   string
		wantErr bool
	}{
		{name: "默认 info 合法", level: LogLevelInfo, wantErr: false},
		{name: "debug 合法", level: LogLevelDebug, wantErr: false},
		{name: "warn 合法", level: LogLevelWarn, wantErr: false},
		{name: "error 合法", level: LogLevelError, wantErr: false},
		{name: "空串视为默认放行", level: "", wantErr: false},
		{name: "非法取值报错", level: "trace", wantErr: true},
		{name: "大小写敏感报错", level: "INFO", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultYAMLConfig()
			cfg.Server.LogLevel = tc.level

			err := ValidateYAMLConfig(cfg)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("配置应通过校验，却返回错误：%v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("期望返回校验错误，却返回 nil")
			}
			var apiErr *domain.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("期望错误类型为 *domain.APIError，实际为 %T", err)
			}
			if _, ok := apiErr.Fields["server.log_level"]; !ok {
				t.Errorf("期望字段级错误包含 server.log_level，实际 Fields=%v", apiErr.Fields)
			}
		})
	}
}

// TestNormalizeLogLevel 验证 NormalizeLogLevel 对合法值原样返回、空串与非法值回退到 info。
func TestNormalizeLogLevel(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{in: LogLevelDebug, want: LogLevelDebug},
		{in: LogLevelInfo, want: LogLevelInfo},
		{in: LogLevelWarn, want: LogLevelWarn},
		{in: LogLevelError, want: LogLevelError},
		{in: "", want: LogLevelInfo},
		{in: "trace", want: LogLevelInfo},
		{in: "INFO", want: LogLevelInfo},
	}
	for _, tc := range cases {
		if got := NormalizeLogLevel(tc.in); got != tc.want {
			t.Errorf("NormalizeLogLevel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestValidateXiaoZhiEndpoint 验证启用小智接入时的接入点地址协议校验（Req 15.6）。
func TestValidateXiaoZhiEndpoint(t *testing.T) {
	cases := []struct {
		name     string
		enabled  bool
		endpoint string
		wantErr  bool
	}{
		{name: "停用时不校验地址", enabled: false, endpoint: "", wantErr: false},
		{name: "合法 ws 地址", enabled: true, endpoint: "ws://example.com/mcp", wantErr: false},
		{name: "合法 wss 地址", enabled: true, endpoint: "wss://example.com/mcp", wantErr: false},
		{name: "启用时地址为空", enabled: true, endpoint: "", wantErr: true},
		{name: "协议非法", enabled: true, endpoint: "http://example.com", wantErr: true},
		{name: "缺少主机名", enabled: true, endpoint: "ws://", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultYAMLConfig()
			cfg.XiaoZhi.Enabled = tc.enabled
			cfg.XiaoZhi.Endpoint = tc.endpoint

			err := ValidateYAMLConfig(cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatal("期望返回校验错误，却返回 nil")
				}
				var apiErr *domain.APIError
				if !errors.As(err, &apiErr) {
					t.Fatalf("期望错误类型为 *domain.APIError，实际为 %T", err)
				}
				if _, ok := apiErr.Fields["xiaozhi.endpoint"]; !ok {
					t.Errorf("期望字段级错误包含 xiaozhi.endpoint，实际 Fields=%v", apiErr.Fields)
				}
			} else if err != nil {
				t.Fatalf("不期望错误，却返回：%v", err)
			}
		})
	}
}
