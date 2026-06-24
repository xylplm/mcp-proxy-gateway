package httpapi

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 本文件实现统计排行查询端点（Req 16.2、16.3、16.4、17.5）：
//
//   GET /api/admin/stats/upstreams?start=&end=          各上游 MCP 区间调用条数
//   GET /api/admin/stats/apikeys?start=&end=            各 API Key 区间调用条数
//   GET /api/admin/stats/tools?start=&end=&limit=       工具调用排行（降序，至多 limit 条）
//   GET /api/admin/stats/summary?start=&end=            调用概览
//   GET /api/admin/stats/daily?start=&end=              每日调用趋势
//   GET /api/admin/stats/tool-errors?start=&end=&limit= 工具错误排行
//
// 时间区间通过查询参数 start/end 传入（RFC3339）。两者均可缺省：缺省 start 取零值
// （表示自最早记录起），缺省 end 取当前时刻。开始晚于结束的非法区间由下层统计服务
// 返回 VALIDATION（Req 16.7），本层透传映射为 400。limit 的默认值与范围收敛
// （默认取配置、范围 1-100）由统计服务负责（Req 16.3），本层仅解析整数。

// timeLayout 为统计/审计时间参数与响应时间字段统一采用的时间格式（RFC3339）。
const timeLayout = time.RFC3339
const apiKeyProfileDefaultDays = 7

// parseTimeRange 解析查询参数 start/end 为闭区间端点（Req 16.5）。
//
//   - start 缺省取零值（time.Time{}），表示自最早记录起；
//   - end 缺省取当前时刻；
//   - 任一参数提供但格式非法时返回字段级 VALIDATION 错误。
//
// 「开始晚于结束」的语义校验交由下层统计服务统一处理（Req 16.7），此处不重复判断。
func parseTimeRange(c *gin.Context) (start, end time.Time, err error) {
	fields := make(map[string]string)
	if s := c.Query("start"); s != "" {
		if t, perr := time.Parse(timeLayout, s); perr == nil {
			start = t
		} else {
			fields["start"] = "时间格式非法，需为 RFC3339（如 2006-01-02T15:04:05Z07:00）"
		}
	}
	if e := c.Query("end"); e != "" {
		if t, perr := time.Parse(timeLayout, e); perr == nil {
			end = t
		} else {
			fields["end"] = "时间格式非法，需为 RFC3339（如 2006-01-02T15:04:05Z07:00）"
		}
	} else {
		end = time.Now()
	}
	if len(fields) > 0 {
		return time.Time{}, time.Time{}, domain.NewValidationError("时间区间参数非法", fields)
	}
	return start, end, nil
}

// registerStatsRoutes 在管理分组下注册统计查询端点（Req 16.2、16.3、16.4）。
func (r *Router) registerStatsRoutes(g *gin.RouterGroup) {
	st := g.Group("/stats")
	st.GET("/upstreams", r.statsByUpstream)
	st.GET("/apikeys", r.statsByAPIKey)
	st.GET("/apikeys/:id/profile", r.statsAPIKeyUsageProfile)
	st.GET("/tools", r.statsTopTools)
	st.GET("/health", r.statsHealth)
	st.GET("/summary", r.statsSummary)
	st.GET("/daily", r.statsDaily)
	st.GET("/tool-errors", r.statsTopToolErrors)
	st.GET("/calls", r.statsCallRecords)
	st.GET("/calls/export", r.exportStatsCallRecords)
	st.DELETE("/calls", r.clearStatsCallRecords)
	st.GET("/calls/:id", r.statsCallRecordDetail)
}

// statsByUpstream 返回各上游 MCP 在区间内的调用条数（Req 16.2）。
func (r *Router) statsByUpstream(c *gin.Context) {
	if r.stats == nil {
		respondServiceUnavailable(c, "统计查询服务未就绪")
		return
	}
	start, end, err := parseTimeRange(c)
	if err != nil {
		respondError(c, err)
		return
	}
	counts, err := r.stats.CountByUpstream(c.Request.Context(), start, end)
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"counts": counts})
}

