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
// 仅声明固定窗口计数实际需要的两个原子操作：Incr 自增计数并返回自增后的值、
// Expire 为键设置存活时间。以接口而非具体 Redis 客户端依赖，便于在单元测试中以
// 内存 fake 替换，使核心限流判定可脱离真实 Redis 验证。*redis.Client 经
// NewRedisRateCounter 适配后满足该接口。
type RateCounter interface {
	// Incr 对 key 计数加一并返回自增后的当前值；键不存在时从 0 起算（首次返回 1）。
	Incr(ctx context.Context, key string) (int64, error)
	// Expire 为 key 设置存活时间 ttl，到期后由存储自动清除。
	Expire(ctx context.Context, key string, ttl time.Duration) error
}

// redisRateCounter 以 go-redis 客户端实现 RateCounter，是限流计数的生产实现。
type redisRateCounter struct {
	rdb *redis.Client
}

// NewRedisRateCounter 将 go-redis 客户端适配为 RateCounter。
func NewRedisRateCounter(rdb *redis.Client) RateCounter {
	return redisRateCounter{rdb: rdb}
}

// Incr 调用 Redis INCR 原子自增并返回自增后的值。
func (c redisRateCounter) Incr(ctx context.Context, key string) (int64, error) {
	return c.rdb.Incr(ctx, key).Result()
}

// Expire 调用 Redis EXPIRE 为键设置存活时间。
func (c redisRateCounter) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return c.rdb.Expire(ctx, key, ttl).Err()
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
	limit, windowS, active := rateLimitConfig(key)
	if !active {
		// 未配置上限或配置非法：不施加限流（Req 21.4）。
		return true, nil
	}

	start := windowStartUnix(now, windowS)
	redisKey := rateLimitKey(key.ID, start)

	n, err := l.counter.Incr(ctx, redisKey)
	if err != nil {
		// 计数后端异常：降级放行，避免基础设施抖动阻断合法请求。
		l.logger.Warn("限流计数自增失败，降级放行", "apiKeyID", key.ID, "error", err)
		return true, nil
	}

	if n == 1 {
		// 仅在窗口内首次计数时设置 TTL，使窗口随时间自然滚动（Req 21.3）。
		if eerr := l.counter.Expire(ctx, redisKey, time.Duration(windowS)*time.Second); eerr != nil {
			// 设置 TTL 失败不影响本次判定；键最坏情况下不过期，但窗口起点变化后即弃用。
			l.logger.Warn("限流计数键设置存活时间失败", "apiKeyID", key.ID, "error", eerr)
		}
	}

	// 自增后的计数超过上限即为超额（Req 21.2）。
	return n <= int64(limit), nil
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

		allowed, err := l.Allow(c.Request.Context(), key, time.Now())
		if err != nil {
			// Allow 当前不返回错误（计数异常已内部降级放行），此分支为防御性处理。
			l.logger.Warn("限流判定异常，降级放行", "apiKeyID", key.ID, "error", err)
			c.Next()
			return
		}

		if !allowed {
			c.AbortWithStatusJSON(http.StatusTooManyRequests,
				domain.NewError(domain.CodeRateLimited, "请求频率超过该 API Key 的速率上限"))
			return
		}

		c.Next()
	}
}
