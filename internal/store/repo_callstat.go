package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 时间分区命名约定：call_stat 按「自然月」做声明式范围分区，分区表名形如
// call_stat_p<YYYYMM>，覆盖 [月初 00:00:00+00, 次月初 00:00:00+00) 的左闭右开区间（Req 16.10）。
//
// 将「所覆盖的月份」编码进分区表名，使保留期清理无需解析 PostgreSQL 的分区边界表达式，
// 仅凭表名即可推算该分区的时间范围，从而以 DROP 整张分区表的方式高效清理超期数据。
const partitionPrefix = "call_stat_p"

const (
	CallStatusSuccess       = "success"
	CallStatusUpstreamError = "upstream_error"
	CallStatusFailed        = "failed"
)

// CallStatRecord 是一条调用统计记录（call_stat 表）（Req 16.1）。
//
// 统计维度采用稳定标识 (UpstreamID, OriginalName)，不随别名重命名或上游重排序而断裂；
// ExposedName 仅作展示。CalledAt 为毫秒精度时间戳。
type CallStatRecord struct {
	// UpstreamID 为所属上游 MCP 标识；可为空（未知上游）。
	UpstreamID string
	// OriginalName 为上游原始工具名（稳定标识）。
	OriginalName string
	// ExposedName 为调用时的对外名，仅作展示；可为空。
	ExposedName string
	// APIKeyID 为所用 API Key 标识；可为空。
	APIKeyID string
	// CalledAt 为调用时间（毫秒精度）。
	CalledAt time.Time
	// LatencyMS 为响应耗时（毫秒）。
	LatencyMS int
	// Success 表示执行结果是否成功。
	Success bool
	// Status stores success/upstream_error/failed.
	Status string
	// RequestArgs 为调用入参 JSON。
	RequestArgs json.RawMessage
	// ResponseResult 为调用出参 JSON。
	ResponseResult json.RawMessage
	// ErrorMessage 为调用失败时的错误说明。
	ErrorMessage string
	// FailureDetail stores diagnostic JSON for failed calls.
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

// DimensionCount 为按某一维度（上游 MCP 或 API Key）聚合的调用条数（Req 16.2、16.4）。
type DimensionCount struct {
	// ID 为该维度的标识（上游 MCP 标识或 API Key 标识）；空串表示该维度为 NULL。
	ID string
	// Count 为该维度在时间范围内的调用条数（含成功与失败）。
	Count int64
	// Source 仅在 ID 为空（维度 NULL）时有意义，记录该 NULL 组的主要调用来源
	// （api/xiaozhi）。非 NULL 维度的 Source 为空串。用于区分「小智接入」与真正的未知。
	Source string
}

// ToolRank 为按工具维度的调用排行项（Req 16.3）。
type ToolRank struct {
	// UpstreamID 为工具所属上游 MCP 标识（稳定标识的一部分）。
	UpstreamID string
	// OriginalName 为上游原始工具名（稳定标识的一部分）。
	OriginalName string
	// Count 为该工具在时间范围内的调用次数。
	Count int64
}

// StatsSummary 为某个时间区间内的调用概览聚合。
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

// DailyCount 为按自然日聚合的调用趋势。
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

// CallStatRepo 提供调用统计（call_stat 表）的批量写入与多维度查询。
type CallStatRepo struct {
	db *gorm.DB
}

// NewCallStatRepo 构造调用统计仓储。
func NewCallStatRepo(db *gorm.DB) *CallStatRepo {
	return &CallStatRepo{db: db}
}

// Insert 批量写入调用统计记录，供后台 worker 将异步缓冲批量落库使用（Req 16.1、16.8）。
//
// 使用 GORM 分批插入；空切片为无操作。id 由 BIGSERIAL 自增生成。
func (r *CallStatRepo) Insert(ctx context.Context, records []CallStatRecord) error {
	if len(records) == 0 {
		return nil
	}
	models := make([]callStatModel, 0, len(records))
	for _, rec := range records {
		upstreamID, err := nullableUUID(rec.UpstreamID)
		if err != nil {
			return err
		}
		apiKeyID, err := nullableUUID(rec.APIKeyID)
		if err != nil {
			return err
		}
		models = append(models, callStatModel{
			UpstreamID:     upstreamID,
			OriginalName:   rec.OriginalName,
			ExposedName:    nullableString(rec.ExposedName),
			APIKeyID:       apiKeyID,
			CalledAt:       rec.CalledAt,
			LatencyMS:      rec.LatencyMS,
			Success:        rec.Success,
			Status:         normalizeCallStatus(rec.Status, rec.Success),
			RequestArgs:    JSONB(rec.RequestArgs),
			ResponseResult: JSONB(rec.ResponseResult),
			ErrorMessage:   nullableString(rec.ErrorMessage),
			FailureDetail:  JSONB(rec.FailureDetail),
			Mode:           normalizeMode(rec.Mode),
			Source:         normalizeSource(rec.Source),
		})
	}
	return r.db.WithContext(ctx).CreateInBatches(models, 1000).Error
}

// ListRecords 按调用时间倒序分页返回调用记录。afterAt/afterID 用于前端停留页面时增量追加最新调用。
func (r *CallStatRepo) ListRecords(ctx context.Context, limit int, afterID int64, afterAt time.Time) ([]CallRecordView, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	const q = `
		SELECT
			cs.id,
			cs.upstream_id,
			coalesce(up.name, '') AS upstream_name,
			cs.original_name,
			coalesce(cs.exposed_name, '') AS exposed_name,
			cs.api_key_id,
			coalesce(ak.name, '') AS api_key_name,
			cs.called_at,
			cs.latency_ms,
			cs.success,
			cs.status,
			coalesce(cs.request_args, 'null'::jsonb) AS request_args,
			coalesce(cs.response_result, 'null'::jsonb) AS response_result,
			coalesce(cs.error_message, '') AS error_message,
			coalesce(cs.failure_detail, 'null'::jsonb) AS failure_detail,
			cs.mode,
			cs.source
		FROM call_stat cs
		LEFT JOIN upstream_mcp up ON up.id = cs.upstream_id
		LEFT JOIN api_key ak ON ak.id = cs.api_key_id
		WHERE (
			(?::bigint = 0 AND ?::timestamptz IS NULL)
			OR cs.id > ?
			OR (?::timestamptz IS NOT NULL AND (cs.called_at > ? OR (cs.called_at = ? AND cs.id > ?)))
		)
		ORDER BY cs.called_at DESC, cs.id DESC
		LIMIT ?`
	var afterAtParam any
	if !afterAt.IsZero() {
		afterAtParam = afterAt
	}
	var rows []callRecordRow
	err := r.db.WithContext(ctx).Raw(q, afterID, afterAtParam, afterID, afterAtParam, afterAtParam, afterAtParam, afterID, limit).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return callRecordRowsToViews(rows), nil
}

// GetRecord 按 ID 读取单条调用记录详情。
func (r *CallStatRepo) GetRecord(ctx context.Context, id int64) (CallRecordView, error) {
	const q = `
		SELECT
			cs.id,
			cs.upstream_id,
			coalesce(up.name, '') AS upstream_name,
			cs.original_name,
			coalesce(cs.exposed_name, '') AS exposed_name,
			cs.api_key_id,
			coalesce(ak.name, '') AS api_key_name,
			cs.called_at,
			cs.latency_ms,
			cs.success,
			cs.status,
			coalesce(cs.request_args, 'null'::jsonb) AS request_args,
			coalesce(cs.response_result, 'null'::jsonb) AS response_result,
			coalesce(cs.error_message, '') AS error_message,
			coalesce(cs.failure_detail, 'null'::jsonb) AS failure_detail,
			cs.mode,
			cs.source
		FROM call_stat cs
		LEFT JOIN upstream_mcp up ON up.id = cs.upstream_id
		LEFT JOIN api_key ak ON ak.id = cs.api_key_id
		WHERE cs.id = ?
		ORDER BY cs.called_at DESC
		LIMIT 1`
	var rows []callRecordRow
	if err := r.db.WithContext(ctx).Raw(q, id).Scan(&rows).Error; err != nil {
		return CallRecordView{}, err
	}
	records := callRecordRowsToViews(rows)
	if len(records) == 0 {
		return CallRecordView{}, domain.NewError(domain.CodeNotFound, "调用记录不存在")
	}
	return records[0], nil
}

// ClearRecordsBefore removes call records not newer than cutoff.
func (r *CallStatRepo) ClearRecordsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("called_at <= ?", cutoff).Delete(&callStatModel{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// CountByUpstream 统计 [start, end] 闭区间内各上游 MCP 的调用条数（含成功失败）（Req 16.2、16.5）。
//
// 当 upstream_id 为 NULL（未知上游）时，按 source 二次分组，使「小智接入」与真正的未知上游
// 区分开（小智调用的 upstream_id 通常非空，此处分组主要为对称与健壮性）。
//   - start 晚于 end 返回 CodeValidation（Req 16.7）。
//   - 无记录返回空切片而非错误（Req 16.6）。
func (r *CallStatRepo) CountByUpstream(ctx context.Context, start, end time.Time) ([]DimensionCount, error) {
	if err := validateRange(start, end); err != nil {
		return nil, err
	}
	const q = `
		SELECT upstream_id AS id, count(*) AS count, max(source) AS source
		FROM call_stat
		WHERE called_at >= ? AND called_at <= ?
		GROUP BY upstream_id
		ORDER BY count(*) DESC`
	return r.queryDimensionCounts(ctx, q, start, end)
}

// CountByAPIKey 统计 [start, end] 闭区间内各 API Key 的调用条数（含成功失败）（Req 16.4、16.5）。
//
// 当 api_key_id 为 NULL 时，按 source 区分：小智接入（source=xiaozhi）单独成组、
// Source=xiaozhi；其余真正的未知（source=api）保留为未知、Source=api。非 NULL 的 API Key
// 取其来源（通常为 api），不影响分组粒度。
//   - start 晚于 end 返回 CodeValidation（Req 16.7）。
//   - 无记录返回空切片而非错误（Req 16.6）。
func (r *CallStatRepo) CountByAPIKey(ctx context.Context, start, end time.Time) ([]DimensionCount, error) {
	if err := validateRange(start, end); err != nil {
		return nil, err
	}
	const q = `
		SELECT api_key_id AS id, count(*) AS count,
		       CASE WHEN bool_and(api_key_id IS NULL)
		            THEN max(source)
		            ELSE 'api' END AS source
		FROM call_stat
		WHERE called_at >= ? AND called_at <= ?
		GROUP BY api_key_id,
		         CASE WHEN api_key_id IS NULL THEN source END
		ORDER BY count(*) DESC`
	return r.queryDimensionCounts(ctx, q, start, end)
}

// TopTools 返回 [start, end] 闭区间内按调用次数降序排列的工具排行（Req 16.3、16.5）。
//
// 基于稳定标识 (upstream_id, original_name) 聚合；limit 限制返回条数。
//   - start 晚于 end 返回 CodeValidation（Req 16.7）。
//   - limit ≤ 0 时按 1 处理，避免无意义查询。
//   - 无记录返回空切片而非错误（Req 16.6）。
func (r *CallStatRepo) TopTools(ctx context.Context, start, end time.Time, limit int) ([]ToolRank, error) {
	if err := validateRange(start, end); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 1
	}
	const q = `
		SELECT upstream_id, original_name, count(*) AS count
		FROM call_stat
		WHERE called_at >= ? AND called_at <= ?
		GROUP BY upstream_id, original_name
		ORDER BY count DESC, original_name ASC
		LIMIT ?`
	type row struct {
		UpstreamID   *string
		OriginalName string
		Count        int64
	}
	var rows []row
	if err := r.db.WithContext(ctx).Raw(q, start, end, limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]ToolRank, 0, len(rows))
	for _, row := range rows {
		result = append(result, ToolRank{UpstreamID: stringValue(row.UpstreamID), OriginalName: row.OriginalName, Count: row.Count})
	}
	return result, nil
}

// Summary 返回 [start, end] 闭区间内调用概览。
func (r *CallStatRepo) Summary(ctx context.Context, start, end time.Time) (StatsSummary, error) {
	if err := validateRange(start, end); err != nil {
		return StatsSummary{}, err
	}
	const q = `
		SELECT
			count(*) AS total_calls,
			count(*) FILTER (WHERE success) AS success_calls,
			count(*) FILTER (WHERE NOT success) AS failure_calls,
			count(DISTINCT upstream_id) FILTER (WHERE upstream_id IS NOT NULL) AS active_upstreams,
			count(DISTINCT api_key_id) FILTER (WHERE api_key_id IS NOT NULL) AS active_api_keys,
			count(DISTINCT (upstream_id, original_name)) AS unique_tools,
			coalesce(avg(latency_ms), 0)::float8 AS avg_latency_ms,
			coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0)::float8 AS p95_latency_ms
		FROM call_stat
		WHERE called_at >= ? AND called_at <= ?`
	var out StatsSummary
	if err := r.db.WithContext(ctx).Raw(q, start, end).Scan(&out).Error; err != nil {
		return StatsSummary{}, err
	}
	return out, nil
}

// Daily 返回 [start, end] 闭区间内按指定时区自然日聚合的调用趋势。
//
// tz 为 IANA 时区名（如 "Asia/Shanghai"），决定「一天」的切分边界；空串回退 UTC。
// 时区名非法时返回字段级 VALIDATION 错误。调用方负责用与 tz 一致的本地日期 key
// 匹配结果，避免前后端因时区定义不一致导致热力图错位。
func (r *CallStatRepo) Daily(ctx context.Context, start, end time.Time, tz string) ([]DailyCount, error) {
	if err := validateRange(start, end); err != nil {
		return nil, err
	}
	zoneName, err := normalizeTimezoneName(tz)
	if err != nil {
		return nil, err
	}
	const q = `
		SELECT
			(date_trunc('day', called_at AT TIME ZONE ?))::date AS day,
			count(*) AS total_calls,
			count(*) FILTER (WHERE success) AS success_calls,
			count(*) FILTER (WHERE NOT success) AS failure_calls,
			coalesce(avg(latency_ms), 0)::float8 AS avg_latency_ms
		FROM call_stat
		WHERE called_at >= ? AND called_at <= ?
		GROUP BY day
		ORDER BY day ASC`
	var result []DailyCount
	if err := r.db.WithContext(ctx).Raw(q, zoneName, start, end).Scan(&result).Error; err != nil {
		return nil, err
	}
	if result == nil {
		return []DailyCount{}, nil
	}
	return result, nil
}

// TopToolErrors 返回 [start, end] 闭区间内按失败次数降序排列的工具错误排行。
func (r *CallStatRepo) TopToolErrors(ctx context.Context, start, end time.Time, limit int) ([]ToolErrorRank, error) {
	if err := validateRange(start, end); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 1
	}
	const q = `
		SELECT
			upstream_id,
			original_name,
			count(*) AS total_calls,
			count(*) FILTER (WHERE NOT success) AS failure_calls,
			max(called_at) FILTER (WHERE NOT success) AS last_failed_at,
			coalesce(avg(latency_ms) FILTER (WHERE NOT success), 0)::float8 AS avg_latency_ms
		FROM call_stat
		WHERE called_at >= ? AND called_at <= ?
		GROUP BY upstream_id, original_name
		HAVING count(*) FILTER (WHERE NOT success) > 0
		ORDER BY failure_calls DESC, total_calls DESC, original_name ASC
		LIMIT ?`
	type row struct {
		UpstreamID   *string
		OriginalName string
		TotalCalls   int64
		FailureCalls int64
		LastFailedAt time.Time
		AvgLatencyMS float64
	}
	var rows []row
	if err := r.db.WithContext(ctx).Raw(q, start, end, limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]ToolErrorRank, 0, len(rows))
	for _, row := range rows {
		result = append(result, ToolErrorRank{
			UpstreamID:   stringValue(row.UpstreamID),
			OriginalName: row.OriginalName,
			TotalCalls:   row.TotalCalls,
			FailureCalls: row.FailureCalls,
			LastFailedAt: row.LastFailedAt,
			AvgLatencyMS: row.AvgLatencyMS,
		})
	}
	return result, nil
}

