package apikey

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// rateLimitKeyPrefix 为限流计数器在 Redis 中的键前缀，完整键形如
// mpg:rl:{apiKeyID}:{windowStart}，遵循设计文档「Redis 缓存键设计」的 mpg: 分域约定（Req 21）。
const rateLimitKeyPrefix = "mpg:rl:"

// RateCounter 是限流逻辑依赖的计数能力窄接口（Req 21）。
//
// 仅声明原子检查并占用多个窗口的能力，使「速率窗口 + 日/月额度」在高并发下不会出现
// 前一窗口已扣减、后一窗口才发现超额的半成功状态。以接口而非具体 Redis 客户端依赖，
// 便于在单元测试中以内存 fake 替换，使核心限流判定可脱离真实 Redis 验证。
type RateCounter interface {
	// Reserve 原子检查并占用所有窗口；rejectedIndex 为 1-based 窗口序号。
	Reserve(ctx context.Context, items []RateReservation) (bool, int, error)
}

// RateReservation 表示一次请求需要占用的一个计数窗口。
type RateReservation struct {
	Key   string
	Limit int
	TTL   time.Duration
}

// redisRateCounter 以 go-redis 客户端实现 RateCounter，是限流计数的生产实现。
type redisRateCounter struct {
	rdb *redis.Client
}

// NewRedisRateCounter 将 go-redis 客户端适配为 RateCounter。
func NewRedisRateCounter(rdb *redis.Client) RateCounter {
	return redisRateCounter{rdb: rdb}
}

