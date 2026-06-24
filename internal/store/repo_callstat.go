package store

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

const (
	CallStatusSuccess       = "success"
	CallStatusUpstreamError = "upstream_error"
	CallStatusFailed        = "failed"
)

const maxDailySnapshotRunes = 100
const maxDailyErrorRunes = 1000

// CallStatRecord 是一次工具调用产生的统计事件。
//
// PostgreSQL 仅持久化按 UTC 日期聚合后的多维统计；请求、响应与失败详情仅供 Redis 最近记录
// 使用，不再写入 PostgreSQL 明细表。统计维度采用稳定标识 (UpstreamID, OriginalName)，
// ExposedName 仅作展示快照。
type CallStatRecord struct {
	// UpstreamID 为所属上游 MCP 标识；可为空（未知上游）。
	UpstreamID string
	// UpstreamName 为调用时的上游名称快照，仅作展示；可为空。
	UpstreamName string
	// OriginalName 为上游原始工具名（稳定标识）。
	OriginalName string
	// ExposedName 为调用时的对外名，仅作展示；可为空。
	ExposedName string
	// APIKeyID 为所用 API Key 标识；可为空。
	APIKeyID string
	// APIKeyName 为调用时的 API Key 名称快照，仅作展示；可为空。
	APIKeyName string
	// CalledAt 为调用时间（毫秒精度）。
	CalledAt time.Time
	// LatencyMS 为响应耗时（毫秒）。
	LatencyMS int
	// Success 表示执行结果是否成功。
	Success bool
	// Status stores success/upstream_error/failed.
	Status string
	// RequestArgs 为调用入参 JSON，仅写入 Redis 最近记录。
	RequestArgs json.RawMessage
	// ResponseResult 为调用出参 JSON，仅写入 Redis 最近记录。
	ResponseResult json.RawMessage
	// ErrorMessage 为调用失败时的错误说明。
	ErrorMessage string
	// FailureDetail stores diagnostic JSON for failed calls，仅写入 Redis 最近记录。
	FailureDetail json.RawMessage
	// Mode 为调用使用的 MCP 模式（full/smart），默认 full。
	Mode string
	// Source 为调用来源（api/xiaozhi），默认 api。
	Source string
}

// CallRecordView 是管理台调用记录列表与详情使用的单条调用视图。
type CallRecordView struct {
	ID             int64
	UpstreamID     string
	UpstreamName   string
	OriginalName   string
	ExposedName    string
	APIKeyID       string
	APIKeyName     string
	CalledAt       time.Time
	LatencyMS      int
	Success        bool
	Status         string
	RequestArgs    json.RawMessage
	ResponseResult json.RawMessage
	ErrorMessage   string
	FailureDetail  json.RawMessage
	Mode           string
	// Source 为调用来源（api/xiaozhi），默认 api。
	Source string
	// Description 为查询时实时拼接的当前工具描述，仅作展示，不持久化。
	// 由调用方（httpapi 层）据当前聚合工具集合填充，可能因别名规则变化而与调用当时不同。
	Description string
}

// CallRecordQuery 是最近调用记录列表的查询条件。
// Cursor 字段用于实时增量拉取；其他字段用于管理台从健康看板下钻定位异常记录。
type CallRecordQuery struct {
	Limit        int
	AfterID      int64
	AfterAt      time.Time
	Since        time.Time
	Until        time.Time
	UpstreamID   string
	OriginalName string
	Success      *bool
	Status       string
	MinLatencyMS int
}

// DimensionCount 为按某一维度（上游 MCP 或 API Key）聚合的调用条数（Req 16.2、16.4）。
type DimensionCount struct {
	// ID 为该维度的标识（上游 MCP 标识或 API Key 标识）；空串表示该维度为空。
	ID string
	// Count 为该维度在日期范围内的调用条数（含成功与失败）。
	Count int64
	// Source 仅在 ID 为空（维度为空）时有意义，记录该空组的调用来源（api/xiaozhi）。
	// 非空维度的 Source 为空串。用于区分「小智接入」与真正的未知。
	Source string
}

// ToolRank 为按工具维度的调用排行项（Req 16.3）。
type ToolRank struct {
	// UpstreamID 为工具所属上游 MCP 标识（稳定标识的一部分）。
	UpstreamID string
	// OriginalName 为上游原始工具名（稳定标识的一部分）。
	OriginalName string
	// Count 为该工具在日期范围内的调用次数。
	Count int64
}

