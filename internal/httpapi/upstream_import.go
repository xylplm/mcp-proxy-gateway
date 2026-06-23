package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

const maxUpstreamImportItems = 100

type upstreamImportRequest struct {
	Content string                  `json:"content"`
	Items   []upstreamConfigRequest `json:"items"`
}

type upstreamImportItem struct {
	Index  int                   `json:"index"`
	Config domain.UpstreamConfig `json:"config"`
}

type upstreamImportPreview struct {
	Items []upstreamImportItem `json:"items"`
	Count int                  `json:"count"`
}

type upstreamImportResultItem struct {
	Index    int               `json:"index"`
	Name     string            `json:"name"`
	Upstream *domain.Upstream  `json:"upstream,omitempty"`
	Error    string            `json:"error,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
}

type upstreamImportResult struct {
	Created []upstreamImportResultItem `json:"created"`
	Failed  []upstreamImportResultItem `json:"failed"`
}

type mcpConfigFile struct {
	MCPServers map[string]mcpServerConfig `json:"mcpServers"`
	Servers    map[string]mcpServerConfig `json:"servers"`
}

type mcpServerConfig struct {
	Name       string                    `json:"name"`
	Type       string                    `json:"type"`
	Transport  string                    `json:"transport"`
	Command    string                    `json:"command"`
	Args       []string                  `json:"args"`
	Env        map[string]string         `json:"env"`
	CWD        string                    `json:"cwd"`
	URL        string                    `json:"url"`
	Headers    map[string]string         `json:"headers"`
	Credential string                    `json:"credential"`
	Enabled    *bool                     `json:"enabled"`
	AutoSync   *bool                     `json:"autoSync"`
	Tags       []string                  `json:"tags"`
	RateLimits domain.UpstreamRateLimits `json:"rateLimits"`
	SortOrder  int                       `json:"sortOrder"`
}

type mcpExportFile struct {
	MCPServers map[string]mcpExportServer `json:"mcpServers"`
}

type mcpExportServer struct {
	Type       string            `json:"type,omitempty"`
	Command    string            `json:"command,omitempty"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	CWD        string            `json:"cwd,omitempty"`
	URL        string            `json:"url,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Credential string            `json:"credential,omitempty"`
	Enabled    bool              `json:"enabled"`
	AutoSync   bool              `json:"autoSync"`
	Tags       []string          `json:"tags,omitempty"`
}

func (r *Router) previewUpstreamImport(c *gin.Context) {
	items, ok := r.parseUpstreamImport(c)
	if !ok {
		return
	}
	respondOK(c, upstreamImportPreview{Items: items, Count: len(items)})
}

func (r *Router) importUpstreams(c *gin.Context) {
	if r.upstream == nil {
		respondServiceUnavailable(c, "上游管理服务未就绪")
		return
	}
	items, ok := r.parseUpstreamImport(c)
	if !ok {
		return
	}

	result := upstreamImportResult{
		Created: make([]upstreamImportResultItem, 0, len(items)),
		Failed:  make([]upstreamImportResultItem, 0),
	}
	for _, item := range items {
		cfg := item.Config
		up, err := r.upstream.Create(c.Request.Context(), cfg)
		if err != nil {
			failed := upstreamImportResultItem{
				Index:  item.Index,
				Name:   cfg.Name,
				Error:  apiErrorMessage(err),
				Fields: apiErrorFields(err),
			}
			result.Failed = append(result.Failed, failed)
			continue
		}
		created := up
		result.Created = append(result.Created, upstreamImportResultItem{
			Index:    item.Index,
			Name:     cfg.Name,
			Upstream: &created,
		})
		r.recordCreate(c, audit.ResourceUpstream, cfg.Name)
	}
	respondOK(c, result)
}

func (r *Router) exportUpstreamsMCPJSON(c *gin.Context) {
	if r.upstream == nil {
		respondServiceUnavailable(c, "上游管理服务未就绪")
		return
	}
	upstreams, err := r.upstream.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	filename := "mpg-mcp-servers-" + time.Now().Format("20060102-150405") + ".json"
	respondJSONDownload(c, filename, buildMCPExport(upstreams))
}

func (r *Router) parseUpstreamImport(c *gin.Context) ([]upstreamImportItem, bool) {
	var req upstreamImportRequest
	if !bindJSON(c, &req) {
		return nil, false
	}
	items, err := buildUpstreamImportItems(req)
	if err != nil {
		respondError(c, err)
		return nil, false
	}
	return items, true
}

func buildUpstreamImportItems(req upstreamImportRequest) ([]upstreamImportItem, error) {
	var configs []domain.UpstreamConfig
	if len(req.Items) > 0 {
		configs = make([]domain.UpstreamConfig, 0, len(req.Items))
		for _, item := range req.Items {
			configs = append(configs, item.toConfig())
		}
	} else {
		content := strings.TrimSpace(req.Content)
		if content == "" {
			return nil, domain.NewValidationError("导入内容不能为空", map[string]string{
				"content": "请粘贴 MCP JSON 配置，或提交 items 数组",
			})
		}
		parsed, err := parseMCPJSONConfig([]byte(content))
		if err != nil {
			return nil, err
		}
		configs = parsed
	}

	if len(configs) == 0 {
		return nil, domain.NewValidationError("未找到可导入的上游 MCP", map[string]string{
			"content": "请提供 mcpServers 配置或上游数组",
		})
	}
	if len(configs) > maxUpstreamImportItems {
		return nil, domain.NewValidationError("单次导入数量过多", map[string]string{
			"content": fmt.Sprintf("一次最多导入 %d 个上游 MCP", maxUpstreamImportItems),
		})
	}

	items := make([]upstreamImportItem, 0, len(configs))
	for i, cfg := range configs {
		items = append(items, upstreamImportItem{Index: i, Config: normalizeImportConfig(cfg)})
	}
	return items, nil
}

func parseMCPJSONConfig(data []byte) ([]domain.UpstreamConfig, error) {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, domain.NewValidationError("MCP JSON 格式非法", map[string]string{
			"content": err.Error(),
		})
	}

	if _, ok := root.(map[string]any); ok {
		var file mcpConfigFile
		if err := json.Unmarshal(data, &file); err != nil {
			return nil, domain.NewValidationError("MCP JSON 格式非法", map[string]string{
				"content": err.Error(),
			})
		}
		servers := file.MCPServers
		if len(servers) == 0 {
			servers = file.Servers
		}
		if len(servers) > 0 {
			names := make([]string, 0, len(servers))
			for name := range servers {
				names = append(names, name)
			}
			sort.Strings(names)

			configs := make([]domain.UpstreamConfig, 0, len(names))
			for _, name := range names {
				configs = append(configs, mcpServerToUpstreamConfig(name, servers[name]))
			}
			return configs, nil
		}

		var wrapped struct {
			Upstreams []upstreamConfigRequest `json:"upstreams"`
			Items     []upstreamConfigRequest `json:"items"`
		}
		if err := json.Unmarshal(data, &wrapped); err == nil {
			items := wrapped.Upstreams
			if len(items) == 0 {
				items = wrapped.Items
			}
			if len(items) > 0 {
				configs := make([]domain.UpstreamConfig, 0, len(items))
				for _, item := range items {
					configs = append(configs, item.toConfig())
				}
				return configs, nil
			}
		}
	}

	var direct []upstreamConfigRequest
	if err := json.Unmarshal(data, &direct); err == nil && len(direct) > 0 {
		configs := make([]domain.UpstreamConfig, 0, len(direct))
		for _, item := range direct {
			configs = append(configs, item.toConfig())
		}
		return configs, nil
	}

	return nil, domain.NewValidationError("未找到可导入的上游 MCP", map[string]string{
		"content": "支持 {\"mcpServers\": {...}}、{\"upstreams\": [...]} 或上游数组",
	})
}

func mcpServerToUpstreamConfig(name string, server mcpServerConfig) domain.UpstreamConfig {
	cfg := domain.UpstreamConfig{
		Name:       firstNonEmpty(server.Name, name),
		Tags:       server.Tags,
		Transport:  inferImportTransport(server),
		ConnParams: make(map[string]any),
		Credential: server.Credential,
		Enabled:    true,
		AutoSync:   true,
		SortOrder:  server.SortOrder,
		RateLimits: server.RateLimits,
	}
	if server.Enabled != nil {
		cfg.Enabled = *server.Enabled
	}
	if server.AutoSync != nil {
		cfg.AutoSync = *server.AutoSync
	}

	if cfg.Transport == domain.TransportStdio {
		if server.Command != "" {
			cfg.ConnParams["command"] = server.Command
		}
		if len(server.Args) > 0 {
			cfg.ConnParams["args"] = server.Args
		}
		if len(server.Env) > 0 {
			cfg.ConnParams["env"] = server.Env
		}
		if server.CWD != "" {
			cfg.ConnParams["cwd"] = server.CWD
		}
		return cfg
	}

	if server.URL != "" {
		cfg.ConnParams["url"] = server.URL
	}
	if len(server.Headers) > 0 {
		cfg.ConnParams["headers"] = server.Headers
	}
	return cfg
}

func buildMCPExport(upstreams []domain.Upstream) mcpExportFile {
	out := mcpExportFile{MCPServers: make(map[string]mcpExportServer, len(upstreams))}
	seen := make(map[string]int, len(upstreams))
	for _, up := range upstreams {
		name := strings.TrimSpace(up.Config.Name)
		if name == "" {
			name = up.ID
		}
		name = uniqueMCPExportName(name, seen)
		out.MCPServers[name] = upstreamToMCPExportServer(up.Config)
	}
	return out
}

func uniqueMCPExportName(name string, seen map[string]int) string {
	if seen == nil {
		return name
	}
	count := seen[name]
	seen[name] = count + 1
	if count == 0 {
		return name
	}
	return fmt.Sprintf("%s-%d", name, count+1)
}

func upstreamToMCPExportServer(cfg domain.UpstreamConfig) mcpExportServer {
	server := mcpExportServer{
		Type:       string(cfg.Transport),
		Credential: cfg.Credential,
		Enabled:    cfg.Enabled,
		AutoSync:   cfg.AutoSync,
		Tags:       cfg.Tags,
	}
	if cfg.Transport == domain.TransportStdio {
		server.Command, _ = cfg.ConnParams["command"].(string)
		server.Args = stringSliceFromAny(cfg.ConnParams["args"])
		server.Env = stringMapFromAny(cfg.ConnParams["env"])
		server.CWD, _ = cfg.ConnParams["cwd"].(string)
		return server
	}
	server.URL, _ = cfg.ConnParams["url"].(string)
	server.Headers = stringMapFromAny(cfg.ConnParams["headers"])
	return server
}

func inferImportTransport(server mcpServerConfig) domain.TransportType {
	raw := strings.ToLower(strings.TrimSpace(firstNonEmpty(server.Transport, server.Type)))
	switch raw {
	case string(domain.TransportStdio), "":
		if server.URL != "" {
			return transportFromURL(server.URL)
		}
		return domain.TransportStdio
	case "http", "streamable_http", "streamable-http":
		return domain.TransportStreamableHTTP
	case string(domain.TransportSSE):
		return domain.TransportSSE
	case "ws", "websocket":
		return domain.TransportWebSocket
	default:
		return domain.TransportType(raw)
	}
}

func transportFromURL(rawURL string) domain.TransportType {
	lower := strings.ToLower(strings.TrimSpace(rawURL))
	switch {
	case strings.HasPrefix(lower, "ws://"), strings.HasPrefix(lower, "wss://"):
		return domain.TransportWebSocket
	case strings.Contains(lower, "/sse"):
		return domain.TransportSSE
	default:
		return domain.TransportStreamableHTTP
	}
}

func stringSliceFromAny(raw any) []string {
	switch v := raw.(type) {
	case []string:
		out := make([]string, len(v))
		copy(out, v)
		return out
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil
			}
			out = append(out, s)
		}
		return out
	default:
		return nil
	}
}

func stringMapFromAny(raw any) map[string]string {
	switch v := raw.(type) {
	case map[string]string:
		out := make(map[string]string, len(v))
		for k, val := range v {
			out[k] = val
		}
		return out
	case map[string]any:
		out := make(map[string]string, len(v))
		for k, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil
			}
			out[k] = s
		}
		return out
	default:
		return nil
	}
}

func normalizeImportConfig(cfg domain.UpstreamConfig) domain.UpstreamConfig {
	cfg.Name = strings.TrimSpace(cfg.Name)
	if cfg.ConnParams == nil {
		cfg.ConnParams = map[string]any{}
	}
	return cfg
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func apiErrorFields(err error) map[string]string {
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) && len(apiErr.Fields) > 0 {
		return apiErr.Fields
	}
	return nil
}

func apiErrorMessage(err error) string {
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Message
	}
	return err.Error()
}
