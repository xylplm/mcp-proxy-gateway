package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/scripts"
)

// ScriptService 脚本中心窄接口。
type ScriptService interface {
	List() ([]scripts.Script, error)
	Get(id string) (scripts.Script, error)
	GetDetail(id string) (scripts.ScriptDetail, error)
	Create(in scripts.CreateInput) (scripts.ScriptDetail, error)
	UpdateMeta(id string, in scripts.UpdateMetaInput) (scripts.Script, error)
	SaveContent(id string, in scripts.SaveContentInput) (scripts.ScriptDetail, error)
	Delete(id string) error
	ListVersions(id string) ([]scripts.VersionMeta, error)
	GetVersion(id, version string) (string, scripts.VersionMeta, error)
	ActivateVersion(id, version string) (scripts.Script, error)
	Analyze(content string) (scripts.RiskReport, error)
	DiffVersions(id, leftVer, rightVer string) (scripts.DiffResult, error)
	BuildLaunchBinding(id, version string) (scripts.LaunchBinding, string, []string, string, error)
}

func (r *Router) registerScriptRoutes(g *gin.RouterGroup) {
	s := g.Group("/scripts")
	s.GET("", r.listScripts)
	s.POST("", r.createScript)
	s.POST("/analyze", r.analyzeScript)
	s.GET("/:id", r.getScript)
	s.PATCH("/:id", r.patchScript)
	s.DELETE("/:id", r.deleteScript)
	s.PUT("/:id/content", r.saveScriptContent)
	s.GET("/:id/versions", r.listScriptVersions)
	s.GET("/:id/versions/:version", r.getScriptVersion)
	s.POST("/:id/versions/:version/activate", r.activateScriptVersion)
	s.GET("/:id/diff", r.diffScriptVersions)
	s.POST("/:id/launch", r.scriptLaunchBinding)
}

func (r *Router) listScripts(c *gin.Context) {
	if r.scripts == nil {
		respondServiceUnavailable(c, "脚本服务未就绪")
		return
	}
	list, err := r.scripts.List()
	if err != nil {
		respondError(c, mapScriptErr(err))
		return
	}
	if list == nil {
		list = []scripts.Script{}
	}
	// 可选过滤
	q := strings.TrimSpace(c.Query("q"))
	lang := strings.TrimSpace(c.Query("language"))
	risk := strings.TrimSpace(c.Query("risk"))
	if q != "" || lang != "" || risk != "" {
		filtered := make([]scripts.Script, 0, len(list))
		ql := strings.ToLower(q)
		for _, item := range list {
			if lang != "" && string(item.Language) != lang {
				continue
			}
			if risk != "" && string(item.Risk.Level) != risk {
				continue
			}
			if ql != "" {
				blob := strings.ToLower(item.Name + " " + item.Description + " " + strings.Join(item.Tags, " "))
				if !strings.Contains(blob, ql) {
					continue
				}
			}
			filtered = append(filtered, item)
		}
		list = filtered
	}
	respondOK(c, gin.H{"scripts": list})
}

func (r *Router) createScript(c *gin.Context) {
	if r.scripts == nil {
		respondServiceUnavailable(c, "脚本服务未就绪")
		return
	}
	limitScriptBody(c)
	var in scripts.CreateInput
	if !bindJSON(c, &in) {
		return
	}
	detail, err := r.scripts.Create(in)
	if err != nil {
		respondError(c, mapScriptErr(err))
		return
	}
	r.recordCreate(c, audit.ResourceScript, detail.Name)
	respondCreated(c, detail)
}

func (r *Router) getScript(c *gin.Context) {
	if r.scripts == nil {
		respondServiceUnavailable(c, "脚本服务未就绪")
		return
	}
	detail, err := r.scripts.GetDetail(c.Param("id"))
	if err != nil {
		respondError(c, mapScriptErr(err))
		return
	}
	respondOK(c, detail)
}

