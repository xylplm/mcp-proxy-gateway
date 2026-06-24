package stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

const (
	recentRecordsZSetKey  = "mpg:stats:recent:zset"
	recentRecordKeyPrefix = "mpg:stats:recent:record:"
	recentRecordSeqKey    = "mpg:stats:recent:seq"

	defaultRecentRecordsTTL = 24 * time.Hour
	defaultRecentRecordsMax = 50000

	maxRecentRequestJSONBytes   = 4096
	maxRecentResponseJSONBytes  = 4096
	maxRecentFailureJSONBytes   = 4096
	maxRecentErrorMessageRunes  = 1000
	defaultRecentRecordsListCap = 100
	defaultRecentHealthCap      = 5000
)

// RedisRecentRecordStore 保存最近 24 小时调用记录，供调用记录页面查询。
//
// PostgreSQL 不再保存调用明细；这里用一个按 called_at 毫秒排序的 ZSET 维护最近记录索引，
// 每条记录详情单独 SETEX，TTL 与最大条数双重约束避免 Redis 无限增长。
type RedisRecentRecordStore struct {
	rdb        *redis.Client
	ttl        time.Duration
	maxRecords int64
}

var _ interface {
	AppendRecords(context.Context, []store.CallStatRecord) error
	ListRecords(context.Context, int, int64, time.Time) ([]store.CallRecordView, error)
	HealthRecords(context.Context, time.Time, time.Time, int) ([]store.CallRecordView, error)
	GetRecord(context.Context, int64) (store.CallRecordView, error)
	ClearRecordsBefore(context.Context, time.Time) (int64, error)
} = (*RedisRecentRecordStore)(nil)

// NewRedisRecentRecordStore 构造 Redis 最近调用记录存储。
func NewRedisRecentRecordStore(rdb *redis.Client) *RedisRecentRecordStore {
	return &RedisRecentRecordStore{rdb: rdb, ttl: defaultRecentRecordsTTL, maxRecords: defaultRecentRecordsMax}
}

// AppendRecords 写入最近调用记录，并执行 TTL/最大条数裁剪。
func (s *RedisRecentRecordStore) AppendRecords(ctx context.Context, records []store.CallStatRecord) error {
	if s == nil || s.rdb == nil || len(records) == 0 {
		return nil
	}
	seqEnd, err := s.rdb.IncrBy(ctx, recentRecordSeqKey, int64(len(records))).Result()
	if err != nil {
		return err
	}
	seqStart := seqEnd - int64(len(records)) + 1
	pipe := s.rdb.Pipeline()
	now := time.Now().UTC()
	for i, rec := range records {
		id := seqStart + int64(i)
		view := recentRecordView(id, rec, now)
		raw, err := json.Marshal(view)
		if err != nil {
			return err
		}
		member := recentMember(id)
		pipe.Set(ctx, recentRecordKey(id), raw, s.ttl)
		pipe.ZAdd(ctx, recentRecordsZSetKey, redis.Z{Score: float64(view.CalledAt.UTC().UnixMilli()), Member: member})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	return s.trim(ctx, now)
}

// ListRecords 按调用时间倒序分页返回调用记录。afterAt/afterID 用于前端增量拉取最新记录。
func (s *RedisRecentRecordStore) ListRecords(ctx context.Context, limit int, afterID int64, afterAt time.Time) ([]store.CallRecordView, error) {
	if s == nil || s.rdb == nil {
		return []store.CallRecordView{}, nil
	}
	limit = normalizeRecordLimit(limit)
	zs, err := s.recentCandidates(ctx, limit, afterID, afterAt)
	if err != nil {
		return nil, err
	}
	return s.loadViews(ctx, zs)
}

// HealthRecords 返回指定时间窗口内的最近调用记录，供健康看板聚合使用。
func (s *RedisRecentRecordStore) HealthRecords(ctx context.Context, since, until time.Time, limit int) ([]store.CallRecordView, error) {
	if s == nil || s.rdb == nil {
		return []store.CallRecordView{}, nil
	}
	if until.IsZero() {
		until = time.Now().UTC()
	}
	if since.IsZero() {
		since = until.Add(-time.Hour)
	}
	limit = normalizeHealthRecordLimit(limit)
	zs, err := s.rdb.ZRevRangeByScoreWithScores(ctx, recentRecordsZSetKey, &redis.ZRangeBy{
		Min:   strconv.FormatInt(since.UTC().UnixMilli(), 10),
		Max:   strconv.FormatInt(until.UTC().UnixMilli(), 10),
		Count: int64(limit),
	}).Result()
	if err != nil {
		return nil, err
	}
	return s.loadViews(ctx, zs)
}

// GetRecord 按 ID 读取单条最近调用记录详情。
func (s *RedisRecentRecordStore) GetRecord(ctx context.Context, id int64) (store.CallRecordView, error) {
	if s == nil || s.rdb == nil || id <= 0 {
		return store.CallRecordView{}, domain.NewError(domain.CodeNotFound, "调用记录不存在或已过期")
	}
	raw, err := s.rdb.Get(ctx, recentRecordKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return store.CallRecordView{}, domain.NewError(domain.CodeNotFound, "调用记录不存在或已过期")
	}
	if err != nil {
		return store.CallRecordView{}, err
	}
	var view store.CallRecordView
	if err := json.Unmarshal(raw, &view); err != nil {
		return store.CallRecordView{}, err
	}
	if view.ID == 0 {
		view.ID = id
	}
	view.Status = storeCallStatus(view.Status, view.Success)
	return view, nil
}

// ClearRecordsBefore 删除 cutoff 时刻及以前的最近调用记录，返回被移出索引的数量。
func (s *RedisRecentRecordStore) ClearRecordsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.rdb == nil {
		return 0, nil
	}
	var members []string
	var err error
	if cutoff.IsZero() {
		members, err = s.rdb.ZRange(ctx, recentRecordsZSetKey, 0, -1).Result()
	} else {
		members, err = s.rdb.ZRangeByScore(ctx, recentRecordsZSetKey, &redis.ZRangeBy{
			Min: "-inf",
			Max: strconv.FormatInt(cutoff.UTC().UnixMilli(), 10),
		}).Result()
	}
	if err != nil {
		return 0, err
	}
	if len(members) == 0 {
		return 0, nil
	}
	if err := s.deleteMembers(ctx, members); err != nil {
		return 0, err
	}
	return int64(len(members)), nil
}