// EnsurePartitions 为 [now-1月, now+ahead月] 范围内的每个自然月按需创建时间分区（Req 16.10）。
//
// 调用统计按 called_at 做声明式月分区：本方法在保留期清理前先确保「当前月及临近若干个
// 未来月」的分区已存在，使新写入的记录落入具体时间分区而非默认分区——只有落入时间分区的
// 数据才能在超期后被整分区 DROP 高效清理。已存在的分区因使用 IF NOT EXISTS 而被跳过，
// 故本方法幂等，可由定时任务安全地反复调用。
//
//   - ahead 为预建的未来月数（<0 时按 0 处理），通常取 1，保证跨月时刻仍有分区可落。
//   - 同时补建上一个月分区，避免边界时刻（如月初）写入落空到默认分区。
//
// 分区创建失败立即返回错误；调用方可据此告警，但不应因此阻断主流程的统计写入
// （写入失败本就静默降级，Req 16.9）。
func (r *CallStatRepo) EnsurePartitions(ctx context.Context, now time.Time, ahead int) error {
	if ahead < 0 {
		ahead = 0
	}
	// 从上一个月起，覆盖当前月与未来 ahead 个月，确保边界时刻写入有分区可落。
	base := monthStart(now.UTC())
	for offset := -1; offset <= ahead; offset++ {
		monthBegin := base.AddDate(0, offset, 0)
		if err := r.createMonthlyPartition(ctx, monthBegin); err != nil {
			return err
		}
	}
	return nil
}