// statsByAPIKey 返回各 API Key 在区间内的调用条数（Req 16.4）。
func (r *Router) statsByAPIKey(c *gin.Context) {
	if r.stats == nil {
		respondServiceUnavailable(c, "统计查询服务未就绪")
		return
	}
	start, end, err := parseTimeRange(c)
	if err != nil {
		respondError(c, err)
		return
	}
	counts, err := r.stats.CountByAPIKey(c.Request.Context(), start, end)
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"counts": counts})
}

func (r *Router) statsAPIKeyUsageProfile(c *gin.Context) {
	if r.stats == nil {
		respondServiceUnavailable(c, "统计查询服务未就绪")
		return
	}
	apiKeyID := c.Param("id")
	if apiKeyID == "" {
		respondError(c, domain.NewValidationError("API Key 标识非法", map[string]string{
			"id": "不能为空",
		}))
		return
	}
	start, end, err := parseTimeRange(c)
	if err != nil {
		respondError(c, err)
		return
	}
	if start.IsZero() {
		start = end.AddDate(0, 0, -apiKeyProfileDefaultDays+1)
	}
	limit := 0
	if l := c.Query("limit"); l != "" {
		n, perr := strconv.Atoi(l)
		if perr != nil {
			respondError(c, domain.NewValidationError("排行条数参数非法", map[string]string{
				"limit": "需为整数",
			}))
			return
		}
		limit = n
	}
	profile, err := r.stats.APIKeyUsageProfile(c.Request.Context(), apiKeyID, start, end, limit)
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"profile": profile})
}

// statsTopTools 返回区间内按调用次数降序的工具排行（Req 16.3）。
//
// limit 经查询参数 limit 传入；缺省或为 0 时由统计服务取配置默认值，越界值由其收敛到 [1,100]。
// limit 非整数时返回字段级 VALIDATION。
func (r *Router) statsTopTools(c *gin.Context) {
	if r.stats == nil {
		respondServiceUnavailable(c, "统计查询服务未就绪")
		return
	}
	start, end, err := parseTimeRange(c)
	if err != nil {
		respondError(c, err)
		return
	}
	limit := 0
	if l := c.Query("limit"); l != "" {
		n, perr := strconv.Atoi(l)
		if perr != nil {
			respondError(c, domain.NewValidationError("排行条数参数非法", map[string]string{
				"limit": "需为整数",
			}))
			return
		}
		limit = n
	}
	ranks, err := r.stats.TopTools(c.Request.Context(), start, end, limit)
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"tools": ranks})
}

func (r *Router) statsHealth(c *gin.Context) {
	if r.stats == nil {
		respondServiceUnavailable(c, "统计查询服务未就绪")
		return
	}
	health, err := r.stats.Health(c.Request.Context(), c.DefaultQuery("window", "1h"), time.Now().UTC())
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"health": health})
}

// statsSummary 返回区间内调用概览。
func (r *Router) statsSummary(c *gin.Context) {
	if r.stats == nil {
		respondServiceUnavailable(c, "统计查询服务未就绪")
		return
	}
	start, end, err := parseTimeRange(c)
	if err != nil {
		respondError(c, err)
		return
	}
	summary, err := r.stats.Summary(c.Request.Context(), start, end)
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"summary": summary})
}

// statsDaily 返回区间内按 UTC 日聚合的调用趋势。
//
// tz 查询参数保留用于兼容旧调用方与参数校验；统计数据固定按 UTC 日期聚合。
func (r *Router) statsDaily(c *gin.Context) {
	if r.stats == nil {
		respondServiceUnavailable(c, "统计查询服务未就绪")
		return
	}
	start, end, err := parseTimeRange(c)
	if err != nil {
		respondError(c, err)
		return
	}
	days, err := r.stats.Daily(c.Request.Context(), start, end, c.Query("tz"))
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"days": days})
}