var rateReserveScript = redis.NewScript(`
if #KEYS == 0 then
	return {1, 0}
end
for i = 1, #KEYS do
	local limit = tonumber(ARGV[(i - 1) * 2 + 1])
	local raw = redis.call("GET", KEYS[i])
	local current = 0
	if raw then
		current = tonumber(raw)
		if current == nil then
			return redis.error_reply("rate counter is not an integer")
		end
	end
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

// Reserve 调用 Redis Lua 脚本原子检查并占用所有计数窗口。
func (c redisRateCounter) Reserve(ctx context.Context, items []RateReservation) (bool, int, error) {
	keys := make([]string, 0, len(items))
	args := make([]any, 0, len(items)*2)
	for _, item := range items {
		keys = append(keys, item.Key)
		args = append(args, item.Limit, item.TTL.Milliseconds())
	}
	result, err := rateReserveScript.Run(ctx, c.rdb, keys, args...).Int64Slice()
	if err != nil {
		return false, 0, err
	}
	if len(result) < 2 {
		return false, 0, fmt.Errorf("API Key 限流计数脚本返回值非法")
	}
	return result[0] == 1, int(result[1]), nil
}

// RateLimiter 实现按 API Key 的固定窗口限流（Req 21）。
//
// 算法（见设计文档「限流（按 API Key 固定计数窗口）」）：以 floor(now/window)*window
// 为窗口起点构造计数键，每次请求对该键 INCR；首次自增（计数为 1）时为键设置等于窗口
// 长度的 TTL，使窗口随时间自然滚动；当计数超过配置上限时拒绝（RATE_LIMITED）。
// 窗口切换后键名随窗口起点改变，旧计数到期清除，新窗口配额自动恢复（Req 21.3）。
type RateLimiter struct {
	// counter 为限流计数能力（生产环境为 Redis）。
	counter RateCounter
	// logger 用于记录计数后端异常；为空时回退到 slog.Default()。
	logger *slog.Logger
}

// NewRateLimiter 构造限流器。counter 为必需的计数能力依赖；logger 为空时回退到默认 logger。
func NewRateLimiter(counter RateCounter, logger *slog.Logger) *RateLimiter {
	if logger == nil {
		logger = slog.Default()
	}
	return &RateLimiter{counter: counter, logger: logger}
}

// windowStartUnix 计算给定时刻 now 在窗口长度 windowS（秒）下的窗口起点（Unix 秒）。
//
// 采用 floor(now/windowS)*windowS 对齐窗口边界，使同一窗口内的请求落入同一计数键。
func windowStartUnix(now time.Time, windowS int) int64 {
	sec := now.Unix()
	w := int64(windowS)
	return (sec / w) * w
}

// rateLimitKey 拼接某 API Key 在某窗口的计数键，形如 mpg:rl:{apiKeyID}:{windowStart}。
func rateLimitKey(apiKeyID string, windowStart int64) string {
	return fmt.Sprintf("%s%s:%d", rateLimitKeyPrefix, apiKeyID, windowStart)
}

func rateQuotaKey(apiKeyID, windowName string, windowStart int64) string {
	return fmt.Sprintf("%s%s:%s:%d", rateLimitKeyPrefix, apiKeyID, windowName, windowStart)
}

// Allow 判定携带某 API Key 的请求在给定时刻 now 是否应被受理（Req 21）。
//
// 决策顺序：
//   - 未配置速率上限（RateLimit 为 nil）或上限非正、窗口非正：视为不限流，直接放行（Req 21.4）。
//   - 否则按固定窗口计数：对当前窗口键 INCR，首次自增时设置等于窗口长度的 TTL；
//     自增后的计数未超过上限则放行，超过则拒绝（Req 21.1、21.2）。
//
// 返回 allowed 表示是否放行；err 仅在计数后端发生不可恢复错误时返回。出于可用性优先的
// 降级考量（与工具缓存的 Redis 降级策略一致），计数后端异常时记录告警并放行（fail-open），
// 避免因 Redis 抖动而拒绝合法流量。
func (l *RateLimiter) Allow(ctx context.Context, key Metadata, now time.Time) (bool, error) {
	allowed, _, err := l.AllowWithReason(ctx, key, now)
	return allowed, err
}

// AllowWithReason 判定请求是否应被受理，并在拒绝时返回面向用户的原因。
func (l *RateLimiter) AllowWithReason(ctx context.Context, key Metadata, now time.Time) (bool, string, error) {
	windows := rateWindows(key, now)
	if len(windows) == 0 {
		return true, "", nil
	}
	reservations := make([]RateReservation, 0, len(windows))
	for _, w := range windows {
		reservations = append(reservations, RateReservation{
			Key:   w.key,
			Limit: w.limit,
			TTL:   w.ttl,
		})
	}

	allowed, rejectedIndex, err := l.counter.Reserve(ctx, reservations)
	if err != nil {
		l.logger.Warn("限流计数失败，降级放行", "apiKeyID", key.ID, "error", err)
		return true, "", nil
	}
	if allowed {
		return true, "", nil
	}
	if rejectedIndex < 1 || rejectedIndex > len(windows) {
		rejectedIndex = 1
	}
	w := windows[rejectedIndex-1]
	return false, fmt.Sprintf("%s已用尽，预计 %s 重置", w.label, w.reset.Format(time.RFC3339)), nil
}

func rateWindows(key Metadata, now time.Time) []rateWindow {
	windows := make([]rateWindow, 0, 3)
	limit, windowS, active := rateLimitConfig(key)
	if active {
		startUnix := windowStartUnix(now, windowS)
		start := time.Unix(startUnix, 0)
		reset := start.Add(time.Duration(windowS) * time.Second)
		windows = append(windows, rateWindow{
			key:   rateLimitKey(key.ID, startUnix),
			label: "请求频率",
			limit: limit,
			reset: reset,
			ttl:   time.Duration(windowS) * time.Second,
		})
	}
	local := now.UTC()
	// API Key 周期额度统一按 UTC 自然日/月重置，避免给单个 Key 再引入时区配置负担。
	if key.QuotaPerDay != nil && *key.QuotaPerDay > 0 {
		start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
		reset := start.AddDate(0, 0, 1)
		windows = append(windows, rateWindow{
			key:   rateQuotaKey(key.ID, "day", start.Unix()),
			label: "每日调用额度",
			limit: *key.QuotaPerDay,
			reset: reset,
			ttl:   reset.Sub(local).Round(time.Second) + time.Second,
		})
	}
	if key.QuotaPerMonth != nil && *key.QuotaPerMonth > 0 {
		start := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.UTC)
		reset := start.AddDate(0, 1, 0)
		windows = append(windows, rateWindow{
			key:   rateQuotaKey(key.ID, "month", start.Unix()),
			label: "每月调用额度",
			limit: *key.QuotaPerMonth,
			reset: reset,
			ttl:   reset.Sub(local).Round(time.Second) + time.Second,
		})
	}
	return windows
}

type rateWindow struct {
	key   string
	label string
	limit int
	reset time.Time
	ttl   time.Duration
}

// rateLimitConfig 解析 API Key 的限流配置，返回上限、窗口秒数与限流是否生效。
//
// 仅当同时配置了正的速率上限与正的窗口长度时限流才生效；任一缺失或非正都视为不限流
// （Req 21.4），以避免误配置导致的除零或恒拒绝。
func rateLimitConfig(key Metadata) (limit int, windowS int, active bool) {
	if key.RateLimit == nil || key.RateWindowS == nil {
		return 0, 0, false
	}
	if *key.RateLimit <= 0 || *key.RateWindowS <= 0 {
		return 0, 0, false
	}
	return *key.RateLimit, *key.RateWindowS, true
}

// RateLimitKeyResolver 从 gin 上下文中解析当前请求所属的 API Key 元数据。
//
// 限流中间件位于 API Key 鉴权之后（见设计文档「鉴权中间件」的对外链路），鉴权通过后
// 将 API Key 元数据写入上下文，本解析器据此取出待限流的 Key。以函数注入而非固定上下文键
// 依赖，便于解耦与单元测试。返回 ok=false 表示上下文中无 API Key。
type RateLimitKeyResolver func(c *gin.Context) (Metadata, bool)

// Middleware 返回一个按 API Key 固定窗口限流的 gin 中间件（Req 21）。
//
// 中间件流程：
//   - 通过 resolve 从上下文取出当前 API Key；取不到则放行（限流无对象，交由前序鉴权负责拒绝）。
//   - 调用 Allow 判定是否超额；超额则以统一错误模型（domain.APIError，code=RATE_LIMITED）
//     返回 HTTP 429 并中止后续处理器（Req 21.2）。
//   - 未超额或未配置上限则放行至下一处理器（Req 21.1、21.4）。
//
// resolve 为 nil 时中间件直接放行，避免误将其接入到无法解析 Key 的链路上造成全量拒绝。
// 限流判定以服务器当前时刻为准。
func (l *RateLimiter) Middleware(resolve RateLimitKeyResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		if resolve == nil {
			c.Next()
			return
		}

		key, ok := resolve(c)
		if !ok {
			// 上下文中无 API Key：无限流对象，放行（前序鉴权中间件负责拒绝无效请求）。
			c.Next()
			return
		}

		allowed, reason, err := l.AllowWithReason(c.Request.Context(), key, time.Now())
		if err != nil {
			// Allow 当前不返回错误（计数异常已内部降级放行），此分支为防御性处理。
			l.logger.Warn("限流判定异常，降级放行", "apiKeyID", key.ID, "error", err)
			c.Next()
			return
		}

		if !allowed {
			if reason == "" {
				reason = "请求超过该 API Key 的限流或额度上限"
			}
			c.AbortWithStatusJSON(http.StatusTooManyRequests,
				domain.NewError(domain.CodeRateLimited, reason))
			return
		}

		c.Next()
	}
}