// DropPartitionsOlderThan 删除「整段时间范围都早于 cutoff」的月分区，返回被删除的分区数（Req 16.10）。
//
// 仅当某分区覆盖区间的上界（次月月初）不晚于 cutoff 时，该分区内全部记录才确定超期，
// 此时以 DROP TABLE 整分区删除，远比逐行 DELETE 高效。对「跨越 cutoff」的边界分区不做
// DROP（其中尚有未超期记录），其超期部分由 DeleteOlderThan 兜底逐行清理。
//
// 默认分区（call_stat_default）因边界未知不参与 DROP，仅通过 DeleteOlderThan 兜底。
func (r *CallStatRepo) DropPartitionsOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	names, err := r.listTimePartitions(ctx)
	if err != nil {
		return 0, err
	}
	cutoffUTC := cutoff.UTC()
	dropped := 0
	for _, name := range names {
		monthBegin, ok := parsePartitionMonth(name)
		if !ok {
			continue
		}
		// 分区覆盖 [monthBegin, 次月月初)；上界不晚于 cutoff 时整分区均已超期，可整体删除。
		upper := monthBegin.AddDate(0, 1, 0)
		if !upper.After(cutoffUTC) {
			// 分区名来自系统目录（catalog）枚举，非外部输入；仍以受控前缀+月份格式构造，避免注入。
			stmt := fmt.Sprintf(`DROP TABLE IF EXISTS %s`, quoteIdentifier(name))
			if derr := r.db.WithContext(ctx).Exec(stmt).Error; derr != nil {
				return dropped, derr
			}
			dropped++
		}
	}
	return dropped, nil
}