// StatsSummary 为某个日期区间内的调用概览聚合。
type StatsSummary struct {
	TotalCalls      int64
	SuccessCalls    int64
	FailureCalls    int64
	ActiveUpstreams int64
	ActiveAPIKeys   int64
	UniqueTools     int64
	AvgLatencyMS    float64
	P95LatencyMS    float64
}

// DailyCount 为按 UTC 自然日聚合的调用趋势。
type DailyCount struct {
	Day          time.Time
	TotalCalls   int64
	SuccessCalls int64
	FailureCalls int64
	AvgLatencyMS float64
}

// ToolErrorRank 为按工具维度聚合的失败排行。
type ToolErrorRank struct {
	UpstreamID   string
	OriginalName string
	TotalCalls   int64
	FailureCalls int64
	LastFailedAt time.Time
	AvgLatencyMS float64
}

type CallHealthToolRank struct {
	UpstreamID   string
	UpstreamName string
	OriginalName string
	ExposedName  string
	Count        int64
	FailureCalls int64
	AvgLatencyMS float64
	P95LatencyMS float64
	LastError    string
}

type CallHealthUpstreamRank struct {
	UpstreamID   string
	UpstreamName string
	TotalCalls   int64
	FailureCalls int64
	SuccessRate  float64
	LastError    string
}

type CallHealth struct {
	Window        string
	Since         time.Time
	Until         time.Time
	TotalCalls    int64
	SuccessCalls  int64
	FailureCalls  int64
	SuccessRate   float64
	P50LatencyMS  float64
	P95LatencyMS  float64
	TopErrorTools []CallHealthToolRank
	TopSlowTools  []CallHealthToolRank
	TopUpstreams  []CallHealthUpstreamRank
}

type APIKeyToolUsage struct {
	UpstreamID   string
	OriginalName string
	Count        int64
}

type APIKeyUsageProfile struct {
	APIKeyID     string
	TotalCalls   int64
	SuccessCalls int64
	FailureCalls int64
	UniqueTools  int64
	AvgLatencyMS float64
	P95LatencyMS float64
	LastCalledAt time.Time
	LastFailedAt time.Time
	TopTools     []APIKeyToolUsage
}

// CallStatRepo 提供调用统计每日聚合事实表的批量写入与多维度查询。
type CallStatRepo struct {
	db *gorm.DB
}

// NewCallStatRepo 构造调用统计仓储。
func NewCallStatRepo(db *gorm.DB) *CallStatRepo {
	return &CallStatRepo{db: db}
}

