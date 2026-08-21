package httpapi

import (
	"context"
	"net/http"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
	"github.com/myGithub/mcp-proxy-gateway/internal/syslog"
)

const (
	diagnosticsFormatVersion = "1"
	diagnosticsRecentLimit   = 50
	redactedValue            = "[redacted]"
)

type diagnosticsBundle struct {
	FormatVersion        string                    `json:"formatVersion"`
	GeneratedAt          time.Time                 `json:"generatedAt"`
	Runtime              diagnosticsRuntime        `json:"runtime"`
	Settings             diagnosticsSettings       `json:"settings"`
	RuntimeServer        config.ServerConfig       `json:"runtimeServer"`
	Upstreams            []diagnosticsUpstream     `json:"upstreams,omitempty"`
	ToolSummaries        []upstreamToolSummary     `json:"toolSummaries,omitempty"`
	SecuritySummary      *store.SecuritySummary    `json:"securitySummary,omitempty"`
	RecentSecurityEvents []store.SecurityEvent     `json:"recentSecurityEvents,omitempty"`
	ActiveSecurityBlocks []store.SecurityBlock     `json:"activeSecurityBlocks,omitempty"`
	RecentCallRecords    []diagnosticsCallRecord   `json:"recentCallRecords,omitempty"`
	SystemLogs           []syslog.Entry            `json:"systemLogs,omitempty"`
	CollectionErrors     []diagnosticsCollectError `json:"collectionErrors,omitempty"`
}

type diagnosticsRuntime struct {
	GoVersion string `json:"goVersion"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
}

type diagnosticsSettings struct {
	Server      config.ServerConfig      `json:"server"`
	Admin       diagnosticsAdminConfig   `json:"admin"`
	Auth        config.AuthConfig        `json:"auth"`
	Sync        config.SyncConfig        `json:"sync"`
	Connection  config.ConnectionConfig  `json:"connection"`
	Aggregation config.AggregationConfig `json:"aggregation"`
	MCPAPI      config.MCPAPIConfig      `json:"mcp_api"`
	Statistics  config.StatisticsConfig  `json:"statistics"`
	Audit       config.AuditConfig       `json:"audit"`
	Security    config.SecurityConfig    `json:"security"`
	XiaoZhi     config.XiaoZhiConfig     `json:"xiaozhi"`
	JWTSecret   string                   `json:"jwt_secret"`
}

type diagnosticsAdminConfig struct {
	Username        string `json:"username"`
	Initialized     bool   `json:"initialized"`
	PasswordHashSet bool   `json:"password_hash_set"`
}

type diagnosticsUpstream struct {
	ID        string                `json:"id"`
	Config    domain.UpstreamConfig `json:"config"`
	State     domain.ConnState      `json:"state"`
	LastError string                `json:"lastError,omitempty"`
	CreatedAt time.Time             `json:"createdAt"`
	UpdatedAt time.Time             `json:"updatedAt"`
}

type diagnosticsCallRecord struct {
	ID           int64     `json:"id"`
	UpstreamID   string    `json:"upstreamId"`
	UpstreamName string    `json:"upstreamName"`
	OriginalName string    `json:"originalName"`
	ExposedName  string    `json:"exposedName"`
	APIKeyID     string    `json:"apiKeyId"`
	APIKeyName   string    `json:"apiKeyName"`
	CalledAt     time.Time `json:"calledAt"`
	LatencyMS    int       `json:"latencyMs"`
	Success      bool      `json:"success"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
	Mode         string    `json:"mode"`
	Source       string    `json:"source"`
}

type diagnosticsCollectError struct {
	Section string `json:"section"`
	Message string `json:"message"`
}

// goroutineLeakProfileName 是 Go 1.27 起随 runtime/pprof 一并提供的 goroutine 泄漏 profile 名。
const goroutineLeakProfileName = "goroutineleak"

// 协程泄漏 profile 支持的输出级别。
//
// 只开放这两级，是因为 runtime/pprof 在 debug >= 2 时会跳过泄漏过滤、退化为全量协程转储
// （等价于普通 goroutine profile）——那与本端点的语义不符，会让正常运行的常驻 worker 被
// 当成泄漏，故不予放行。需要全量协程栈是另一件事，不该由这个端点承担。
const (
	// goroutineLeakDebugBinary 输出 protobuf 二进制，供 go tool pprof 离线分析。
	goroutineLeakDebugBinary = 0
	// goroutineLeakDebugText 输出可直接阅读的文本，含泄漏计数头部与各泄漏协程的调用栈。
	goroutineLeakDebugText = 1
)

func (r *Router) registerDiagnosticsRoutes(g *gin.RouterGroup) {
	g.GET("/diagnostics/export", r.exportDiagnostics)
	g.GET("/diagnostics/goroutine-leaks", r.exportGoroutineLeaks)
}

