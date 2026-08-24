package httpapi

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/apikey"
	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/risk"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 本文件实现 API Key 管理端点（Req 12.1、13.1、13.9、21）：
//
// 明文存储说明：自部署场景下 API Key 的明文密钥会持久化（api_key.key_plain 列），
// List/Get 接口均返回明文，便于管理台二次查看与复制；鉴权仍走 key_hash 等值查询，
// 明文不参与鉴权。DB 被拖库即等价明文泄露，部署时需妥善保护数据库访问权限。
//
// API Key 生命周期（Req 12.1）：
//   GET    /api/admin/apikeys              列出全部 API Key 元数据（含明文）
//   POST   /api/admin/apikeys              创建 API Key（生成明文，后续可经 List/Get 二次查看）
//   GET    /api/admin/apikeys/:id          查询单个 API Key 元数据（含明文）
//   POST   /api/admin/apikeys/:id/enable   启用 API Key
//   POST   /api/admin/apikeys/:id/disable  停用 API Key
//   DELETE /api/admin/apikeys/:id          删除 API Key（级联清理规则与 ACL）
//
// API Key 级屏蔽规则（Req 13.1）：
//   GET    /api/admin/apikeys/:id/filters            列出某 API Key 的屏蔽规则
//   POST   /api/admin/apikeys/:id/filters            创建屏蔽规则
//   POST   /api/admin/apikey-filters/:ruleId/enable  启用屏蔽规则
//   POST   /api/admin/apikey-filters/:ruleId/disable 停用屏蔽规则
//   DELETE /api/admin/apikey-filters/:ruleId         删除屏蔽规则
//
// API Key 来源白名单 ACL（Req 13.9）：
//   GET    /api/admin/apikeys/:id/acl   列出某 API Key 的来源白名单
//   POST   /api/admin/apikeys/:id/acl   新增一条来源白名单
//   DELETE /api/admin/acl/:entryId      删除一条来源白名单
//
// API Key 限流配置（Req 21）：
//   GET    /api/admin/apikeys/:id/ratelimit  读取限流配置
//   PUT    /api/admin/apikeys/:id/ratelimit  更新限流配置
//
// 屏蔽规则与 ACL 使用独立路径前缀（apikey-filters、acl）按规则/记录标识操作，
// 避免与 /apikeys/:id 的通配段在 gin 路由树上冲突。

// apiKeyCreateRequest 为创建 API Key 的请求体（Req 12.1）。
type apiKeyCreateRequest struct {
	// Name 为名称，长度需在 1 至 100 个字符之间。
	Name string `json:"name"`
	// ExpiresAt 为可选有效期；为空表示永不过期（Req 12.6）。
	ExpiresAt *time.Time `json:"expiresAt"`
	// RiskProfile 缺省为 standard。
	RiskProfile risk.Profile `json:"riskProfile"`
}

type apiKeyRiskProfileRequest struct {
	RiskProfile risk.Profile `json:"riskProfile"`
}

// apiKeyFilterRequest 为创建 API Key 级屏蔽规则的请求体（Req 13.1）。
type apiKeyFilterRequest struct {
	// Pattern 为匹配模式，长度需在 1 至 200 个字符之间。
	Pattern string `json:"pattern"`
	// IsRegex 表示是否启用正则匹配（完整匹配）。
	IsRegex bool `json:"isRegex"`
	// Enabled 表示创建后该规则是否处于启用状态。
	Enabled bool `json:"enabled"`
}

// aclEntryRequest 为新增 API Key 来源白名单的请求体（Req 13.9）。
type aclEntryRequest struct {
	// CIDR 为允许来源的 IP 或网段（如 "10.0.0.0/8" 或 "1.2.3.4"）。
	CIDR string `json:"cidr"`
}

// rateLimitConfigRequest 为更新 API Key 限流配置的请求体（Req 21）。
//
// 两字段均为指针：置空表示清除该项配置（即不限流）；二者须同时为正才生效（Req 21.4）。
type rateLimitConfigRequest struct {
	// RateLimit 为窗口内的请求上限；nil 表示不限流。
	RateLimit *int `json:"rateLimit"`
	// RateWindowS 为限流计数窗口秒数；nil 表示未配置。
	RateWindowS *int `json:"rateWindowS"`
	// QuotaPerDay 为每日调用上限；nil 表示不限额。
	QuotaPerDay *int `json:"quotaPerDay"`
	// QuotaPerMonth 为每月调用上限；nil 表示不限额。
	QuotaPerMonth *int `json:"quotaPerMonth"`
}

