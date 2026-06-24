package stats

import (
	"context"
	"errors"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// RecentRecordWriter 是 Redis 最近调用记录写入的窄接口。
type RecentRecordWriter interface {
	AppendRecords(ctx context.Context, records []store.CallStatRecord) error
}

// RecordEnricher 为调用事件补齐名称快照。
type RecordEnricher interface {
	EnrichRecords(ctx context.Context, records []store.CallStatRecord) ([]store.CallStatRecord, error)
}

// CompositeWriter 将一批调用事件同时写入 Redis 最近记录与 PostgreSQL 每日聚合。
type CompositeWriter struct {
	recent    RecentRecordWriter
	aggregate StatWriter
	enricher  RecordEnricher
}

// NewCompositeWriter 构造统计批量写入器。任一依赖可为空；为空时跳过对应写入。
func NewCompositeWriter(aggregate StatWriter, recent RecentRecordWriter) *CompositeWriter {
	w := &CompositeWriter{recent: recent, aggregate: aggregate}
	if enricher, ok := aggregate.(RecordEnricher); ok {
		w.enricher = enricher
	}
	return w
}

// Insert 写 Redis 最近记录并 upsert PostgreSQL 每日聚合。两侧互不短路，最终合并返回错误。
func (w *CompositeWriter) Insert(ctx context.Context, records []store.CallStatRecord) error {
	if w == nil || len(records) == 0 {
		return nil
	}
	writeRecords := records
	if w.enricher != nil {
		if enriched, err := w.enricher.EnrichRecords(ctx, records); err == nil && len(enriched) == len(records) {
			writeRecords = enriched
		}
	}
	var recentErr error
	if w.recent != nil {
		recentErr = w.recent.AppendRecords(ctx, writeRecords)
	}
	var aggregateErr error
	if w.aggregate != nil {
		aggregateErr = w.aggregate.Insert(ctx, writeRecords)
	}
	return errors.Join(recentErr, aggregateErr)
}

// CombinedQuerier 将 PostgreSQL 聚合查询与 Redis 最近记录查询组合为 QueryService 依赖。
type CombinedQuerier struct {
	aggregate aggregateQuerier
	records   recordQuerier
}

type aggregateQuerier interface {
	CountByUpstream(ctx context.Context, start, end time.Time) ([]store.DimensionCount, error)
	CountByAPIKey(ctx context.Context, start, end time.Time) ([]store.DimensionCount, error)
	TopTools(ctx context.Context, start, end time.Time, limit int) ([]store.ToolRank, error)
	Summary(ctx context.Context, start, end time.Time) (store.StatsSummary, error)
	Daily(ctx context.Context, start, end time.Time, tz string) ([]store.DailyCount, error)
	TopToolErrors(ctx context.Context, start, end time.Time, limit int) ([]store.ToolErrorRank, error)
	APIKeyUsageProfile(ctx context.Context, apiKeyID string, start, end time.Time, limit int) (store.APIKeyUsageProfile, error)
}

type recordQuerier interface {
	ListRecords(ctx context.Context, limit int, afterID int64, afterAt time.Time) ([]store.CallRecordView, error)
	HealthRecords(ctx context.Context, since, until time.Time, limit int) ([]store.CallRecordView, error)
	GetRecord(ctx context.Context, id int64) (store.CallRecordView, error)
	ClearRecordsBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// NewCombinedQuerier 构造组合查询器。
func NewCombinedQuerier(aggregate aggregateQuerier, records recordQuerier) *CombinedQuerier {
	return &CombinedQuerier{aggregate: aggregate, records: records}
}

func (q *CombinedQuerier) CountByUpstream(ctx context.Context, start, end time.Time) ([]store.DimensionCount, error) {
	return q.aggregate.CountByUpstream(ctx, start, end)
}

func (q *CombinedQuerier) CountByAPIKey(ctx context.Context, start, end time.Time) ([]store.DimensionCount, error) {
	return q.aggregate.CountByAPIKey(ctx, start, end)
}

func (q *CombinedQuerier) TopTools(ctx context.Context, start, end time.Time, limit int) ([]store.ToolRank, error) {
	return q.aggregate.TopTools(ctx, start, end, limit)
}

func (q *CombinedQuerier) Summary(ctx context.Context, start, end time.Time) (store.StatsSummary, error) {
	return q.aggregate.Summary(ctx, start, end)
}

func (q *CombinedQuerier) Daily(ctx context.Context, start, end time.Time, tz string) ([]store.DailyCount, error) {
	return q.aggregate.Daily(ctx, start, end, tz)
}

func (q *CombinedQuerier) TopToolErrors(ctx context.Context, start, end time.Time, limit int) ([]store.ToolErrorRank, error) {
	return q.aggregate.TopToolErrors(ctx, start, end, limit)
}

func (q *CombinedQuerier) APIKeyUsageProfile(ctx context.Context, apiKeyID string, start, end time.Time, limit int) (store.APIKeyUsageProfile, error) {
	return q.aggregate.APIKeyUsageProfile(ctx, apiKeyID, start, end, limit)
}

func (q *CombinedQuerier) ListRecords(ctx context.Context, limit int, afterID int64, afterAt time.Time) ([]store.CallRecordView, error) {
	return q.records.ListRecords(ctx, limit, afterID, afterAt)
}

func (q *CombinedQuerier) HealthRecords(ctx context.Context, since, until time.Time, limit int) ([]store.CallRecordView, error) {
	return q.records.HealthRecords(ctx, since, until, limit)
}

func (q *CombinedQuerier) GetRecord(ctx context.Context, id int64) (store.CallRecordView, error) {
	return q.records.GetRecord(ctx, id)
}

func (q *CombinedQuerier) ClearRecordsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	return q.records.ClearRecordsBefore(ctx, cutoff)
}