func (r *Router) patchScript(c *gin.Context) {
	if r.scripts == nil {
		respondServiceUnavailable(c, "脚本服务未就绪")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var in scripts.UpdateMetaInput
	if !bindJSON(c, &in) {
		return
	}
	sc, err := r.scripts.UpdateMeta(c.Param("id"), in)
	if err != nil {
		respondError(c, mapScriptErr(err))
		return
	}
	r.recordUpdate(c, audit.ResourceScript, sc.Name)
	respondOK(c, sc)
}

func (r *Router) deleteScript(c *gin.Context) {
	if r.scripts == nil {
		respondServiceUnavailable(c, "脚本服务未就绪")
		return
	}
	id := c.Param("id")
	r.scriptRefMu.Lock()
	defer r.scriptRefMu.Unlock()
	if r.upstream != nil {
		ups, err := r.upstream.List(c.Request.Context())
		if err != nil {
			respondError(c, err)
			return
		}
		refs := make([]string, 0)
		for _, up := range ups {
			if upstreamUsesScript(up, id) {
				refs = append(refs, up.Config.Name)
			}
		}
		if len(refs) > 0 {
			respondError(c, domain.NewError(
				domain.CodeConflict,
				"脚本仍被上游引用，请先修改或删除相关上游："+strings.Join(refs, "、"),
			))
			return
		}
	}
	if err := r.scripts.Delete(id); err != nil {
		respondError(c, mapScriptErr(err))
		return
	}
	r.recordDelete(c, audit.ResourceScript, id)
	respondNoContent(c)
}

func (r *Router) saveScriptContent(c *gin.Context) {
	if r.scripts == nil {
		respondServiceUnavailable(c, "脚本服务未就绪")
		return
	}
	limitScriptBody(c)
	var in scripts.SaveContentInput
	if !bindJSON(c, &in) {
		return
	}
	detail, err := r.scripts.SaveContent(c.Param("id"), in)
	if err != nil {
		respondError(c, mapScriptErr(err))
		return
	}
	r.recordUpdate(c, audit.ResourceScript, detail.Name+":"+detail.CurrentVersion)
	respondOK(c, detail)
}

func (r *Router) listScriptVersions(c *gin.Context) {
	if r.scripts == nil {
		respondServiceUnavailable(c, "脚本服务未就绪")
		return
	}
	list, err := r.scripts.ListVersions(c.Param("id"))
	if err != nil {
		respondError(c, mapScriptErr(err))
		return
	}
	if list == nil {
		list = []scripts.VersionMeta{}
	}
	respondOK(c, gin.H{"versions": list})
}

func (r *Router) getScriptVersion(c *gin.Context) {
	if r.scripts == nil {
		respondServiceUnavailable(c, "脚本服务未就绪")
		return
	}
	content, meta, err := r.scripts.GetVersion(c.Param("id"), c.Param("version"))
	if err != nil {
		respondError(c, mapScriptErr(err))
		return
	}
	respondOK(c, gin.H{"meta": meta, "content": content})
}

func (r *Router) activateScriptVersion(c *gin.Context) {
	if r.scripts == nil {
		respondServiceUnavailable(c, "脚本服务未就绪")
		return
	}
	sc, err := r.scripts.ActivateVersion(c.Param("id"), c.Param("version"))
	if err != nil {
		respondError(c, mapScriptErr(err))
		return
	}
	r.recordUpdate(c, audit.ResourceScript, sc.Name+":activate:"+sc.CurrentVersion)
	respondOK(c, sc)
}

func (r *Router) diffScriptVersions(c *gin.Context) {
	if r.scripts == nil {
		respondServiceUnavailable(c, "脚本服务未就绪")
		return
	}
	left := strings.TrimSpace(c.Query("left"))
	right := strings.TrimSpace(c.Query("right"))
	if left == "" || right == "" {
		respondError(c, domain.NewValidationError("left/right 版本号必填", map[string]string{
			"left":  "必填",
			"right": "必填",
		}))
		return
	}
	diff, err := r.scripts.DiffVersions(c.Param("id"), left, right)
	if err != nil {
		respondError(c, mapScriptErr(err))
		return
	}
	respondOK(c, diff)
}

func (r *Router) analyzeScript(c *gin.Context) {
	if r.scripts == nil {
		respondServiceUnavailable(c, "脚本服务未就绪")
		return
	}
	limitScriptBody(c)
	var body struct {
		Content string `json:"content"`
	}
	if !bindJSON(c, &body) {
		return
	}
	report, err := r.scripts.Analyze(body.Content)
	if err != nil {
		respondError(c, mapScriptErr(err))
		return
	}
	respondOK(c, report)
}

func (r *Router) scriptLaunchBinding(c *gin.Context) {
	if r.scripts == nil {
		respondServiceUnavailable(c, "脚本服务未就绪")
		return
	}
	var body struct {
		Version string `json:"version"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8*1024)
	raw, readErr := io.ReadAll(c.Request.Body)
	if readErr != nil {
		respondError(c, domain.NewValidationError("请求体格式非法", map[string]string{"body": "请求体超过 8 KiB 或无法读取"}))
		return
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		if err := dec.Decode(&body); err != nil {
			respondError(c, domain.NewValidationError("请求体格式非法", map[string]string{"body": "仅支持空请求体或包含 version 的 JSON 对象"}))
			return
		}
		var extra any
		if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
			respondError(c, domain.NewValidationError("请求体格式非法", map[string]string{"body": "请求体只能包含一个 JSON 对象"}))
			return
		}
	}
	bind, cmd, args, cwd, err := r.scripts.BuildLaunchBinding(c.Param("id"), body.Version)
	if err != nil {
		respondError(c, mapScriptErr(err))
		return
	}
	respondOK(c, gin.H{
		"scriptRef":  bind,
		"command":    cmd,
		"args":       args,
		"cwd":        cwd,
		"launchMode": "script",
	})
}

func upstreamUsesScript(up domain.Upstream, scriptID string) bool {
	if up.Config.Transport != domain.TransportStdio || up.Config.ConnParams == nil {
		return false
	}
	mode, _ := up.Config.ConnParams["launchMode"].(string)
	if mode != "script" {
		return false
	}
	switch ref := up.Config.ConnParams["scriptRef"].(type) {
	case map[string]any:
		id, _ := ref["scriptId"].(string)
		return id == scriptID
	case scripts.LaunchBinding:
		return ref.ScriptID == scriptID
	default:
		return false
	}
}

func limitScriptBody(c *gin.Context) {
	// 内容上限 1 MiB，额外留出 JSON/元数据开销。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, scripts.MaxScriptBytes+128*1024)
}

func mapScriptErr(err error) error {
	if err == nil {
		return domain.NewError(domain.CodeInternal, "脚本操作失败")
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "不存在"):
		return domain.NewError(domain.CodeNotFound, msg)
	case strings.Contains(msg, "未就绪"):
		return domain.NewError(domain.CodeInternal, msg)
	default:
		return domain.NewValidationError(msg, map[string]string{"script": msg})
	}
}
