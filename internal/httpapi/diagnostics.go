package httpapi

import (
	"context"
	"runtime"
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
	Settings             diagnosticsSettings       `json:"settings,omitempty"`
	RuntimeServer        config.ServerConfig       `json:"runtimeServer,omitempty"`
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

func (r *Router) registerDiagnosticsRoutes(g *gin.RouterGroup) {
	g.GET("/diagnostics/export", r.exportDiagnostics)
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