// Insert 批量聚合调用统计事件并 upsert 到 call_stat_daily。
//
// 数据库不再保存调用明细；本方法按「UTC 日期 + source + mode + upstream + api_key + tool」
// 折叠批次内记录，然后累加写入每日多维聚合事实表。空切片为无操作。
func (r *CallStatRepo) Insert(ctx context.Context, records []CallStatRecord) error {
	if len(records) == 0 {
		return nil
	}
	models := aggregateDailyModels(records)
	if len(models) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "stat_date"},
			{Name: "source"},
			{Name: "mode"},
			{Name: "upstream_id"},
			{Name: "api_key_id"},
			{Name: "original_name"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"upstream_name_snapshot": gorm.Expr("CASE WHEN EXCLUDED.upstream_name_snapshot <> '' THEN EXCLUDED.upstream_name_snapshot ELSE call_stat_daily.upstream_name_snapshot END"),
			"api_key_name_snapshot":  gorm.Expr("CASE WHEN EXCLUDED.api_key_name_snapshot <> '' THEN EXCLUDED.api_key_name_snapshot ELSE call_stat_daily.api_key_name_snapshot END"),
			"exposed_name_snapshot":  gorm.Expr("CASE WHEN EXCLUDED.exposed_name_snapshot <> '' THEN EXCLUDED.exposed_name_snapshot ELSE call_stat_daily.exposed_name_snapshot END"),
			"total_calls":            gorm.Expr("call_stat_daily.total_calls + EXCLUDED.total_calls"),
			"success_calls":          gorm.Expr("call_stat_daily.success_calls + EXCLUDED.success_calls"),
			"failure_calls":          gorm.Expr("call_stat_daily.failure_calls + EXCLUDED.failure_calls"),
			"upstream_error_calls":   gorm.Expr("call_stat_daily.upstream_error_calls + EXCLUDED.upstream_error_calls"),
			"failed_calls":           gorm.Expr("call_stat_daily.failed_calls + EXCLUDED.failed_calls"),
			"latency_sum_ms":         gorm.Expr("call_stat_daily.latency_sum_ms + EXCLUDED.latency_sum_ms"),
			"latency_max_ms":         gorm.Expr("GREATEST(call_stat_daily.latency_max_ms, EXCLUDED.latency_max_ms)"),
			"failure_latency_sum_ms": gorm.Expr("call_stat_daily.failure_latency_sum_ms + EXCLUDED.failure_latency_sum_ms"),
			"latency_lt_50":          gorm.Expr("call_stat_daily.latency_lt_50 + EXCLUDED.latency_lt_50"),
			"latency_lt_100":         gorm.Expr("call_stat_daily.latency_lt_100 + EXCLUDED.latency_lt_100"),
			"latency_lt_200":         gorm.Expr("call_stat_daily.latency_lt_200 + EXCLUDED.latency_lt_200"),
			"latency_lt_500":         gorm.Expr("call_stat_daily.latency_lt_500 + EXCLUDED.latency_lt_500"),
			"latency_lt_1000":        gorm.Expr("call_stat_daily.latency_lt_1000 + EXCLUDED.latency_lt_1000"),
			"latency_lt_3000":        gorm.Expr("call_stat_daily.latency_lt_3000 + EXCLUDED.latency_lt_3000"),
			"latency_gte_3000":       gorm.Expr("call_stat_daily.latency_gte_3000 + EXCLUDED.latency_gte_3000"),
			"last_called_at":         gorm.Expr("CASE WHEN call_stat_daily.last_called_at IS NULL OR EXCLUDED.last_called_at > call_stat_daily.last_called_at THEN EXCLUDED.last_called_at ELSE call_stat_daily.last_called_at END"),
			"last_failed_at":         gorm.Expr("CASE WHEN call_stat_daily.last_failed_at IS NULL OR (EXCLUDED.last_failed_at IS NOT NULL AND EXCLUDED.last_failed_at > call_stat_daily.last_failed_at) THEN EXCLUDED.last_failed_at ELSE call_stat_daily.last_failed_at END"),
			"last_error_message":     gorm.Expr("CASE WHEN EXCLUDED.last_error_message <> '' AND (call_stat_daily.last_failed_at IS NULL OR EXCLUDED.last_failed_at >= call_stat_daily.last_failed_at) THEN EXCLUDED.last_error_message ELSE call_stat_daily.last_error_message END"),
			"updated_at":             gorm.Expr("now()"),
		}),
	}).CreateInBatches(models, 1000).Error
}

// EnrichRecords 为调用事件补齐当前上游/API Key 名称快照；查询失败时返回错误，由调用方决定是否降级。
func (r *CallStatRepo) EnrichRecords(ctx context.Context, records []CallStatRecord) ([]CallStatRecord, error) {
	if len(records) == 0 {
		return records, nil
	}
	upstreamIDs := uniqueValidUUIDs(records, func(rec CallStatRecord) (string, bool) {
		return rec.UpstreamID, rec.UpstreamName == ""
	})
	apiKeyIDs := uniqueValidUUIDs(records, func(rec CallStatRecord) (string, bool) {
		return rec.APIKeyID, rec.APIKeyName == ""
	})
	upstreamNames, err := r.lookupNames(ctx, "upstream_mcp", upstreamIDs)
	if err != nil {
		return nil, err
	}
	apiKeyNames, err := r.lookupNames(ctx, "api_key", apiKeyIDs)
	if err != nil {
		return nil, err
	}
	out := make([]CallStatRecord, len(records))
	copy(out, records)
	for i := range out {
		if out[i].UpstreamName == "" {
			out[i].UpstreamName = upstreamNames[out[i].UpstreamID]
		}
		if out[i].APIKeyName == "" {
			out[i].APIKeyName = apiKeyNames[out[i].APIKeyID]
		}
	}
	return out, nil
}

