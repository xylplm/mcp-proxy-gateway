package aggregation

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

const quotaKeyPrefix = "mpg:upstream_quota:"

// QuotaCounter 是上游调用额度判定依赖的最小计数能力。
type QuotaCounter interface {
	// Reserve 原子检查并占用所有额度窗口；返回 rejectedIndex 为 1-based 窗口序号。
	Reserve(ctx context.Context, items []QuotaReservation) (bool, int, error)
}

// QuotaReservation 表示一次上游调用需要同时占用的单个额度窗口。
type QuotaReservation struct {
	Key   string
	Limit int
	TTL   time.Duration
}

type redisQuotaCounter struct {
	rdb *redis.Client
}

var quotaReserveScript = redis.NewScript(`
if #KEYS == 0 then
	return {1, 0}
end
for i = 1, #KEYS do
	local limit = tonumber(ARGV[(i - 1) * 2 + 1])
	local current = tonumber(redis.call("GET", KEYS[i]) or "0") or 0
	if current >= limit then
		return {0, i}
	end
end
for i = 1, #KEYS do
	local ttl = tonumber(ARGV[(i - 1) * 2 + 2])
	local count = redis.call("INCR", KEYS[i])
	if count == 1 and ttl > 0 then
		redis.call("PEXPIRE", KEYS[i], ttl)
	end
end
return {1, 0}
`)

// NewRedisQuotaCounter 将 Redis 客户端适配为上游额度计数器。
func NewRedisQuotaCounter(rdb *redis.Client) QuotaCounter {
	return redisQuotaCounter{rdb: rdb}
}

func (c redisQuotaCounter) Reserve(ctx context.Context, items []QuotaReservation) (bool, int, error) {
	keys := make([]string, 0, len(items))
	args := make([]any, 0, len(items)*2)
	for _, item := range items {
		keys = append(keys, item.Key)
		args = append(args, item.Limit, item.TTL.Milliseconds())
	}
	result, err := quotaReserveScript.Run(ctx, c.rdb, keys, args...).Int64Slice()
	if err != nil {
		return false, 0, err
	}
	if len(result) < 2 {
		return false, 0, fmt.Errorf("上游额度计数脚本返回值非法")
	}
	return result[0] == 1, int(result[1]), nil
}

// QuotaManager 按上游维度执行固定窗口频率/周期额度占用。
type QuotaManager struct {
	counter QuotaCounter
	logger  *slog.Logger
	now     func() time.Time
}

func NewQuotaManager(counter QuotaCounter, logger *slog.Logger) *QuotaManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &QuotaManager{counter: counter, logger: logger, now: time.Now}
}

func (q *QuotaManager) Allow(ctx context.Context, upstreamID string, limits domain.UpstreamRateLimits) (bool, string) {
	if q == nil || q.counter == nil || !limits.Enabled {
		return true, ""
	}
	now := q.now()
	windows := quotaWindows(now, limits)
	reservations := make([]QuotaReservation, 0, len(windows))
	for _, w := range windows {
		reservations = append(reservations, QuotaReservation{
			Key:   fmt.Sprintf("%s%s:%s:%d", quotaKeyPrefix, upstreamID, w.name, w.start.Unix()),
			Limit: w.limit,
			TTL:   w.ttl,
		})
	}
	if len(reservations) == 0 {
		return true, ""
	}
	allowed, rejectedIndex, err := q.counter.Reserve(ctx, reservations)
	if err != nil {
		q.logger.Warn("上游额度计数失败，降级放行", "upstreamID", upstreamID, "error", err)
		return true, ""
	}
	if allowed {
		return true, ""
	}
	if rejectedIndex < 1 || rejectedIndex > len(windows) {
		rejectedIndex = 1
	}
	w := windows[rejectedIndex-1]
	return false, fmt.Sprintf("%s额度已用尽，预计 %s 重置", w.label, w.reset.Format(time.RFC3339))
}

type quotaWindow struct {
	name  string
	label string
	limit int
	start time.Time
	reset time.Time
	ttl   time.Duration
}

func quotaWindows(now time.Time, limits domain.UpstreamRateLimits) []quotaWindow {
	loc, err := time.LoadLocation(limits.Timezone)
	if err != nil || loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	windows := []quotaWindow{
		fixedWindow("second", "每秒", limits.PerSecond, local, time.Second),
		fixedWindow("minute", "每分钟", limits.PerMinute, local, time.Minute),
		fixedWindow("hour", "每小时", limits.PerHour, local, time.Hour),
		calendarDayWindow(local, limits.PerDay),
		calendarWeekWindow(local, limits.PerWeek),
		calendarMonthWindow(local, limits.PerMonth),
	}
	out := make([]quotaWindow, 0, len(windows))
	for _, w := range windows {
		if w.limit > 0 {
			out = append(out, w)
		}
	}
	return out
}

func fixedWindow(name, label string, limit int, now time.Time, size time.Duration) quotaWindow {
	start := now.Truncate(size)
	reset := start.Add(size)
	return quotaWindow{name: name, label: label, limit: limit, start: start, reset: reset, ttl: reset.Sub(now).Round(time.Second) + time.Second}
}

func calendarDayWindow(now time.Time, limit int) quotaWindow {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	reset := start.AddDate(0, 0, 1)
	return quotaWindow{name: "day", label: "每日", limit: limit, start: start, reset: reset, ttl: reset.Sub(now).Round(time.Second) + time.Second}
}

func calendarWeekWindow(now time.Time, limit int) quotaWindow {
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start := dayStart.AddDate(0, 0, -(weekday - 1))
	reset := start.AddDate(0, 0, 7)
	return quotaWindow{name: "week", label: "每周", limit: limit, start: start, reset: reset, ttl: reset.Sub(now).Round(time.Second) + time.Second}
}

func calendarMonthWindow(now time.Time, limit int) quotaWindow {
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	reset := start.AddDate(0, 1, 0)
	return quotaWindow{name: "month", label: "每月", limit: limit, start: start, reset: reset, ttl: reset.Sub(now).Round(time.Second) + time.Second}
}