func (s *RedisRecentRecordStore) trim(ctx context.Context, now time.Time) error {
	cutoff := now.Add(-s.ttl).UTC().UnixMilli()
	old, err := s.rdb.ZRangeByScore(ctx, recentRecordsZSetKey, &redis.ZRangeBy{
		Min: "-inf",
		Max: strconv.FormatInt(cutoff, 10),
	}).Result()
	if err != nil {
		return err
	}
	if len(old) > 0 {
		if err := s.deleteMembers(ctx, old); err != nil {
			return err
		}
	}
	if s.maxRecords <= 0 {
		return nil
	}
	count, err := s.rdb.ZCard(ctx, recentRecordsZSetKey).Result()
	if err != nil {
		return err
	}
	extra := count - s.maxRecords
	if extra <= 0 {
		return nil
	}
	members, err := s.rdb.ZRange(ctx, recentRecordsZSetKey, 0, extra-1).Result()
	if err != nil {
		return err
	}
	return s.deleteMembers(ctx, members)
}

func (s *RedisRecentRecordStore) deleteMembers(ctx context.Context, members []string) error {
	if len(members) == 0 {
		return nil
	}
	pipe := s.rdb.Pipeline()
	keys := make([]string, 0, len(members))
	for _, member := range members {
		id, ok := parseRecentMember(member)
		if !ok {
			continue
		}
		keys = append(keys, recentRecordKey(id))
	}
	if len(keys) > 0 {
		pipe.Del(ctx, keys...)
	}
	values := make([]any, 0, len(members))
	for _, member := range members {
		values = append(values, member)
	}
	pipe.ZRem(ctx, recentRecordsZSetKey, values...)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *RedisRecentRecordStore) recentCandidates(ctx context.Context, limit int, afterID int64, afterAt time.Time) ([]redis.Z, error) {
	if afterID <= 0 && afterAt.IsZero() {
		return s.rdb.ZRevRangeWithScores(ctx, recentRecordsZSetKey, 0, int64(limit)-1).Result()
	}
	if afterAt.IsZero() {
		zs, err := s.rdb.ZRevRangeWithScores(ctx, recentRecordsZSetKey, 0, s.maxRecords-1).Result()
		if err != nil {
			return nil, err
		}
		return filterRecentCandidates(zs, limit, afterID, 0), nil
	}
	afterScore := afterAt.UTC().UnixMilli()
	zs, err := s.rdb.ZRevRangeByScoreWithScores(ctx, recentRecordsZSetKey, &redis.ZRangeBy{
		Min:   fmt.Sprintf("(%d", afterScore),
		Max:   "+inf",
		Count: int64(limit),
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(zs) >= limit {
		return zs, nil
	}
	equal, err := s.rdb.ZRevRangeByScoreWithScores(ctx, recentRecordsZSetKey, &redis.ZRangeBy{
		Min: strconv.FormatInt(afterScore, 10),
		Max: strconv.FormatInt(afterScore, 10),
	}).Result()
	if err != nil {
		return nil, err
	}
	zs = append(zs, filterRecentCandidates(equal, limit-len(zs), afterID, afterScore)...)
	return zs, nil
}

func (s *RedisRecentRecordStore) loadViews(ctx context.Context, zs []redis.Z) ([]store.CallRecordView, error) {
	if len(zs) == 0 {
		return []store.CallRecordView{}, nil
	}
	keys := make([]string, 0, len(zs))
	members := make([]string, 0, len(zs))
	for _, z := range zs {
		member, ok := z.Member.(string)
		if !ok {
			continue
		}
		id, ok := parseRecentMember(member)
		if !ok {
			continue
		}
		members = append(members, member)
		keys = append(keys, recentRecordKey(id))
	}
	if len(keys) == 0 {
		return []store.CallRecordView{}, nil
	}
	vals, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	result := make([]store.CallRecordView, 0, len(vals))
	stale := make([]string, 0)
	for i, val := range vals {
		if val == nil {
			stale = append(stale, members[i])
			continue
		}
		var raw []byte
		switch v := val.(type) {
		case string:
			raw = []byte(v)
		case []byte:
			raw = v
		default:
			continue
		}
		var view store.CallRecordView
		if err := json.Unmarshal(raw, &view); err != nil {
			stale = append(stale, members[i])
			continue
		}
		view.Status = storeCallStatus(view.Status, view.Success)
		result = append(result, view)
	}
	if len(stale) > 0 {
		_ = s.deleteMembers(ctx, stale)
	}
	return result, nil
}

func filterRecentCandidates(zs []redis.Z, limit int, afterID int64, afterScore int64) []redis.Z {
	if limit <= 0 {
		return nil
	}
	out := make([]redis.Z, 0, limit)
	for _, z := range zs {
		member, ok := z.Member.(string)
		if !ok {
			continue
		}
		id, ok := parseRecentMember(member)
		if !ok {
			continue
		}
		if id <= afterID {
			continue
		}
		if afterScore > 0 && int64(z.Score) != afterScore {
			continue
		}
		out = append(out, z)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func sanitizeRecordPayloads(rec store.CallStatRecord) store.CallStatRecord {
	rec.RequestArgs = truncateJSONRaw(rec.RequestArgs, maxRecentRequestJSONBytes)
	rec.ResponseResult = truncateJSONRaw(rec.ResponseResult, maxRecentResponseJSONBytes)
	rec.FailureDetail = truncateJSONRaw(rec.FailureDetail, maxRecentFailureJSONBytes)
	rec.ErrorMessage = truncateStringRunes(rec.ErrorMessage, maxRecentErrorMessageRunes)
	return rec
}

func recentRecordView(id int64, rec store.CallStatRecord, now time.Time) store.CallRecordView {
	calledAt := rec.CalledAt.UTC()
	if calledAt.IsZero() {
		calledAt = now
	}
	status := storeCallStatus(rec.Status, rec.Success)
	return store.CallRecordView{
		ID:             id,
		UpstreamID:     rec.UpstreamID,
		UpstreamName:   rec.UpstreamName,
		OriginalName:   rec.OriginalName,
		ExposedName:    rec.ExposedName,
		APIKeyID:       rec.APIKeyID,
		APIKeyName:     rec.APIKeyName,
		CalledAt:       calledAt,
		LatencyMS:      maxInt(rec.LatencyMS, 0),
		Success:        status == store.CallStatusSuccess,
		Status:         status,
		RequestArgs:    truncateJSONRaw(rec.RequestArgs, maxRecentRequestJSONBytes),
		ResponseResult: truncateJSONRaw(rec.ResponseResult, maxRecentResponseJSONBytes),
		ErrorMessage:   truncateStringRunes(rec.ErrorMessage, maxRecentErrorMessageRunes),
		FailureDetail:  truncateJSONRaw(rec.FailureDetail, maxRecentFailureJSONBytes),
		Mode:           normalizeMode(rec.Mode),
		Source:         normalizeSource(rec.Source),
	}
}

func normalizeRecordLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > defaultRecentRecordsListCap {
		return defaultRecentRecordsListCap
	}
	return limit
}

func normalizeHealthRecordLimit(limit int) int {
	if limit <= 0 || limit > defaultRecentHealthCap {
		return defaultRecentHealthCap
	}
	return limit
}

func recentMember(id int64) string {
	return fmt.Sprintf("%020d", id)
}

func parseRecentMember(member string) (int64, bool) {
	id, err := strconv.ParseInt(member, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func recentRecordKey(id int64) string {
	return recentRecordKeyPrefix + strconv.FormatInt(id, 10)
}

func truncateJSONRaw(raw json.RawMessage, maxBytes int) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	if (maxBytes <= 0 || len(raw) <= maxBytes) && json.Valid(raw) {
		return raw
	}
	previewBytes := raw
	truncated := false
	if maxBytes > 0 && len(previewBytes) > maxBytes {
		previewBytes = previewBytes[:maxBytes]
		truncated = true
	}
	invalidJSON := false
	if !truncated {
		invalidJSON = !json.Valid(raw)
	}
	out, err := json.Marshal(map[string]any{
		"truncated":     truncated,
		"invalidJSON":   invalidJSON,
		"originalBytes": len(raw),
		"preview":       string(previewBytes),
	})
	if err != nil {
		return json.RawMessage(`{"truncated":true}`)
	}
	return out
}

func truncateStringRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

func normalizeMode(mode string) string {
	if mode == "smart" || mode == "full" {
		return mode
	}
	return "full"
}

func normalizeSource(source string) string {
	if source == "api" || source == "xiaozhi" {
		return source
	}
	return "api"
}

func storeCallStatus(status string, success bool) string {
	switch status {
	case store.CallStatusSuccess, store.CallStatusUpstreamError, store.CallStatusFailed:
		return status
	}
	if success {
		return store.CallStatusSuccess
	}
	return store.CallStatusFailed
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
