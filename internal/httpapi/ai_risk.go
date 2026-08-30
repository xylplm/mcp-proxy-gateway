package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/risk"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

type manualOverrideRequest struct {
	Level  risk.Level `json:"level"`
	Tags   []string   `json:"tags"`
	Reason string     `json:"reason"`
	Force  bool       `json:"force"`
}

type bulkOverrideRequest struct {
	Items []struct {
		UpstreamID   string `json:"upstreamId"`
		OriginalName string `json:"originalName"`
	} `json:"items"`
	Level  risk.Level `json:"level"`
	Tags   []string   `json:"tags"`
	Reason string     `json:"reason"`
	Force  bool       `json:"force"`
}

func (r *Router) registerAIRiskRoutes(g *gin.RouterGroup) {
	group := g.Group("/ai-risk")
	group.GET("/providers", r.listAIProviders)
	group.POST("/providers", r.createAIProvider)
	group.GET("/providers/:id", r.getAIProvider)
	group.PUT("/providers/:id", r.updateAIProvider)
	group.DELETE("/providers/:id", r.deleteAIProvider)
	group.POST("/providers/:id/activate", r.activateAIProvider)
	group.POST("/providers/:id/test", r.testAIProvider)
	group.GET("/tools", r.listRiskTools)
	group.POST("/tools/bulk-override", r.bulkRiskOverride)
	group.GET("/tools/:upstreamId/:originalName", r.getRiskTool)
	group.POST("/tools/:upstreamId/:originalName/reassess", r.reassessRiskTool)
	group.PUT("/tools/:upstreamId/:originalName/manual-override", r.setRiskOverride)
	group.DELETE("/tools/:upstreamId/:originalName/manual-override", r.clearRiskOverride)
	group.POST("/reconcile", r.reconcileRiskCatalog)
	group.POST("/assess", r.assessRiskCatalog)
	group.POST("/assess/review", r.assessNeedsReviewCatalog)
	group.GET("/jobs", r.listRiskJobs)
	group.GET("/jobs/:id", r.getRiskJob)
	group.POST("/jobs/:id/cancel", r.cancelRiskJob)
}

func (r *Router) requireAIRisk(c *gin.Context) bool {
	if r.aiRisk == nil || r.toolRiskStore == nil {
		respondServiceUnavailable(c, "AI 风险治理服务未就绪")
		return false
	}
	return true
}

func (r *Router) listAIProviders(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	items, err := r.aiRisk.ListProviders(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"providers": items, "encryptionReady": r.aiRisk.ProviderEncryptionReady()})
}
func (r *Router) getAIProvider(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	item, err := r.aiRisk.GetProvider(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, item)
}
func (r *Router) createAIProvider(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	var req risk.ProviderInput
	if !bindJSON(c, &req) {
		return
	}
	item, err := r.aiRisk.CreateProvider(c.Request.Context(), req)
	if err != nil {
		respondError(c, err)
		return
	}
	r.recordCreate(c, audit.ResourceSetting, item.ID)
	respondCreated(c, item)
}
func (r *Router) updateAIProvider(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	var req risk.ProviderInput
	if !bindJSON(c, &req) {
		return
	}
	item, err := r.aiRisk.UpdateProvider(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		respondError(c, err)
		return
	}
	r.recordUpdate(c, audit.ResourceSetting, item.ID)
	respondOK(c, item)
}
func (r *Router) deleteAIProvider(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	if err := r.aiRisk.DeleteProvider(c.Request.Context(), c.Param("id")); err != nil {
		respondError(c, err)
		return
	}
	r.recordDelete(c, audit.ResourceSetting, c.Param("id"))
	respondNoContent(c)
}
func (r *Router) activateAIProvider(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	if err := r.aiRisk.ActivateProvider(c.Request.Context(), c.Param("id")); err != nil {
		respondError(c, err)
		return
	}
	r.recordUpdate(c, audit.ResourceSetting, c.Param("id"))
	respondOK(c, gin.H{"id": c.Param("id"), "active": true})
}
func (r *Router) testAIProvider(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	latency, err := r.aiRisk.TestProvider(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"ok": true, "latencyMs": latency})
}

