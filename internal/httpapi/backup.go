package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/backup"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type backupContentRequest struct {
	Content string `json:"content"`
}

type backupPreviewResponse struct {
	Version               string `json:"version"`
	UpstreamCount         int    `json:"upstreamCount"`
	AliasRuleCount        int    `json:"aliasRuleCount"`
	MCPFilterRuleCount    int    `json:"mcpFilterRuleCount"`
	APIKeyCount           int    `json:"apiKeyCount"`
	APIKeyFilterRuleCount int    `json:"apiKeyFilterRuleCount"`
	ACLCount              int    `json:"aclCount"`
	ContainsSecrets       bool   `json:"containsSecrets"`
}

type backupImportResponse struct {
	Imported         bool                  `json:"imported"`
	RestartRequested bool                  `json:"restartRequested"`
	Preview          backupPreviewResponse `json:"preview"`
}

// registerBackupRoutes 注册配置备份导入导出端点。
func (r *Router) registerBackupRoutes(g *gin.RouterGroup) {
	g.GET("/backup/export", r.exportBackup)
	g.POST("/backup/preview", r.previewBackup)
	g.POST("/backup/import", r.importBackup)
}

func (r *Router) exportBackup(c *gin.Context) {
	if r.backup == nil {
		respondServiceUnavailable(c, "配置备份服务未就绪")
		return
	}

	data, err := r.backup.Export(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}

	filename := "mpg-backup-" + time.Now().Format("20060102-150405") + ".json"
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "application/json; charset=utf-8", data)
}

func (r *Router) previewBackup(c *gin.Context) {
	var req backupContentRequest
	if !bindJSON(c, &req) {
		return
	}

	b, err := parseBackupContent(req.Content)
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, previewBackup(b))
}

func (r *Router) importBackup(c *gin.Context) {
	if r.backup == nil {
		respondServiceUnavailable(c, "配置备份服务未就绪")
		return
	}

	var req backupContentRequest
	if !bindJSON(c, &req) {
		return
	}

	b, err := parseBackupContent(req.Content)
	if err != nil {
		respondError(c, err)
		return
	}
	if err := r.backup.Import(c.Request.Context(), []byte(req.Content)); err != nil {
		respondError(c, err)
		return
	}

	restartRequested := false
	if r.settingsRuntime != nil {
		r.settingsRuntime.RequestRestart()
		restartRequested = true
	}

	r.recordUpdate(c, audit.ResourceSetting, "backup-import")
	respondOK(c, backupImportResponse{
		Imported:         true,
		RestartRequested: restartRequested,
		Preview:          previewBackup(b),
	})
}

func parseBackupContent(content string) (backup.Backup, error) {
	if strings.TrimSpace(content) == "" {
		return backup.Backup{}, domain.NewValidationError("备份文件内容不能为空", map[string]string{
			"content": "请选择有效的备份文件",
		})
	}
	return backup.ParseAndValidate([]byte(content))
}

func previewBackup(b backup.Backup) backupPreviewResponse {
	resp := backupPreviewResponse{
		Version:            b.Version,
		UpstreamCount:      len(b.Business.Upstreams),
		AliasRuleCount:     len(b.Business.AliasRules),
		MCPFilterRuleCount: len(b.Business.MCPFilterRules),
		APIKeyCount:        len(b.Business.APIKeys),
		ContainsSecrets:    containsYAMLSecrets(b),
	}

	for _, entry := range b.Business.Upstreams {
		if strings.TrimSpace(entry.Config.Credential) != "" {
			resp.ContainsSecrets = true
		}
	}
	for _, entry := range b.Business.APIKeys {
		resp.APIKeyFilterRuleCount += len(entry.FilterRules)
		resp.ACLCount += len(entry.ACLCIDRs)
		if strings.TrimSpace(entry.Meta.KeyPlain) != "" || len(entry.Meta.KeyHash) > 0 {
			resp.ContainsSecrets = true
		}
	}
	return resp
}

func containsYAMLSecrets(b backup.Backup) bool {
	return strings.TrimSpace(b.YAML.JWTSecret) != "" ||
		strings.TrimSpace(b.YAML.Admin.Username) != "" ||
		strings.TrimSpace(b.YAML.Admin.PasswordHash) != ""
}