// exportGoroutineLeaks 导出 goroutine 泄漏 profile。
//
// 「泄漏」在此有严格含义：goroutine 阻塞在某个并发原语（channel、Mutex、Cond 等）上，
// 而该原语已无法被任何可运行的 goroutine 触达，因此永远不可能被唤醒。运行时借垃圾回收
// 的可达性分析判定，故覆盖不到经全局变量可达的原语——检出即确为泄漏，未检出不代表没有。
//
// 本网关有多类长生命周期 goroutine（上游连接重连循环、统计与审计落库 worker、周期同步
// 调度、小智接入连接），正是这个 profile 的适用场景。
//
// 该端点仅注册在管理员 JWT 之下，不进对外 MCP 路由：输出含全部相关 goroutine 的调用栈，
// 属内部实现细节，不应对外暴露。
//
// 采集会抢占 runtime/pprof 的包级锁并触发一轮垃圾回收，属较重操作：仅供排查时手动调用，
// 不宜作为轮询监控项，且同一时刻只允许一次（并发请求直接以 429 拒绝，而非排队各自再触发
// 一轮 GC）。
func (r *Router) exportGoroutineLeaks(c *gin.Context) {
	profile := pprof.Lookup(goroutineLeakProfileName)
	if profile == nil {
		respondError(c, domain.NewError(domain.CodeInternal, "当前 Go 运行时未提供协程泄漏 profile"))
		return
	}

	debug, ok := parseGoroutineLeakDebugLevel(c.Query("debug"))
	if !ok {
		respondError(c, domain.NewValidationError("导出参数非法", map[string]string{
			"debug": "只能为 0（pprof 二进制格式）或 1（文本格式）",
		}))
		return
	}

	if !r.goroutineLeakInFlight.CompareAndSwap(false, true) {
		respondError(c, domain.NewError(domain.CodeRateLimited, "已有一次协程泄漏采集在进行，请稍后重试"))
		return
	}
	defer r.goroutineLeakInFlight.Store(false)

	binary := debug == goroutineLeakDebugBinary
	contentType := "text/plain; charset=utf-8"
	if binary {
		contentType = "application/octet-stream"
		filename := "mpg-goroutine-leaks-" + time.Now().Format("20060102-150405") + ".pprof"
		c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	}
	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(http.StatusOK)

	if err := profile.WriteTo(c.Writer, debug); err != nil {
		// 响应头已发出，无法再改回错误状态码。文本格式下把原因追加到末尾便于当场看到；
		// 二进制格式下不能追加——中文尾巴会让 go tool pprof 报出与真实原因无关的解析错误，
		// 截断的响应体本身就是失败信号。两种情况都记入系统日志以便事后归因。
		if !binary {
			_, _ = c.Writer.WriteString("\n采集协程泄漏 profile 失败：" + err.Error() + "\n")
		}
		_ = c.Error(err)
	}
}

// parseGoroutineLeakDebugLevel 解析输出级别；空值默认文本格式。
//
// 只接受 0 与 1，原因见 goroutineLeakDebugText 的说明。
func parseGoroutineLeakDebugLevel(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return goroutineLeakDebugText, true
	}
	level, err := strconv.Atoi(raw)
	if err != nil || (level != goroutineLeakDebugBinary && level != goroutineLeakDebugText) {
		return 0, false
	}
	return level, true
}

func (r *Router) exportDiagnostics(c *gin.Context) {
	bundle := r.buildDiagnostics(c.Request.Context())
	filename := "mpg-diagnostics-" + time.Now().Format("20060102-150405") + ".json"
	respondJSONDownload(c, filename, bundle)
}

func (r *Router) buildDiagnostics(ctx context.Context) diagnosticsBundle {
	bundle := diagnosticsBundle{
		FormatVersion: diagnosticsFormatVersion,
		GeneratedAt:   time.Now().UTC(),
		Runtime: diagnosticsRuntime{
			GoVersion: runtime.Version(),
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
		},
	}

	if r.settings != nil {
		bundle.Settings = redactSettings(r.settings.Config())
	}
	if r.settingsRuntime != nil {
		bundle.RuntimeServer = r.settingsRuntime.RuntimeServerConfig()
	}
	if r.upstream != nil {
		upstreams, err := r.upstream.List(ctx)
		if err != nil {
			bundle.addError("upstreams", err)
		} else {
			bundle.Upstreams = redactUpstreams(upstreams)
			bundle.ToolSummaries = r.collectToolSummaries(ctx, upstreams)
		}
	}
	if r.security != nil {
		summary, err := r.security.Summary(ctx)
		if err != nil {
			bundle.addError("securitySummary", err)
		} else {
			bundle.SecuritySummary = &summary
		}
		events, err := r.security.ListEvents(ctx, store.SecurityEventQuery{Limit: diagnosticsRecentLimit})
		if err != nil {
			bundle.addError("recentSecurityEvents", err)
		} else {
			bundle.RecentSecurityEvents = events
		}
		blocks, err := r.security.ListBlocks(ctx, store.SecurityBlockQuery{Status: store.SecurityBlockStatusActive, Limit: diagnosticsRecentLimit})
		if err != nil {
			bundle.addError("activeSecurityBlocks", err)
		} else {
			bundle.ActiveSecurityBlocks = blocks
		}
	}
	if r.stats != nil {
		records, err := r.stats.ListRecords(ctx, store.CallRecordQuery{Limit: diagnosticsRecentLimit})
		if err != nil {
			bundle.addError("recentCallRecords", err)
		} else {
			bundle.RecentCallRecords = redactCallRecords(records)
		}
	}
	if r.systemLogs != nil {
		bundle.SystemLogs = redactSystemLogs(r.systemLogs.List(0, "", diagnosticsRecentLimit))
	}
	return bundle
}