// CountByUpstream 统计日期范围内各上游 MCP 的调用条数（含成功失败）。
func (r *CallStatRepo) CountByUpstream(ctx context.Context, start, end time.Time) ([]DimensionCount, error) {
	startDate, endDate, err := normalizeStatDateRange(start, end)
	if err != nil {
		return nil, err
	}
	const q = `
		SELECT upstream_id AS id,
		       coalesce(sum(total_calls), 0)::bigint AS count,
		       CASE WHEN upstream_id = '' THEN max(source) ELSE '' END AS source
		FROM call_stat_daily
		WHERE stat_date >= ? AND stat_date <= ?
		GROUP BY upstream_id
		ORDER BY count DESC, id ASC`
	return r.queryDimensionCounts(ctx, q, startDate, endDate)
}

// CountByAPIKey 统计日期范围内各 API Key 的调用条数（含成功失败）。
func (r *CallStatRepo) CountByAPIKey(ctx context.Context, start, end time.Time) ([]DimensionCount, error) {
	startDate, endDate, err := normalizeStatDateRange(start, end)
	if err != nil {
		return nil, err
	}
	const q = `
		SELECT api_key_id AS id,
		       coalesce(sum(total_calls), 0)::bigint AS count,
		       CASE WHEN api_key_id = '' THEN source ELSE '' END AS source
		FROM call_stat_daily
		WHERE stat_date >= ? AND stat_date <= ?
		GROUP BY api_key_id, CASE WHEN api_key_id = '' THEN source ELSE '' END
		ORDER BY count DESC, id ASC`
	return r.queryDimensionCounts(ctx, q, startDate, endDate)
}

// TopTools 返回日期范围内按调用次数降序排列的工具排行。
func (r *CallStatRepo) TopTools(ctx context.Context, start, end time.Time, limit int) ([]ToolRank, error) {
	startDate, endDate, err := normalizeStatDateRange(start, end)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 1
	}
	const q = `
		SELECT upstream_id, original_name, coalesce(sum(total_calls), 0)::bigint AS count
		FROM call_stat_daily
		WHERE stat_date >= ? AND stat_date <= ?
		GROUP BY upstream_id, original_name
		ORDER BY count DESC, original_name ASC
		LIMIT ?`
	var rows []ToolRank
	if err := r.db.WithContext(ctx).Raw(q, startDate, endDate, limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		return []ToolRank{}, nil
	}
	return rows, nil
}

// Summary 返回日期范围内调用概览。P95LatencyMS 由每日聚合延迟桶估算。
func (r *CallStatRepo) Summary(ctx context.Context, start, end time.Time) (StatsSummary, error) {
	startDate, endDate, err := normalizeStatDateRange(start, end)
	if err != nil {
		return StatsSummary{}, err
	}
	const q = `
		SELECT
			coalesce(sum(total_calls), 0)::bigint AS total_calls,
			coalesce(sum(success_calls), 0)::bigint AS success_calls,
			coalesce(sum(failure_calls), 0)::bigint AS failure_calls,
			count(DISTINCT upstream_id) FILTER (WHERE upstream_id <> '') AS active_upstreams,
			count(DISTINCT api_key_id) FILTER (WHERE api_key_id <> '') AS active_api_keys,
			count(DISTINCT (upstream_id, original_name)) AS unique_tools,
			coalesce(sum(latency_sum_ms), 0)::bigint AS latency_sum_ms,
			coalesce(max(latency_max_ms), 0)::integer AS latency_max_ms,
			coalesce(sum(latency_lt_50), 0)::bigint AS latency_lt_50,
			coalesce(sum(latency_lt_100), 0)::bigint AS latency_lt_100,
			coalesce(sum(latency_lt_200), 0)::bigint AS latency_lt_200,
			coalesce(sum(latency_lt_500), 0)::bigint AS latency_lt_500,
			coalesce(sum(latency_lt_1000), 0)::bigint AS latency_lt_1000,
			coalesce(sum(latency_lt_3000), 0)::bigint AS latency_lt_3000,
			coalesce(sum(latency_gte_3000), 0)::bigint AS latency_gte_3000
		FROM call_stat_daily
		WHERE stat_date >= ? AND stat_date <= ?`
	var row summaryAggregateRow
	if err := r.db.WithContext(ctx).Raw(q, startDate, endDate).Scan(&row).Error; err != nil {
		return StatsSummary{}, err
	}
	out := StatsSummary{
		TotalCalls:      row.TotalCalls,
		SuccessCalls:    row.SuccessCalls,
		FailureCalls:    row.FailureCalls,
		ActiveUpstreams: row.ActiveUpstreams,
		ActiveAPIKeys:   row.ActiveAPIKeys,
		UniqueTools:     row.UniqueTools,
		P95LatencyMS:    estimateP95LatencyMS(row.latencyBuckets(), row.LatencyMaxMS),
	}
	if row.TotalCalls > 0 {
		out.AvgLatencyMS = float64(row.LatencySumMS) / float64(row.TotalCalls)
	}
	return out, nil
}