func (r *Router) listRiskTools(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("pageSize", "50"))
	var minConfidence *float64
	if raw := c.Query("minConfidence"); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || value < 0 || value > 1 {
			respondError(c, domain.NewError(domain.CodeValidation, "minConfidence 必须在 0 到 1 之间"))
			return
		}
		minConfidence = &value
	}
	result, err := r.toolRiskStore.List(c.Request.Context(), store.RiskListQuery{
		UpstreamID: c.Query("upstreamId"), Status: risk.Status(c.Query("status")), Keyword: c.Query("keyword"),
		Level: risk.Level(c.Query("level")), ManualOnly: c.Query("manualOnly") == "true", MinConfidence: minConfidence, Page: page, PageSize: size,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	r.attachRiskUpstreamNames(c.Request.Context(), result.Items)
	respondOK(c, result)
}

func (r *Router) attachRiskUpstreamNames(ctx context.Context, items []risk.Assessment) {
	if r.upstream == nil || len(items) == 0 {
		return
	}
	upstreams, err := r.upstream.List(ctx)
	if err != nil {
		return
	}
	names := make(map[string]string, len(upstreams))
	for _, upstream := range upstreams {
		names[upstream.ID] = upstream.Config.Name
	}
	for i := range items {
		items[i].UpstreamName = names[items[i].UpstreamID]
	}
}

func (r *Router) getRiskTool(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	item, err := r.toolRiskStore.Get(c.Request.Context(), c.Param("upstreamId"), c.Param("originalName"))
	if err != nil {
		respondError(c, err)
		return
	}
	items := []risk.Assessment{item}
	r.attachRiskUpstreamNames(c.Request.Context(), items)
	item = items[0]
	respondOK(c, item)
}
func (r *Router) reassessRiskTool(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	item, err := r.aiRisk.ReassessTool(c.Request.Context(), c.Param("upstreamId"), c.Param("originalName"))
	if err != nil {
		respondError(c, err)
		return
	}
	items := []risk.Assessment{item}
	r.attachRiskUpstreamNames(c.Request.Context(), items)
	item = items[0]
	r.recordUpdate(c, audit.ResourceSetting, item.ID)
	respondOK(c, item)
}
func (r *Router) bulkRiskOverride(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	var req bulkOverrideRequest
	if !bindJSON(c, &req) {
		return
	}
	if len(req.Items) == 0 || len(req.Items) > 500 {
		respondError(c, domain.NewError(domain.CodeValidation, "批量覆写需要包含 1 到 500 个工具"))
		return
	}
	targets := make([]store.RiskOverrideTarget, 0, len(req.Items))
	for _, target := range req.Items {
		targets = append(targets, store.RiskOverrideTarget{UpstreamID: target.UpstreamID, OriginalName: target.OriginalName})
	}
	updated, err := r.toolRiskStore.BulkSetManualOverride(c.Request.Context(), targets, req.Level, req.Tags, req.Reason, req.Force)
	if err != nil {
		respondError(c, err)
		return
	}
	r.invalidateToolSetCache()
	r.recordUpdate(c, audit.ResourceSetting, "ai-risk-bulk-override")
	respondOK(c, gin.H{"items": updated, "updated": len(updated)})
}
func (r *Router) setRiskOverride(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	var req manualOverrideRequest
	if !bindJSON(c, &req) {
		return
	}
	item, err := r.toolRiskStore.SetManualOverride(c.Request.Context(), c.Param("upstreamId"), c.Param("originalName"), req.Level, req.Tags, req.Reason, req.Force)
	if err != nil {
		respondError(c, err)
		return
	}
	r.invalidateToolSetCache()
	r.recordUpdate(c, audit.ResourceSetting, item.ID)
	respondOK(c, item)
}
func (r *Router) clearRiskOverride(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	item, err := r.toolRiskStore.ClearManualOverride(c.Request.Context(), c.Param("upstreamId"), c.Param("originalName"))
	if err != nil {
		respondError(c, err)
		return
	}
	r.invalidateToolSetCache()
	r.recordUpdate(c, audit.ResourceSetting, item.ID)
	respondOK(c, item)
}
func (r *Router) reconcileRiskCatalog(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	// 同步遍历所有上游逐个 Reconcile 属于低频管理操作，加 30s 超时避免长时间阻塞。
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	upstreams, err := r.upstream.List(ctx)
	if err != nil {
		respondError(c, err)
		return
	}
	total := store.ReconcileResult{}
	for _, upstream := range upstreams {
		tools, _, _ := r.toolCache.Get(ctx, upstream.ID)
		result, reconcileErr := r.toolRiskStore.Reconcile(ctx, upstream.ID, tools)
		if reconcileErr != nil {
			respondError(c, reconcileErr)
			return
		}
		total.Added += result.Added
		total.Changed += result.Changed
		total.Removed += result.Removed
		total.Current += result.Current
	}
	r.invalidateToolSetCache()
	r.recordUpdate(c, audit.ResourceSetting, "ai-risk-reconcile")
	respondOK(c, total)
}
func (r *Router) assessRiskCatalog(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "500"))
	result, err := r.aiRisk.QueueAssessment(c.Request.Context(), limit)
	if err != nil {
		respondError(c, err)
		return
	}
	r.recordCreate(c, audit.ResourceSetting, "ai-risk-assessment")
	c.Status(http.StatusAccepted)
	respondOK(c, result)
}

func (r *Router) assessNeedsReviewCatalog(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "500"))
	result, err := r.aiRisk.QueueReviewAssessment(c.Request.Context(), limit)
	if err != nil {
		respondError(c, err)
		return
	}
	r.recordCreate(c, audit.ResourceSetting, "ai-risk-review-reassessment")
	c.Status(http.StatusAccepted)
	respondOK(c, result)
}

func (r *Router) listRiskJobs(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := r.aiRisk.ListJobs(c.Request.Context(), limit)
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"jobs": items})
}
func (r *Router) getRiskJob(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	item, err := r.aiRisk.GetJob(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, item)
}
func (r *Router) cancelRiskJob(c *gin.Context) {
	if !r.requireAIRisk(c) {
		return
	}
	if err := r.aiRisk.CancelJob(c.Request.Context(), c.Param("id")); err != nil {
		respondError(c, err)
		return
	}
	r.recordUpdate(c, audit.ResourceSetting, c.Param("id"))
	respondOK(c, gin.H{"id": c.Param("id"), "status": risk.JobCancelled})
}