// statsTopToolErrors 返回区间内按失败次数降序的工具错误排行。
func (r *Router) statsTopToolErrors(c *gin.Context) {
	if r.stats == nil {
		respondServiceUnavailable(c, "统计查询服务未就绪")
		return
	}
	start, end, err := parseTimeRange(c)
	if err != nil {
		respondError(c, err)
		return
	}
	limit := 0
	if l := c.Query("limit"); l != "" {
		n, perr := strconv.Atoi(l)
		if perr != nil {
			respondError(c, domain.NewValidationError("排行条数参数非法", map[string]string{
				"limit": "需为整数",
			}))
			return
		}
		limit = n
	}
	ranks, err := r.stats.TopToolErrors(c.Request.Context(), start, end, limit)
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, gin.H{"tools": ranks})
}

// statsCallRecords 按最新时间倒序返回调用记录列表，afterId/afterAt 用于实时增量追加。
func (r *Router) statsCallRecords(c *gin.Context) {
	if r.stats == nil {
		respondServiceUnavailable(c, "统计查询服务未就绪")
		return
	}
	limit, afterID, afterAt, ok := parseCallRecordQuery(c, 30)
	if !ok {
		return
	}
	records, err := r.stats.ListRecords(c.Request.Context(), limit, afterID, afterAt)
	if err != nil {
		respondError(c, err)
		return
	}
	descMap := r.buildDescriptionMap(c.Request.Context())
	views := make([]callRecordResponseView, 0, len(records))
	for _, rec := range records {
		views = append(views, toCallRecordResponseView(rec, descMap))
	}
	respondOK(c, gin.H{"records": views})
}

func (r *Router) exportStatsCallRecords(c *gin.Context) {
	if r.stats == nil {
		respondServiceUnavailable(c, "统计查询服务未就绪")
		return
	}
	limit, afterID, afterAt, ok := parseCallRecordQuery(c, 100)
	if !ok {
		return
	}
	records, err := r.stats.ListRecords(c.Request.Context(), limit, afterID, afterAt)
	if err != nil {
		respondError(c, err)
		return
	}
	descMap := r.buildDescriptionMap(c.Request.Context())
	views := make([]callRecordResponseView, 0, len(records))
	for _, rec := range records {
		views = append(views, toCallRecordResponseView(rec, descMap))
	}
	respondJSONDownload(c, "mpg-call-records-"+time.Now().Format("20060102-150405")+".json", views)
}