// Daily 返回日期范围内按 UTC 自然日聚合的调用趋势。
//
// tz 仅做 IANA 时区名校验以兼容旧 API；日聚合事实表固定使用 UTC 日期，不再按浏览器时区
// 动态切分自然日。
func (r *CallStatRepo) Daily(ctx context.Context, start, end time.Time, tz string) ([]DailyCount, error) {
	if _, err := normalizeTimezoneName(tz); err != nil {
		return nil, err
	}
	startDate, endDate, err := normalizeStatDateRange(start, end)
	if err != nil {
		return nil, err
	}
	const q = `
		SELECT
			stat_date::timestamp AT TIME ZONE 'UTC' AS day,
			coalesce(sum(total_calls), 0)::bigint AS total_calls,
			coalesce(sum(success_calls), 0)::bigint AS success_calls,
			coalesce(sum(failure_calls), 0)::bigint AS failure_calls,
			coalesce(sum(latency_sum_ms)::float8 / nullif(sum(total_calls), 0), 0)::float8 AS avg_latency_ms
		FROM call_stat_daily
		WHERE stat_date >= ? AND stat_date <= ?
		GROUP BY stat_date
		ORDER BY stat_date ASC`
	var result []DailyCount
	if err := r.db.WithContext(ctx).Raw(q, startDate, endDate).Scan(&result).Error; err != nil {
		return nil, err
	}
	if result == nil {
		return []DailyCount{}, nil
	}
	return result, nil
}

// TopToolErrors 返回日期范围内按失败次数降序排列的工具错误排行。
func (r *CallStatRepo) TopToolErrors(ctx context.Context, start, end time.Time, limit int) ([]ToolErrorRank, error) {
	startDate, endDate, err := normalizeStatDateRange(start, end)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 1
	}
	const q = `
		SELECT
			upstream_id,
			original_name,
			coalesce(sum(total_calls), 0)::bigint AS total_calls,
			coalesce(sum(failure_calls), 0)::bigint AS failure_calls,
			max(last_failed_at) AS last_failed_at,
			coalesce(sum(failure_latency_sum_ms)::float8 / nullif(sum(failure_calls), 0), 0)::float8 AS avg_latency_ms
		FROM call_stat_daily
		WHERE stat_date >= ? AND stat_date <= ?
		GROUP BY upstream_id, original_name
		HAVING coalesce(sum(failure_calls), 0) > 0
		ORDER BY failure_calls DESC, total_calls DESC, original_name ASC
		LIMIT ?`
	var rows []ToolErrorRank
	if err := r.db.WithContext(ctx).Raw(q, startDate, endDate, limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		return []ToolErrorRank{}, nil
	}
	return rows, nil
}

