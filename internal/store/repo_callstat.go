package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

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
}

// DimensionCount 为按某一维度（上游 MCP 或 API Key）聚合的调用条数（Req 16.2、16.4）。
type DimensionCount struct {
	// ID 为该维度的标识（上游 MCP 标识或 API Key 标识）；空串表示该维度为 NULL。
	ID string
	// Count 为该维度在时间范围内的调用条数（含成功与失败）。
	Count int64
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
	pool *pgxpool.Pool
}

// NewCallStatRepo 构造调用统计仓储。
func NewCallStatRepo(pool *pgxpool.Pool) *CallStatRepo {
	return &CallStatRepo{pool: pool}
}

// Insert 批量写入调用统计记录，供后台 worker 将异步缓冲批量落库使用（Req 16.1、16.8）。
//
// 采用 pgx CopyFrom 高效批量插入；空切片为无操作。id 由 BIGSERIAL 自增生成。
func (r *CallStatRepo) Insert(ctx context.Context, records []CallStatRecord) error {
	if len(records) == 0 {
		return nil
	}
	columns := []string{
		"upstream_id", "original_name", "exposed_name",
		"api_key_id", "called_at", "latency_ms", "success",
		"status", "request_args", "response_result", "error_message", "failure_detail",
	}
	rows := make([][]any, 0, len(records))
	for _, rec := range records {
		upstreamID, err := parseUUID(rec.UpstreamID)
		if err != nil {
			return err
		}
		apiKeyID, err := parseUUID(rec.APIKeyID)
		if err != nil {
			return err
		}
		status := normalizeCallStatus(rec.Status, rec.Success)
		rows = append(rows, []any{
			upstreamID,
			rec.OriginalName,
			nullableText(rec.ExposedName),
			apiKeyID,
			rec.CalledAt,
			int32(rec.LatencyMS),
			rec.Success,
			status,
			nullableJSON(rec.RequestArgs),
			nullableJSON(rec.ResponseResult),
			nullableText(rec.ErrorMessage),
			nullableJSON(rec.FailureDetail),
		})
	}

	_, err := r.pool.CopyFrom(ctx, pgx.Identifier{"call_stat"}, columns, pgx.CopyFromRows(rows))
	if err != nil {
		return err
	}
	return nil
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
			coalesce(up.name, ''),
			cs.original_name,
			coalesce(cs.exposed_name, ''),
			cs.api_key_id,
			coalesce(ak.name, ''),
			cs.called_at,
			cs.latency_ms,
			cs.success,
			coalesce(cs.status, CASE WHEN cs.success THEN 'success' WHEN coalesce(cs.error_message, '') <> '' THEN 'failed' ELSE 'upstream_error' END),
			coalesce(cs.request_args, 'null'::jsonb),
			coalesce(cs.response_result, 'null'::jsonb),
			coalesce(cs.error_message, ''),
			coalesce(cs.failure_detail, 'null'::jsonb)
		FROM call_stat cs
		LEFT JOIN upstream_mcp up ON up.id = cs.upstream_id
		LEFT JOIN api_key ak ON ak.id = cs.api_key_id
		WHERE (
			($1::bigint = 0 AND $2::timestamptz IS NULL)
			OR cs.id > $1
			OR ($2::timestamptz IS NOT NULL AND (cs.called_at > $2 OR (cs.called_at = $2 AND cs.id > $1)))
		)
		ORDER BY cs.called_at DESC, cs.id DESC
		LIMIT $3`
	var afterAtParam any
	if !afterAt.IsZero() {
		afterAtParam = afterAt
	}
	rows, err := r.pool.Query(ctx, q, afterID, afterAtParam, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCallRecordViews(rows)
}

// GetRecord 按 ID 读取单条调用记录详情。
func (r *CallStatRepo) GetRecord(ctx context.Context, id int64) (CallRecordView, error) {
	const q = `
		SELECT
			cs.id,
			cs.upstream_id,
			coalesce(up.name, ''),
			cs.original_name,
			coalesce(cs.exposed_name, ''),
			cs.api_key_id,
			coalesce(ak.name, ''),
			cs.called_at,
			cs.latency_ms,
			cs.success,
			coalesce(cs.status, CASE WHEN cs.success THEN 'success' WHEN coalesce(cs.error_message, '') <> '' THEN 'failed' ELSE 'upstream_error' END),
			coalesce(cs.request_args, 'null'::jsonb),
			coalesce(cs.response_result, 'null'::jsonb),
			coalesce(cs.error_message, ''),
			coalesce(cs.failure_detail, 'null'::jsonb)
		FROM call_stat cs
		LEFT JOIN upstream_mcp up ON up.id = cs.upstream_id
		LEFT JOIN api_key ak ON ak.id = cs.api_key_id
		WHERE cs.id = $1
		ORDER BY cs.called_at DESC
		LIMIT 1`
	rows, err := r.pool.Query(ctx, q, id)
	if err != nil {
		return CallRecordView{}, err
	}
	defer rows.Close()
	records, err := scanCallRecordViews(rows)
	if err != nil {
		return CallRecordView{}, err
	}
	if len(records) == 0 {
		return CallRecordView{}, domain.NewError(domain.CodeNotFound, "调用记录不存在")
	}
	return records[0], nil
}

// CountByUpstream 统计 [start, end] 闭区间内各上游 MCP 的调用条数（含成功失败）（Req 16.2、16.5）。
//   - start 晚于 end 返回 CodeValidation（Req 16.7）。
//   - 无记录返回空切片而非错误（Req 16.6）。
func (r *CallStatRepo) CountByUpstream(ctx context.Context, start, end time.Time) ([]DimensionCount, error) {
	if err := validateRange(start, end); err != nil {
		return nil, err
	}
	const q = `
		SELECT upstream_id, count(*)
		FROM call_stat
		WHERE called_at >= $1 AND called_at <= $2
		GROUP BY upstream_id
		ORDER BY count(*) DESC`
	return r.queryDimensionCounts(ctx, q, start, end)
}

// CountByAPIKey 统计 [start, end] 闭区间内各 API Key 的调用条数（含成功失败）（Req 16.4、16.5）。
//   - start 晚于 end 返回 CodeValidation（Req 16.7）。
//   - 无记录返回空切片而非错误（Req 16.6）。
func (r *CallStatRepo) CountByAPIKey(ctx context.Context, start, end time.Time) ([]DimensionCount, error) {
	if err := validateRange(start, end); err != nil {
		return nil, err
	}
	const q = `
		SELECT api_key_id, count(*)
		FROM call_stat
		WHERE called_at >= $1 AND called_at <= $2
		GROUP BY api_key_id
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
		SELECT upstream_id, original_name, count(*) AS c
		FROM call_stat
		WHERE called_at >= $1 AND called_at <= $2
		GROUP BY upstream_id, original_name
		ORDER BY c DESC, original_name ASC
		LIMIT $3`
	rows, err := r.pool.Query(ctx, q, start, end, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]ToolRank, 0)
	for rows.Next() {
		var (
			upstreamID pgtype.UUID
			original   string
			count      int64
		)
		if err := rows.Scan(&upstreamID, &original, &count); err != nil {
			return nil, err
		}
		result = append(result, ToolRank{
			UpstreamID:   uuidString(upstreamID),
			OriginalName: original,
			Count:        count,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
		WHERE called_at >= $1 AND called_at <= $2`

	var out StatsSummary
	err := r.pool.QueryRow(ctx, q, start, end).Scan(
		&out.TotalCalls,
		&out.SuccessCalls,
		&out.FailureCalls,
		&out.ActiveUpstreams,
		&out.ActiveAPIKeys,
		&out.UniqueTools,
		&out.AvgLatencyMS,
		&out.P95LatencyMS,
	)
	if err != nil {
		return StatsSummary{}, err
	}
	return out, nil
}