// rateLimitConfigResponse 为限流配置的对外视图，仅暴露限流相关字段，绝不回显密钥哈希。
type rateLimitConfigResponse struct {
	// ID 为 API Key 标识。
	ID string `json:"id"`
	// RateLimit 为窗口内的请求上限；nil 表示不限流。
	RateLimit *int `json:"rateLimit,omitempty"`
	// RateWindowS 为限流计数窗口秒数；nil 表示未配置。
	RateWindowS *int `json:"rateWindowS,omitempty"`
	// QuotaPerDay 为每日调用上限；nil 表示不限额。
	QuotaPerDay *int `json:"quotaPerDay,omitempty"`
	// QuotaPerMonth 为每月调用上限；nil 表示不限额。
	QuotaPerMonth *int `json:"quotaPerMonth,omitempty"`
}

type apiKeyUpstreamAccessRequest struct {
	Mode        apikey.UpstreamAccessMode `json:"mode"`
	UpstreamIDs []string                  `json:"upstreamIds"`
}

// registerAPIKeyRoutes 在管理分组下注册 API Key 生命周期、屏蔽规则、ACL 与限流配置端点。
func (r *Router) registerAPIKeyRoutes(g *gin.RouterGroup) {
	keys := g.Group("/apikeys")
	// 生命周期（Req 12.1）。
	keys.GET("", r.listAPIKeys)
	keys.POST("", r.createAPIKey)
	keys.GET("/:id", r.getAPIKey)
	keys.POST("/:id/enable", r.enableAPIKey)
	keys.POST("/:id/disable", r.disableAPIKey)
	keys.DELETE("/:id", r.deleteAPIKey)
	keys.PUT("/:id/risk-profile", r.updateAPIKeyRiskProfile)
	keys.GET("/:id/risk-impact", r.getAPIKeyRiskImpact)
	keys.GET("/:id/upstream-access", r.getAPIKeyUpstreamAccess)
	keys.PUT("/:id/upstream-access", r.updateAPIKeyUpstreamAccess)

	// API Key 级屏蔽规则（Req 13.1）：列表/创建按 API Key 分组。
	keys.GET("/:id/filters", r.listAPIKeyFilters)
	keys.POST("/:id/filters", r.createAPIKeyFilter)

	// 来源白名单 ACL（Req 13.9）：列表/创建按 API Key 分组。
	keys.GET("/:id/acl", r.listACL)
	keys.POST("/:id/acl", r.createACL)

	// 限流配置（Req 21）：读取/更新按 API Key 分组。
	keys.GET("/:id/ratelimit", r.getRateLimit)
	keys.PUT("/:id/ratelimit", r.updateRateLimit)

	// 屏蔽规则启停/删除按规则标识（独立前缀避免与 /apikeys/:id 通配冲突）。
	g.POST("/apikey-filters/:ruleId/enable", r.enableAPIKeyFilter)
	g.POST("/apikey-filters/:ruleId/disable", r.disableAPIKeyFilter)
	g.DELETE("/apikey-filters/:ruleId", r.deleteAPIKeyFilter)

	// 来源白名单删除按记录标识（独立前缀避免与 /apikeys/:id 通配冲突）。
	g.DELETE("/acl/:entryId", r.deleteACL)
}

func (r *Router) getAPIKeyUpstreamAccess(c *gin.Context) {
	if r.apiKeyUpstreams == nil {
		respondServiceUnavailable(c, "API Key 上游权限服务未就绪")
		return
	}
	cfg, err := r.apiKeyUpstreams.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, cfg)
}