func (r *CallStatRepo) APIKeyUsageProfile(ctx context.Context, apiKeyID string, start, end time.Time, limit int) (APIKeyUsageProfile, error) {
	startDate, endDate, err := normalizeStatDateRange(start, end)
	if err != nil {
		return APIKeyUsageProfile{}, err
	}
	if limit <= 0 {
		limit = 1
	}
	const summaryQ = `
		SELECT
			coalesce(sum(total_calls), 0)::bigint AS total_calls,
			coalesce(sum(success_calls), 0)::bigint AS success_calls,
			coalesce(sum(failure_calls), 0)::bigint AS failure_calls,
			count(DISTINCT (upstream_id, original_name)) AS unique_tools,
			coalesce(sum(latency_sum_ms), 0)::bigint AS latency_sum_ms,
			coalesce(max(latency_max_ms), 0)::integer AS latency_max_ms,
			coalesce(sum(latency_lt_50), 0)::bigint AS latency_lt_50,
			coalesce(sum(latency_lt_100), 0)::bigint AS latency_lt_100,
			coalesce(sum(latency_lt_200), 0)::bigint AS latency_lt_200,
			coalesce(sum(latency_lt_500), 0)::bigint AS latency_lt_500,
			coalesce(sum(latency_lt_1000), 0)::bigint AS latency_lt_1000,
			coalesce(sum(latency_lt_3000), 0)::bigint AS latency_lt_3000,
			coalesce(sum(latency_gte_3000), 0)::bigint AS latency_gte_3000,
			max(last_called_at) AS last_called_at,
			max(last_failed_at) AS last_failed_at
		FROM call_stat_daily
		WHERE api_key_id = ? AND stat_date >= ? AND stat_date <= ?`
	var row apiKeyProfileAggregateRow
	if err := r.db.WithContext(ctx).Raw(summaryQ, apiKeyID, startDate, endDate).Scan(&row).Error; err != nil {
		return APIKeyUsageProfile{}, err
	}
	out := APIKeyUsageProfile{
		APIKeyID:     apiKeyID,
		TotalCalls:   row.TotalCalls,
		SuccessCalls: row.SuccessCalls,
		FailureCalls: row.FailureCalls,
		UniqueTools:  row.UniqueTools,
		P95LatencyMS: estimateP95LatencyMS(row.latencyBuckets(), row.LatencyMaxMS),
	}
	if row.LastCalledAt != nil {
		out.LastCalledAt = row.LastCalledAt.UTC()
	}
	if row.LastFailedAt != nil {
		out.LastFailedAt = row.LastFailedAt.UTC()
	}
	if row.TotalCalls > 0 {
		out.AvgLatencyMS = float64(row.LatencySumMS) / float64(row.TotalCalls)
	}

	const toolsQ = `
		SELECT upstream_id, original_name, coalesce(sum(total_calls), 0)::bigint AS count
		FROM call_stat_daily
		WHERE api_key_id = ? AND stat_date >= ? AND stat_date <= ?
		GROUP BY upstream_id, original_name
		ORDER BY count DESC, original_name ASC
		LIMIT ?`
	var tools []APIKeyToolUsage
	if err := r.db.WithContext(ctx).Raw(toolsQ, apiKeyID, startDate, endDate, limit).Scan(&tools).Error; err != nil {
		return APIKeyUsageProfile{}, err
	}
	if tools == nil {
		tools = []APIKeyToolUsage{}
	}
	out.TopTools = tools
	return out, nil
}