// Daily 返回 [start, end] 闭区间内按 UTC 自然日聚合的调用趋势。
func (r *CallStatRepo) Daily(ctx context.Context, start, end time.Time) ([]DailyCount, error) {
	if err := validateRange(start, end); err != nil {
		return nil, err
	}
	const q = `
		SELECT
			(date_trunc('day', called_at AT TIME ZONE 'UTC'))::date AS day,
			count(*) AS total_calls,
			count(*) FILTER (WHERE success) AS success_calls,
			count(*) FILTER (WHERE NOT success) AS failure_calls,
			coalesce(avg(latency_ms), 0)::float8 AS avg_latency_ms
		FROM call_stat
		WHERE called_at >= $1 AND called_at <= $2
		GROUP BY day
		ORDER BY day ASC`
	rows, err := r.pool.Query(ctx, q, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]DailyCount, 0)
	for rows.Next() {
		var item DailyCount
		if err := rows.Scan(&item.Day, &item.TotalCalls, &item.SuccessCalls, &item.FailureCalls, &item.AvgLatencyMS); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
		WHERE called_at >= $1 AND called_at <= $2
		GROUP BY upstream_id, original_name
		HAVING count(*) FILTER (WHERE NOT success) > 0
		ORDER BY failure_calls DESC, total_calls DESC, original_name ASC
		LIMIT $3`
	rows, err := r.pool.Query(ctx, q, start, end, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]ToolErrorRank, 0)
	for rows.Next() {
		var (
			upstreamID pgtype.UUID
			item       ToolErrorRank
		)
		if err := rows.Scan(&upstreamID, &item.OriginalName, &item.TotalCalls, &item.FailureCalls, &item.LastFailedAt, &item.AvgLatencyMS); err != nil {
			return nil, err
		}
		item.UpstreamID = uuidString(upstreamID)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
			stmt := fmt.Sprintf(`DROP TABLE IF EXISTS %s`, pgx.Identifier{name}.Sanitize())
			if _, derr := r.pool.Exec(ctx, stmt); derr != nil {
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
	const q = `DELETE FROM call_stat WHERE called_at < $1`
	tag, err := r.pool.Exec(ctx, q, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
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
		pgx.Identifier{name}.Sanitize(),
		begin.Format("2006-01-02 15:04:05-07"),
		end.Format("2006-01-02 15:04:05-07"),
	)
	if _, err := r.pool.Exec(ctx, stmt); err != nil {
		return err
	}
	return nil
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
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if _, ok := parsePartitionMonth(name); ok {
			names = append(names, name)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
	rows, err := r.pool.Query(ctx, q, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]DimensionCount, 0)
	for rows.Next() {
		var (
			id    pgtype.UUID
			count int64
		)
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		result = append(result, DimensionCount{ID: uuidString(id), Count: count})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func scanCallRecordViews(rows pgx.Rows) ([]CallRecordView, error) {
	result := make([]CallRecordView, 0)
	for rows.Next() {
		var (
			upstreamID pgtype.UUID
			apiKeyID   pgtype.UUID
			request    []byte
			response   []byte
			failure    []byte
			item       CallRecordView
		)
		if err := rows.Scan(
			&item.ID,
			&upstreamID,
			&item.UpstreamName,
			&item.OriginalName,
			&item.ExposedName,
			&apiKeyID,
			&item.APIKeyName,
			&item.CalledAt,
			&item.LatencyMS,
			&item.Success,
			&item.Status,
			&request,
			&response,
			&item.ErrorMessage,
			&failure,
		); err != nil {
			return nil, err
		}
		item.UpstreamID = uuidString(upstreamID)
		item.APIKeyID = uuidString(apiKeyID)
		item.Status = normalizeCallStatus(item.Status, item.Success)
		item.RequestArgs = json.RawMessage(request)
		item.ResponseResult = json.RawMessage(response)
		item.FailureDetail = json.RawMessage(failure)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
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

// validateRange checks that the statistics range start is not after end.
func validateRange(start, end time.Time) error {
	if start.After(end) {
		return domain.NewValidationError("统计时间范围无效：开始时间晚于结束时间", map[string]string{
			"start": "开始时间不得晚于结束时间",
		})
	}
	return nil
}
