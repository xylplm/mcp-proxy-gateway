package httpapi

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
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
	st.GET("/tools", r.statsTopTools)
	st.GET("/summary", r.statsSummary)
	st.GET("/daily", r.statsDaily)
	st.GET("/tool-errors", r.statsTopToolErrors)
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

// statsDaily 返回区间内按日聚合的调用趋势。
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
	days, err := r.stats.Daily(c.Request.Context(), start, end)
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