// DeleteOlderThan 删除早于 cutoff 所在 UTC 日期的每日聚合统计，返回删除行数。
func (r *CallStatRepo) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("stat_date < ?", utcDayStart(cutoff)).Delete(&callStatDailyModel{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

func aggregateDailyModels(records []CallStatRecord) []callStatDailyModel {
	groups := make(map[dailyAggregateKey]*callStatDailyModel, len(records))
	now := time.Now().UTC()
	for _, rec := range records {
		calledAt := rec.CalledAt.UTC()
		if calledAt.IsZero() {
			calledAt = now
		}
		status := normalizeCallStatus(rec.Status, rec.Success)
		key := dailyAggregateKey{
			StatDate:     utcDayStart(calledAt),
			Source:       normalizeSource(rec.Source),
			Mode:         normalizeMode(rec.Mode),
			UpstreamID:   truncateRunes(rec.UpstreamID, 36),
			APIKeyID:     truncateRunes(rec.APIKeyID, 36),
			OriginalName: truncateRunes(rec.OriginalName, maxDailySnapshotRunes),
		}
		model := groups[key]
		if model == nil {
			model = &callStatDailyModel{
				StatDate:             key.StatDate,
				Source:               key.Source,
				Mode:                 key.Mode,
				UpstreamID:           key.UpstreamID,
				UpstreamNameSnapshot: truncateRunes(rec.UpstreamName, maxDailySnapshotRunes),
				APIKeyID:             key.APIKeyID,
				APIKeyNameSnapshot:   truncateRunes(rec.APIKeyName, maxDailySnapshotRunes),
				OriginalName:         key.OriginalName,
				ExposedNameSnapshot:  truncateRunes(rec.ExposedName, maxDailySnapshotRunes),
				CreatedAt:            now,
				UpdatedAt:            now,
			}
			groups[key] = model
		} else {
			if rec.UpstreamName != "" {
				model.UpstreamNameSnapshot = truncateRunes(rec.UpstreamName, maxDailySnapshotRunes)
			}
			if rec.APIKeyName != "" {
				model.APIKeyNameSnapshot = truncateRunes(rec.APIKeyName, maxDailySnapshotRunes)
			}
			if rec.ExposedName != "" {
				model.ExposedNameSnapshot = truncateRunes(rec.ExposedName, maxDailySnapshotRunes)
			}
		}
		accumulateDailyModel(model, rec, calledAt, status)
	}

	models := make([]callStatDailyModel, 0, len(groups))
	for _, model := range groups {
		models = append(models, *model)
	}
	return models
}

func accumulateDailyModel(model *callStatDailyModel, rec CallStatRecord, calledAt time.Time, status string) {
	latency := rec.LatencyMS
	if latency < 0 {
		latency = 0
	}
	model.TotalCalls++
	model.LatencySumMS += int64(latency)
	if latency > model.LatencyMaxMS {
		model.LatencyMaxMS = latency
	}
	addLatencyBucket(model, latency)
	if model.LastCalledAt == nil || calledAt.After(*model.LastCalledAt) {
		model.LastCalledAt = timePtr(calledAt)
	}
	if status == CallStatusSuccess {
		model.SuccessCalls++
		return
	}
	model.FailureCalls++
	model.FailureLatencySumMS += int64(latency)
	if model.LastFailedAt == nil || calledAt.After(*model.LastFailedAt) {
		model.LastFailedAt = timePtr(calledAt)
		model.LastErrorMessage = truncateRunes(rec.ErrorMessage, maxDailyErrorRunes)
	}
	if status == CallStatusUpstreamError {
		model.UpstreamErrorCalls++
		return
	}
	model.FailedCalls++
}

func addLatencyBucket(model *callStatDailyModel, latency int) {
	switch {
	case latency < 50:
		model.LatencyLT50++
	case latency < 100:
		model.LatencyLT100++
	case latency < 200:
		model.LatencyLT200++
	case latency < 500:
		model.LatencyLT500++
	case latency < 1000:
		model.LatencyLT1000++
	case latency < 3000:
		model.LatencyLT3000++
	default:
		model.LatencyGTE3000++
	}
}

type dailyAggregateKey struct {
	StatDate     time.Time
	Source       string
	Mode         string
	UpstreamID   string
	APIKeyID     string
	OriginalName string
}

type summaryAggregateRow struct {
	TotalCalls      int64
	SuccessCalls    int64
	FailureCalls    int64
	ActiveUpstreams int64
	ActiveAPIKeys   int64
	UniqueTools     int64
	LatencySumMS    int64
	LatencyMaxMS    int
	LatencyLT50     int64
	LatencyLT100    int64
	LatencyLT200    int64
	LatencyLT500    int64
	LatencyLT1000   int64
	LatencyLT3000   int64
	LatencyGTE3000  int64
}

func (r summaryAggregateRow) latencyBuckets() []latencyBucketCount {
	return buildLatencyBuckets(r.LatencyLT50, r.LatencyLT100, r.LatencyLT200, r.LatencyLT500, r.LatencyLT1000, r.LatencyLT3000, r.LatencyGTE3000, r.LatencyMaxMS)
}

type apiKeyProfileAggregateRow struct {
	TotalCalls     int64
	SuccessCalls   int64
	FailureCalls   int64
	UniqueTools    int64
	LatencySumMS   int64
	LatencyMaxMS   int
	LatencyLT50    int64
	LatencyLT100   int64
	LatencyLT200   int64
	LatencyLT500   int64
	LatencyLT1000  int64
	LatencyLT3000  int64
	LatencyGTE3000 int64
	LastCalledAt   *time.Time
	LastFailedAt   *time.Time
}

func (r apiKeyProfileAggregateRow) latencyBuckets() []latencyBucketCount {
	return buildLatencyBuckets(r.LatencyLT50, r.LatencyLT100, r.LatencyLT200, r.LatencyLT500, r.LatencyLT1000, r.LatencyLT3000, r.LatencyGTE3000, r.LatencyMaxMS)
}

func buildLatencyBuckets(lt50, lt100, lt200, lt500, lt1000, lt3000, gte3000 int64, maxLatencyMS int) []latencyBucketCount {
	return []latencyBucketCount{
		{UpperBoundMS: 50, Count: lt50},
		{UpperBoundMS: 100, Count: lt100},
		{UpperBoundMS: 200, Count: lt200},
		{UpperBoundMS: 500, Count: lt500},
		{UpperBoundMS: 1000, Count: lt1000},
		{UpperBoundMS: 3000, Count: lt3000},
		{UpperBoundMS: float64(maxInt(maxLatencyMS, 3000)), Count: gte3000},
	}
}

type latencyBucketCount struct {
	UpperBoundMS float64
	Count        int64
}

func estimateP95LatencyMS(buckets []latencyBucketCount, maxLatencyMS int) float64 {
	total := int64(0)
	for _, bucket := range buckets {
		total += bucket.Count
	}
	if total == 0 {
		return 0
	}
	target := int64(math.Ceil(float64(total) * 0.95))
	if target < 1 {
		target = 1
	}
	seen := int64(0)
	for _, bucket := range buckets {
		seen += bucket.Count
		if seen >= target {
			if bucket.UpperBoundMS <= 0 {
				return float64(maxLatencyMS)
			}
			return bucket.UpperBoundMS
		}
	}
	return float64(maxLatencyMS)
}

func normalizeStatDateRange(start, end time.Time) (time.Time, time.Time, error) {
	if end.IsZero() {
		end = time.Now().UTC()
	}
	if err := validateRange(start, end); err != nil {
		return time.Time{}, time.Time{}, err
	}
	if start.IsZero() {
		start = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return utcDayStart(start), utcDayStart(end), nil
}

func utcDayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func uniqueValidUUIDs(records []CallStatRecord, pick func(CallStatRecord) (string, bool)) []string {
	seen := make(map[string]struct{})
	ids := make([]string, 0)
	for _, rec := range records {
		id, shouldLookup := pick(rec)
		if !shouldLookup || !isUUID(id) {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func (r *CallStatRepo) lookupNames(ctx context.Context, table string, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	switch table {
	case "upstream_mcp", "api_key":
	default:
		return out, nil
	}
	type row struct {
		ID   string
		Name string
	}
	var rows []row
	if err := r.db.WithContext(ctx).Raw("SELECT id::text AS id, name FROM "+table+" WHERE id IN ?", ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row.Name
	}
	return out, nil
}

// queryDimensionCounts 执行按单一维度分组计数的查询并扫描结果。
func (r *CallStatRepo) queryDimensionCounts(ctx context.Context, q string, start, end time.Time) ([]DimensionCount, error) {
	var rows []DimensionCount
	if err := r.db.WithContext(ctx).Raw(q, start, end).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		return []DimensionCount{}, nil
	}
	return rows, nil
}

func normalizeCallStatus(status string, success bool) string {
	switch status {
	case CallStatusSuccess, CallStatusUpstreamError, CallStatusFailed:
		return status
	}
	if success {
		return CallStatusSuccess
	}
	return CallStatusFailed
}

func normalizeMode(mode string) string {
	if mode == "smart" || mode == "full" {
		return mode
	}
	return "full"
}

// normalizeSource 归一化调用来源，仅接受 api/xiaozhi，其余回退 api。
func normalizeSource(source string) string {
	if source == "api" || source == "xiaozhi" {
		return source
	}
	return "api"
}

// validateRange checks that the statistics range start is not after end.
func validateRange(start, end time.Time) error {
	if start.After(end) {
		return domain.NewValidationError("统计时间范围无效：开始时间晚于结束时间", map[string]string{
			"start": "开始时间不得晚于结束时间",
		})
	}
	return nil
}

// normalizeTimezoneName 校验并归一化时区名，空串回退 UTC。
//
// 日聚合事实表固定使用 UTC 日期；保留本校验函数用于兼容旧 API 参数与错误语义。
func normalizeTimezoneName(tz string) (string, error) {
	if tz == "" {
		return "UTC", nil
	}
	if loc, err := time.LoadLocation(tz); err == nil && loc != nil {
		return tz, nil
	}
	return "", domain.NewValidationError("时区参数非法", map[string]string{
		"tz": "需为有效的 IANA 时区名，如 Asia/Shanghai",
	})
}

func timePtr(t time.Time) *time.Time {
	v := t.UTC()
	return &v
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