func (r *Router) updateAPIKeyUpstreamAccess(c *gin.Context) {
	if r.apiKeyUpstreams == nil {
		respondServiceUnavailable(c, "API Key 上游权限服务未就绪")
		return
	}
	var req apiKeyUpstreamAccessRequest
	if !bindJSON(c, &req) {
		return
	}
	cfg, err := r.apiKeyUpstreams.Set(c.Request.Context(), c.Param("id"), apikey.UpstreamAccessConfig{
		Mode: req.Mode, UpstreamIDs: req.UpstreamIDs,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	r.invalidateToolSetCache()
	r.recordUpdate(c, audit.ResourceAPIKey, c.Param("id"))
	respondOK(c, cfg)
}

// listAPIKeys 返回全部 API Key 元数据（含明文，自部署场景供二次查看/复制）。
func (r *Router) listAPIKeys(c *gin.Context) {
	if r.apiKeys == nil {
		respondServiceUnavailable(c, "API Key 服务未就绪")
		return
	}
	keys, err := r.apiKeys.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"apiKeys": keys})
}

// createAPIKey 创建一个 API Key（Req 12.1）。
//
// 生成新的明文密钥（PlaintextKey）并持久化，响应中返回该明文。由于明文同时入库，
// 后续 List/Get 仍可经管理台二次查看；调用方仍应在创建时即提示用户妥善保存。
func (r *Router) createAPIKey(c *gin.Context) {
	if r.apiKeys == nil {
		respondServiceUnavailable(c, "API Key 服务未就绪")
		return
	}
	var req apiKeyCreateRequest
	if !bindJSON(c, &req) {
		return
	}
	created, err := r.apiKeys.Create(c.Request.Context(), apikey.CreateInput{
		Name:        req.Name,
		ExpiresAt:   req.ExpiresAt,
		RiskProfile: req.RiskProfile,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	r.recordCreate(c, audit.ResourceAPIKey, req.Name)
	respondCreated(c, created)
}

func (r *Router) updateAPIKeyRiskProfile(c *gin.Context) {
	service, ok := r.apiKeys.(interface {
		SetRiskProfile(context.Context, string, risk.Profile) error
	})
	if !ok {
		respondServiceUnavailable(c, "API Key 风险档案服务未就绪")
		return
	}
	var req apiKeyRiskProfileRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := service.SetRiskProfile(c.Request.Context(), c.Param("id"), req.RiskProfile); err != nil {
		respondError(c, err)
		return
	}
	r.invalidateToolSetCache()
	r.recordUpdate(c, audit.ResourceAPIKey, c.Param("id"))
	respondOK(c, gin.H{"id": c.Param("id"), "riskProfile": req.RiskProfile})
}

func (r *Router) getAPIKeyRiskImpact(c *gin.Context) {
	if r.toolRiskStore == nil || r.apiKeys == nil {
		respondServiceUnavailable(c, "API Key 风险预览服务未就绪")
		return
	}
	key, err := r.apiKeys.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	target := risk.Profile(c.Query("profile"))
	if !risk.ValidProfile(target) {
		respondError(c, domain.NewError(domain.CodeValidation, "目标风险档案无效"))
		return
	}
	current := key.RiskProfile
	if current == "" {
		current = risk.ProfileLegacy
	}
	currentVisible, targetVisible, newlyHidden, newlyAllowed := 0, 0, 0, 0
	byLevel := map[risk.Level]int{risk.LevelLow: 0, risk.LevelMedium: 0, risk.LevelHigh: 0, risk.LevelBlocked: 0}
	page := 1
	for {
		result, listErr := r.toolRiskStore.List(c.Request.Context(), store.RiskListQuery{Page: page, PageSize: 200})
		if listErr != nil {
			respondError(c, listErr)
			return
		}
		for _, item := range result.Items {
			if item.Status == risk.StatusRemoved {
				continue
			}
			level := risk.EffectiveLevel(item)
			byLevel[level]++
			before, after := risk.ProfileAllows(current, level), risk.ProfileAllows(target, level)
			if before {
				currentVisible++
			}
			if after {
				targetVisible++
			}
			if before && !after {
				newlyHidden++
			}
			if !before && after {
				newlyAllowed++
			}
		}
		if page*result.Size >= int(result.Total) || len(result.Items) == 0 {
			break
		}
		page++
	}
	respondOK(c, gin.H{"currentProfile": current, "targetProfile": target, "currentVisible": currentVisible,
		"targetVisible": targetVisible, "newlyHidden": newlyHidden, "newlyAllowed": newlyAllowed, "byLevel": byLevel})
}

// getAPIKey 查询单个 API Key 的元数据（含明文，Req 12.7）。
func (r *Router) getAPIKey(c *gin.Context) {
	if r.apiKeys == nil {
		respondServiceUnavailable(c, "API Key 服务未就绪")
		return
	}
	key, err := r.apiKeys.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, key)
}

// enableAPIKey 启用某个 API Key（Req 12.4）。
func (r *Router) enableAPIKey(c *gin.Context) {
	r.setAPIKeyEnabled(c, true)
}

// disableAPIKey 停用某个 API Key（Req 12.4）。
func (r *Router) disableAPIKey(c *gin.Context) {
	r.setAPIKeyEnabled(c, false)
}

// setAPIKeyEnabled 是启用/停用 API Key 的共用实现。
func (r *Router) setAPIKeyEnabled(c *gin.Context, enabled bool) {
	if r.apiKeys == nil {
		respondServiceUnavailable(c, "API Key 服务未就绪")
		return
	}
	if err := r.apiKeys.SetEnabled(c.Request.Context(), c.Param("id"), enabled); err != nil {
		respondError(c, err)
		return
	}
	r.recordUpdate(c, audit.ResourceAPIKey, c.Param("id"))
	respondOK(c, gin.H{"id": c.Param("id"), "enabled": enabled})
}

// deleteAPIKey 删除某个 API Key 并级联清理其屏蔽规则与 ACL（Req 12.2）。
func (r *Router) deleteAPIKey(c *gin.Context) {
	if r.apiKeys == nil {
		respondServiceUnavailable(c, "API Key 服务未就绪")
		return
	}
	if err := r.apiKeys.Delete(c.Request.Context(), c.Param("id")); err != nil {
		respondError(c, err)
		return
	}
	r.recordDelete(c, audit.ResourceAPIKey, c.Param("id"))
	respondNoContent(c)
}

// listAPIKeyFilters 返回某 API Key 的全部屏蔽规则（Req 13.1）。
func (r *Router) listAPIKeyFilters(c *gin.Context) {
	if r.apiKeyFilters == nil {
		respondServiceUnavailable(c, "API Key 屏蔽规则服务未就绪")
		return
	}
	filters, err := r.apiKeyFilters.List(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"filters": filters})
}