// statsCallRecordDetail 返回单条调用记录详情。
func (r *Router) statsCallRecordDetail(c *gin.Context) {
	if r.stats == nil {
		respondServiceUnavailable(c, "统计查询服务未就绪")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		respondError(c, domain.NewValidationError("调用记录标识非法", map[string]string{
			"id": "需为正整数",
		}))
		return
	}
	record, err := r.stats.GetRecord(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	descMap := r.buildDescriptionMap(c.Request.Context())
	respondOK(c, gin.H{"record": toCallRecordResponseView(record, descMap)})
}

// clearStatsCallRecords 清空调用记录。
func (r *Router) clearStatsCallRecords(c *gin.Context) {
	if r.stats == nil {
		respondServiceUnavailable(c, "统计查询服务未就绪")
		return
	}
	deleted, err := r.stats.ClearRecords(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	r.recordUpdate(c, audit.ResourceSetting, "stats:calls:clear")
	respondOK(c, gin.H{"deleted": deleted})
}

func parseCallRecordQuery(c *gin.Context, defaultLimit int) (limit int, afterID int64, afterAt time.Time, ok bool) {
	limit = defaultLimit
	if l := c.Query("limit"); l != "" {
		n, perr := strconv.Atoi(l)
		if perr != nil {
			respondError(c, domain.NewValidationError("调用记录条数参数非法", map[string]string{
				"limit": "需为整数",
			}))
			return 0, 0, time.Time{}, false
		}
		limit = n
	}
	if v := c.Query("afterId"); v != "" {
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || n < 0 {
			respondError(c, domain.NewValidationError("调用记录游标参数非法", map[string]string{
				"afterId": "需为非负整数",
			}))
			return 0, 0, time.Time{}, false
		}
		afterID = n
	}
	if v := c.Query("afterAt"); v != "" {
		t, perr := time.Parse(timeLayout, v)
		if perr != nil {
			respondError(c, domain.NewValidationError("调用记录时间游标参数非法", map[string]string{
				"afterAt": "时间格式需为 RFC3339",
			}))
			return 0, 0, time.Time{}, false
		}
		afterAt = t
	}
	return limit, afterID, afterAt, true
}

// callRecordResponseView 是调用记录的管理台响应视图，在 store.CallRecordView 基础上
// 附加查询时实时拼接的工具描述（Description）。
//
// 字段名保持 PascalCase（与 store.CallRecordView 无 json tag 的历史序列化一致，前端
// CallRecord 类型据此对齐）。Description 为 view-only 字段，非 DB 列，由调用方经
// buildDescriptionMap 据「当前」聚合工具集合填充——因别名规则可能变化，它反映的是当前
// 最新描述而非调用当时的快照，对 hover 展示场景足够。
type callRecordResponseView struct {
	ID             int64           `json:"ID"`
	UpstreamID     string          `json:"UpstreamID"`
	UpstreamName   string          `json:"UpstreamName"`
	OriginalName   string          `json:"OriginalName"`
	ExposedName    string          `json:"ExposedName"`
	APIKeyID       string          `json:"APIKeyID"`
	APIKeyName     string          `json:"APIKeyName"`
	CalledAt       time.Time       `json:"CalledAt"`
	LatencyMS      int             `json:"LatencyMS"`
	Success        bool            `json:"Success"`
	Status         string          `json:"Status"`
	RequestArgs    json.RawMessage `json:"RequestArgs"`
	ResponseResult json.RawMessage `json:"ResponseResult"`
	ErrorMessage   string          `json:"ErrorMessage"`
	FailureDetail  json.RawMessage `json:"FailureDetail"`
	Mode           string          `json:"Mode"`
	Source         string          `json:"Source"`
	Description    string          `json:"Description"`
}

// buildDescriptionMap 构造「对外工具名 → 当前描述」映射，供调用记录附加 hover 描述。
//
// 以空 apiKeyID（全局视角）跑一次聚合 BuildToolSet，取回经别名/描述重写后的工具集合；
// 聚合服务未注入或构建失败时返回空映射（降级为不展示描述），不阻断调用记录查询。
// 不同 API Key 视角仅影响可见性、不改变描述，故用全局视角可覆盖所有记录。
func (r *Router) buildDescriptionMap(ctx context.Context) map[string]string {
	if r.aggregation == nil {
		return nil
	}
	tools, err := r.aggregation.BuildToolSet(ctx, "")
	if err != nil {
		return nil
	}
	m := make(map[string]string, len(tools))
	for _, t := range tools {
		if t.Name != "" && t.Description != "" {
			// 重复 Name 以后写入为准；同上游去重后 Name 通常唯一。
			m[t.Name] = t.Description
		}
	}
	return m
}

// toCallRecordResponseView 将仓储视图转为响应视图并附加工具描述。
func toCallRecordResponseView(rec store.CallRecordView, descMap map[string]string) callRecordResponseView {
	desc := ""
	if descMap != nil {
		// 优先按对外名匹配（别名重写后的展示名），回退到原始名。
		if name := rec.ExposedName; name != "" {
			desc = descMap[name]
		}
		if desc == "" && rec.OriginalName != "" {
			desc = descMap[rec.OriginalName]
		}
	}
	rec.Description = desc
	return callRecordResponseView{
		ID:             rec.ID,
		UpstreamID:     rec.UpstreamID,
		UpstreamName:   rec.UpstreamName,
		OriginalName:   rec.OriginalName,
		ExposedName:    rec.ExposedName,
		APIKeyID:       rec.APIKeyID,
		APIKeyName:     rec.APIKeyName,
		CalledAt:       rec.CalledAt,
		LatencyMS:      rec.LatencyMS,
		Success:        rec.Success,
		Status:         rec.Status,
		RequestArgs:    rec.RequestArgs,
		ResponseResult: rec.ResponseResult,
		ErrorMessage:   rec.ErrorMessage,
		FailureDetail:  rec.FailureDetail,
		Mode:           rec.Mode,
		Source:         rec.Source,
		Description:    rec.Description,
	}
}
