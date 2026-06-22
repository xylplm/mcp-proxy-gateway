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
	Incr(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) error
}

type redisQuotaCounter struct {
	rdb *redis.Client
}

// NewRedisQuotaCounter 将 Redis 客户端适配为上游额度计数器。
func NewRedisQuotaCounter(rdb *redis.Client) QuotaCounter {
	return redisQuotaCounter{rdb: rdb}
}

func (c redisQuotaCounter) Incr(ctx context.Context, key string) (int64, error) {
	return c.rdb.Incr(ctx, key).Result()
}

func (c redisQuotaCounter) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return c.rdb.Expire(ctx, key, ttl).Err()
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
	for _, w := range windows {
		if w.limit <= 0 {
			continue
		}
		key := fmt.Sprintf("%s%s:%s:%d", quotaKeyPrefix, upstreamID, w.name, w.start.Unix())
		n, err := q.counter.Incr(ctx, key)
		if err != nil {
			q.logger.Warn("上游额度计数失败，降级放行", "upstreamID", upstreamID, "window", w.name, "error", err)
			return true, ""
		}
		if n == 1 {
			if err := q.counter.Expire(ctx, key, w.ttl); err != nil {
				q.logger.Warn("上游额度计数 TTL 设置失败", "upstreamID", upstreamID, "window", w.name, "error", err)
			}
		}
		if n > int64(w.limit) {
			return false, fmt.Sprintf("%s额度已用尽，预计 %s 重置", w.label, w.reset.Format(time.RFC3339))
		}
	}
	return true, ""
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