// DeleteOlderThan 清理 called_at 早于 cutoff 的调用统计记录，返回删除条数（Req 16.10）。
//
// 作为分区 DROP 的兜底：整分区删除只能清掉「整段超期」的月分区，而跨越 cutoff 的边界
// 分区与默认分区中仍可能残留超期记录，本方法按时间逐行删除以保证清理边界精确。
func (r *CallStatRepo) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("called_at < ?", cutoff).Delete(&callStatModel{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// createMonthlyPartition 为 monthBegin 所在自然月创建一个范围分区（已存在则跳过）。
//
// 分区覆盖 [月初, 次月月初) 的左闭右开区间，与 PostgreSQL RANGE 分区语义一致；表名将
// 月份编码为 call_stat_p<YYYYMM>，便于清理时仅凭表名推算区间。
func (r *CallStatRepo) createMonthlyPartition(ctx context.Context, monthBegin time.Time) error {
	begin := monthStart(monthBegin.UTC())
	end := begin.AddDate(0, 1, 0)
	name := partitionName(begin)
	// 表名与边界字面量均由受控的时间值格式化而来，非外部输入，无注入风险。
	stmt := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF call_stat FOR VALUES FROM ('%s') TO ('%s')`,
		quoteIdentifier(name),
		begin.Format("2006-01-02 15:04:05-07"),
		end.Format("2006-01-02 15:04:05-07"),
	)
	return r.db.WithContext(ctx).Exec(stmt).Error
}

// listTimePartitions 枚举 call_stat 下所有「时间分区」的表名（不含默认分区与父表）。
//
// 经 pg_inherits/pg_class 查询父表 call_stat 的直接子分区，并仅保留符合
// call_stat_p<YYYYMM> 命名约定者，从而排除默认分区 call_stat_default。
func (r *CallStatRepo) listTimePartitions(ctx context.Context) ([]string, error) {
	const q = `
		SELECT c.relname
		FROM pg_inherits i
		JOIN pg_class c   ON c.oid = i.inhrelid
		JOIN pg_class p   ON p.oid = i.inhparent
		WHERE p.relname = 'call_stat'`
	type row struct{ Relname string }
	var rows []row
	if err := r.db.WithContext(ctx).Raw(q).Scan(&rows).Error; err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		if _, ok := parsePartitionMonth(row.Relname); ok {
			names = append(names, row.Relname)
		}
	}
	return names, nil
}

// monthStart 返回 t 所在自然月月初（当月 1 日 00:00:00），并归一到 UTC。
func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// partitionName 依据月初时间生成分区表名 call_stat_p<YYYYMM>。
func partitionName(monthBegin time.Time) string {
	return fmt.Sprintf("%s%04d%02d", partitionPrefix, monthBegin.Year(), int(monthBegin.Month()))
}

// parsePartitionMonth 从分区表名解析其覆盖月份的月初时间（UTC）。
//
// 仅接受 call_stat_p<YYYYMM> 形态：前缀匹配且尾部为 6 位、月份在 1..12 内方为有效，
// 借此排除默认分区与任何不符合命名约定的子表。
func parsePartitionMonth(name string) (time.Time, bool) {
	suffix, ok := strings.CutPrefix(name, partitionPrefix)
	if !ok || len(suffix) != 6 {
		return time.Time{}, false
	}
	year, err := strconv.Atoi(suffix[:4])
	if err != nil {
		return time.Time{}, false
	}
	month, err := strconv.Atoi(suffix[4:])
	if err != nil || month < 1 || month > 12 {
		return time.Time{}, false
	}
	return time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC), true
}

// queryDimensionCounts 执行按单一维度分组计数的查询并扫描结果。
func (r *CallStatRepo) queryDimensionCounts(ctx context.Context, q string, start, end time.Time) ([]DimensionCount, error) {
	type row struct {
		ID     *string
		Count  int64
		Source string
	}
	var rows []row
	if err := r.db.WithContext(ctx).Raw(q, start, end).Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]DimensionCount, 0, len(rows))
	for _, row := range rows {
		result = append(result, DimensionCount{ID: stringValue(row.ID), Count: row.Count, Source: row.Source})
	}
	return result, nil
}

type callRecordRow struct {
	ID             int64
	UpstreamID     *string
	UpstreamName   string
	OriginalName   string
	ExposedName    string
	APIKeyID       *string
	APIKeyName     string
	CalledAt       time.Time
	LatencyMS      int
	Success        bool
	Status         string
	RequestArgs    JSONB
	ResponseResult JSONB
	ErrorMessage   string
	FailureDetail  JSONB
	Mode           string
	Source         string
}

func callRecordRowsToViews(rows []callRecordRow) []CallRecordView {
	result := make([]CallRecordView, 0, len(rows))
	for _, row := range rows {
		item := CallRecordView{
			ID:             row.ID,
			UpstreamID:     stringValue(row.UpstreamID),
			UpstreamName:   row.UpstreamName,
			OriginalName:   row.OriginalName,
			ExposedName:    row.ExposedName,
			APIKeyID:       stringValue(row.APIKeyID),
			APIKeyName:     row.APIKeyName,
			CalledAt:       row.CalledAt,
			LatencyMS:      row.LatencyMS,
			Success:        row.Success,
			Status:         normalizeCallStatus(row.Status, row.Success),
			RequestArgs:    json.RawMessage(row.RequestArgs),
			ResponseResult: json.RawMessage(row.ResponseResult),
			ErrorMessage:   row.ErrorMessage,
			FailureDetail:  json.RawMessage(row.FailureDetail),
			Mode:           row.Mode,
			Source:         row.Source,
		}
		result = append(result, item)
	}
	return result
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
// 仅信任 Go 的 time.LoadLocation 校验结果，避免拼接任意字符串进 SQL。返回的
// 名称可直接作为 AT TIME ZONE 参数传入 PostgreSQL（PG 与 Go 共用 IANA 命名）。
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

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