func (b *diagnosticsBundle) addError(section string, err error) {
	if err == nil {
		return
	}
	b.CollectionErrors = append(b.CollectionErrors, diagnosticsCollectError{
		Section: section,
		Message: err.Error(),
	})
}

func (r *Router) collectToolSummaries(ctx context.Context, upstreams []domain.Upstream) []upstreamToolSummary {
	if r.toolCache == nil {
		return nil
	}
	summaries := make([]upstreamToolSummary, 0, len(upstreams))
	for _, up := range upstreams {
		tools, updatedAt, found := r.toolCache.Get(ctx, up.ID)
		var updatedAtPtr *time.Time
		if found {
			updatedAtPtr = &updatedAt
		}
		summaries = append(summaries, upstreamToolSummary{
			ID:        up.ID,
			Count:     len(tools),
			UpdatedAt: updatedAtPtr,
		})
	}
	return summaries
}

func redactSettings(cfg config.YAMLConfig) diagnosticsSettings {
	return diagnosticsSettings{
		Server: cfg.Server,
		Admin: diagnosticsAdminConfig{
			Username:        cfg.Admin.Username,
			Initialized:     cfg.Admin.Initialized,
			PasswordHashSet: cfg.Admin.PasswordHash != "",
		},
		Auth:        cfg.Auth,
		Sync:        cfg.Sync,
		Connection:  cfg.Connection,
		Aggregation: cfg.Aggregation,
		MCPAPI:      cfg.MCPAPI,
		Statistics:  cfg.Statistics,
		Audit:       cfg.Audit,
		Security:    cfg.Security,
		XiaoZhi:     cfg.XiaoZhi,
		JWTSecret:   redactNonEmpty(cfg.JWTSecret),
	}
}

func redactUpstreams(upstreams []domain.Upstream) []diagnosticsUpstream {
	out := make([]diagnosticsUpstream, 0, len(upstreams))
	for _, up := range upstreams {
		cfg := up.Config
		if strings.TrimSpace(cfg.Credential) != "" {
			cfg.Credential = redactedValue
		}
		cfg.ConnParams = redactConnParams(cfg.ConnParams)
		out = append(out, diagnosticsUpstream{
			ID:        up.ID,
			Config:    cfg,
			State:     up.State,
			LastError: up.LastError,
			CreatedAt: up.CreatedAt,
			UpdatedAt: up.UpdatedAt,
		})
	}
	return out
}

func redactConnParams(params map[string]any) map[string]any {
	if len(params) == 0 {
		return nil
	}
	return redactAnyMap(params)
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(normalized, "authorization") ||
		strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "api-key") ||
		strings.Contains(normalized, "api_key") ||
		strings.Contains(normalized, "apikey") ||
		strings.Contains(normalized, "credential") ||
		normalized == "authvalue" ||
		normalized == "doccontent"
}

func redactNonEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return redactedValue
}

func redactCallRecords(records []store.CallRecordView) []diagnosticsCallRecord {
	out := make([]diagnosticsCallRecord, 0, len(records))
	for _, rec := range records {
		out = append(out, diagnosticsCallRecord{
			ID:           rec.ID,
			UpstreamID:   rec.UpstreamID,
			UpstreamName: rec.UpstreamName,
			OriginalName: rec.OriginalName,
			ExposedName:  rec.ExposedName,
			APIKeyID:     rec.APIKeyID,
			APIKeyName:   rec.APIKeyName,
			CalledAt:     rec.CalledAt,
			LatencyMS:    rec.LatencyMS,
			Success:      rec.Success,
			Status:       rec.Status,
			ErrorMessage: rec.ErrorMessage,
			Mode:         rec.Mode,
			Source:       rec.Source,
		})
	}
	return out
}

func redactSystemLogs(logs []syslog.Entry) []syslog.Entry {
	out := make([]syslog.Entry, 0, len(logs))
	for _, entry := range logs {
		if len(entry.Attrs) > 0 {
			entry.Attrs = redactAnyMap(entry.Attrs)
		}
		out = append(out, entry)
	}
	return out
}

func redactAnyMap(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		if isSensitiveKey(key) {
			out[key] = redactedValue
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			out[key] = redactAnyMap(typed)
		case map[string]string:
			nested := make(map[string]any, len(typed))
			for k, v := range typed {
				if isSensitiveKey(k) {
					nested[k] = redactedValue
				} else {
					nested[k] = v
				}
			}
			out[key] = nested
		default:
			out[key] = value
		}
	}
	return out
}