// createAPIKeyFilter 在某 API Key 上创建一条屏蔽规则（Req 13.1、13.4）。
//
// 字段级校验与数量上限由应用服务（apikey.FilterManager）强制，本层仅做接线。
func (r *Router) createAPIKeyFilter(c *gin.Context) {
	if r.apiKeyFilters == nil {
		respondServiceUnavailable(c, "API Key 屏蔽规则服务未就绪")
		return
	}
	var req apiKeyFilterRequest
	if !bindJSON(c, &req) {
		return
	}
	created, err := r.apiKeyFilters.Create(c.Request.Context(), apikey.CreateFilterInput{
		APIKeyID: c.Param("id"),
		Pattern:  req.Pattern,
		IsRegex:  req.IsRegex,
		Enabled:  req.Enabled,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	r.invalidateToolSetCache()
	r.recordCreate(c, audit.ResourceAPIKey, req.Pattern)
	respondCreated(c, created)
}

// enableAPIKeyFilter 启用一条 API Key 级屏蔽规则（Req 13.8）。
func (r *Router) enableAPIKeyFilter(c *gin.Context) {
	r.setAPIKeyFilterEnabled(c, true)
}

// disableAPIKeyFilter 停用一条 API Key 级屏蔽规则（Req 13.8）。
func (r *Router) disableAPIKeyFilter(c *gin.Context) {
	r.setAPIKeyFilterEnabled(c, false)
}

// setAPIKeyFilterEnabled 是 API Key 级屏蔽规则启停的共用实现。
func (r *Router) setAPIKeyFilterEnabled(c *gin.Context, enabled bool) {
	if r.apiKeyFilters == nil {
		respondServiceUnavailable(c, "API Key 屏蔽规则服务未就绪")
		return
	}
	if err := r.apiKeyFilters.SetEnabled(c.Request.Context(), c.Param("ruleId"), enabled); err != nil {
		respondError(c, err)
		return
	}
	r.invalidateToolSetCache()
	r.recordUpdate(c, audit.ResourceAPIKey, c.Param("ruleId"))
	respondOK(c, gin.H{"id": c.Param("ruleId"), "enabled": enabled})
}

// deleteAPIKeyFilter 删除一条 API Key 级屏蔽规则（Req 13.1）。
func (r *Router) deleteAPIKeyFilter(c *gin.Context) {
	if r.apiKeyFilters == nil {
		respondServiceUnavailable(c, "API Key 屏蔽规则服务未就绪")
		return
	}
	if err := r.apiKeyFilters.Delete(c.Request.Context(), c.Param("ruleId")); err != nil {
		respondError(c, err)
		return
	}
	r.invalidateToolSetCache()
	r.recordDelete(c, audit.ResourceAPIKey, c.Param("ruleId"))
	respondNoContent(c)
}

// listACL 返回某 API Key 的全部来源白名单（Req 13.9）。
func (r *Router) listACL(c *gin.Context) {
	if r.aclStore == nil {
		respondServiceUnavailable(c, "来源白名单服务未就绪")
		return
	}
	entries, err := r.aclStore.ListByAPIKey(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"acl": entries})
}

// createACL 为某 API Key 新增一条来源白名单（Req 13.9）。
//
// CIDR 文本的校验与规范化由仓储层完成（非法返回 VALIDATION，绑定 Key 不存在返回 NOT_FOUND）。
func (r *Router) createACL(c *gin.Context) {
	if r.aclStore == nil {
		respondServiceUnavailable(c, "来源白名单服务未就绪")
		return
	}
	var req aclEntryRequest
	if !bindJSON(c, &req) {
		return
	}
	created, err := r.aclStore.Create(c.Request.Context(), store.ACLEntry{
		APIKeyID: c.Param("id"),
		CIDR:     req.CIDR,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	r.recordCreate(c, audit.ResourceAPIKey, req.CIDR)
	respondCreated(c, created)
}

// deleteACL 删除一条来源白名单记录（Req 13.9）。
func (r *Router) deleteACL(c *gin.Context) {
	if r.aclStore == nil {
		respondServiceUnavailable(c, "来源白名单服务未就绪")
		return
	}
	if err := r.aclStore.Delete(c.Request.Context(), c.Param("entryId")); err != nil {
		respondError(c, err)
		return
	}
	r.recordDelete(c, audit.ResourceAPIKey, c.Param("entryId"))
	respondNoContent(c)
}

// getRateLimit 读取某 API Key 的限流配置（Req 21）。
func (r *Router) getRateLimit(c *gin.Context) {
	if r.rateLimitStore == nil {
		respondServiceUnavailable(c, "限流配置服务未就绪")
		return
	}
	key, err := r.rateLimitStore.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, rateLimitConfigResponse{
		ID:            key.ID,
		RateLimit:     key.RateLimit,
		RateWindowS:   key.RateWindowS,
		QuotaPerDay:   key.QuotaPerDay,
		QuotaPerMonth: key.QuotaPerMonth,
	})
}

// updateRateLimit 更新某 API Key 的限流配置（Req 21）。
//
// 流程：先读取既有 API Key 以沿用其不可变字段（哈希、前缀、名称等）→ 覆盖限流字段后回写。
// 两字段均为指针，置空即清除该项配置（不限流）。
func (r *Router) updateRateLimit(c *gin.Context) {
	if r.rateLimitStore == nil {
		respondServiceUnavailable(c, "限流配置服务未就绪")
		return
	}
	var req rateLimitConfigRequest
	if !bindJSON(c, &req) {
		return
	}
	existing, err := r.rateLimitStore.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	existing.RateLimit = req.RateLimit
	existing.RateWindowS = req.RateWindowS
	existing.QuotaPerDay = req.QuotaPerDay
	existing.QuotaPerMonth = req.QuotaPerMonth
	updated, err := r.rateLimitStore.Update(c.Request.Context(), existing)
	if err != nil {
		respondError(c, err)
		return
	}
	r.recordUpdate(c, audit.ResourceAPIKey, c.Param("id"))
	respondOK(c, rateLimitConfigResponse{
		ID:            updated.ID,
		RateLimit:     updated.RateLimit,
		RateWindowS:   updated.RateWindowS,
		QuotaPerDay:   updated.QuotaPerDay,
		QuotaPerMonth: updated.QuotaPerMonth,
	})
}
