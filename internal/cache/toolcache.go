package cache

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// keyPrefix 为工具缓存在 Redis 中的键前缀，完整键形如 mpg:tools:{upstreamID}。
//
// 前缀遵循设计文档「Redis 缓存键设计」的 mpg: 分域约定（Req 6）。
const keyPrefix = "mpg:tools:"

// cacheEntry 为 Redis 值的 JSON 结构：工具列表 + 更新时间戳。
//
// 与 PG tool_cache 持久副本保持同构，便于回填与回源时的语义一致（Req 6.1）。
type cacheEntry struct {
	// Tools 为该上游 MCP 的完整工具列表。
	Tools []domain.ToolDef `json:"tools"`
	// UpdatedAt 为该工具列表的更新时间戳。
	UpdatedAt time.Time `json:"updatedAt"`
}

// ToolCache 实现 domain.Tool_Cache：Redis 热路径 + PostgreSQL 持久兜底。
//
// 以「整列表替换」为唯一写语义（Req 6.1）；聚合服务只读不实时拉取（Req 6.2）；
// 上游删除时级联清理（Req 6.6）。Redis 仅作为热路径加速，读写失败时降级到 PG，
// 不因 Redis 抖动导致整体失败。
type ToolCache struct {
	redis  *redis.Client
	repo   *store.ToolCacheRepo
	logger *slog.Logger
}

// 编译期断言：ToolCache 必须满足 domain.Tool_Cache 接口契约。
var _ domain.Tool_Cache = (*ToolCache)(nil)

// New 构造工具缓存。
//
// rdb 为 Redis 客户端（热路径），repo 为 PG 持久副本仓储（兜底）。
// logger 为空时回退到 slog.Default()。
func New(rdb *redis.Client, repo *store.ToolCacheRepo, logger *slog.Logger) *ToolCache {
	if logger == nil {
		logger = slog.Default()
	}
	return &ToolCache{redis: rdb, repo: repo, logger: logger}
}

// redisKey 拼接某上游 MCP 的 Redis 键。
func redisKey(upstreamID string) string {
	return keyPrefix + upstreamID
}

// Get 读取某上游 MCP 最近一次持久化的工具列表及其更新时间（Req 6.2）。
//
// 读取顺序：优先读 Redis；未命中或出错则回源 PG，命中后回填 Redis。
// 由于接口签名无 error 返回，Redis/PG 均未命中或均失败时统一返回 found=false。
func (c *ToolCache) Get(ctx context.Context, upstreamID string) ([]domain.ToolDef, time.Time, bool) {
	// 1) 优先读 Redis 热路径。
	if c.redis != nil {
		raw, err := c.redis.Get(ctx, redisKey(upstreamID)).Bytes()
		switch {
		case err == nil:
			var entry cacheEntry
			if uerr := json.Unmarshal(raw, &entry); uerr == nil {
				return entry.Tools, entry.UpdatedAt, true
			} else {
				// 值损坏：记录后回源 PG，并尝试以 PG 数据修复 Redis。
				c.logger.Warn("Redis 工具缓存值反序列化失败，回源 PG", "upstreamID", upstreamID, "error", uerr)
			}
		case errors.Is(err, redis.Nil):
			// 未命中：正常回源 PG。
		default:
			// Redis 故障：降级到 PG，不因抖动导致整体失败。
			c.logger.Warn("读取 Redis 工具缓存失败，降级到 PG", "upstreamID", upstreamID, "error", err)
		}
	}

	// 2) 回源 PG 持久副本。
	tools, updatedAt, found, err := c.repo.Get(ctx, upstreamID)
	if err != nil {
		// 接口无 error 返回，PG 出错时记录并按未命中处理。
		c.logger.Error("回源 PG 工具缓存失败", "upstreamID", upstreamID, "error", err)
		return nil, time.Time{}, false
	}
	if !found {
		return nil, time.Time{}, false
	}

	// 3) 命中后回填 Redis（尽力而为，失败不影响本次返回）。
	c.writeRedis(ctx, upstreamID, tools, updatedAt)
	return tools, updatedAt, true
}

// Replace 以整列表替换语义写入某上游 MCP 的工具列表（Req 6.1）。
//
// PG 为持久兜底、是回源真相来源，故先写 PG；PG 写入失败直接返回错误。
// PG 成功后再覆盖写 Redis 热路径（尽力而为）；若 Redis 写入失败，
// 尝试删除可能存在的旧键，使后续 Get 回源 PG，避免读到陈旧热数据。
func (c *ToolCache) Replace(ctx context.Context, upstreamID string, tools []domain.ToolDef) error {
	updatedAt := time.Now().UTC()

	// 1) 先写 PG 持久副本（真相来源）。
	if err := c.repo.Replace(ctx, upstreamID, tools, updatedAt); err != nil {
		return err
	}

	// 2) 覆盖写 Redis 热路径。
	if c.redis != nil {
		if !c.writeRedis(ctx, upstreamID, tools, updatedAt) {
			// 写入失败时删除旧键，确保 Get 回源到刚写入的 PG 数据。
			if derr := c.redis.Del(ctx, redisKey(upstreamID)).Err(); derr != nil && !errors.Is(derr, redis.Nil) {
				c.logger.Warn("清理陈旧 Redis 工具缓存键失败", "upstreamID", upstreamID, "error", derr)
			}
		}
	}
	return nil
}

// Delete 删除某上游 MCP 的缓存工具列表（Req 6.6，级联清理）。
//
// 先删 Redis 键（尽力而为，失败仅记录不阻断），再删 PG 持久副本；
// PG 删除失败返回错误。删除语义幂等，删除不存在的记录不视为错误。
func (c *ToolCache) Delete(ctx context.Context, upstreamID string) error {
	// 1) 删 Redis 键（降级处理，失败不阻断 PG 清理）。
	if c.redis != nil {
		if err := c.redis.Del(ctx, redisKey(upstreamID)).Err(); err != nil && !errors.Is(err, redis.Nil) {
			c.logger.Warn("删除 Redis 工具缓存失败，继续清理 PG", "upstreamID", upstreamID, "error", err)
		}
	}

	// 2) 删 PG 持久副本（真相来源），失败返回错误。
	if _, err := c.repo.Delete(ctx, upstreamID); err != nil {
		return err
	}
	return nil
}

// writeRedis 将工具列表与更新时间序列化后覆盖写入 Redis。
//
// 返回是否写入成功；失败时记录日志并返回 false，由调用方决定降级动作。
// TTL 为无（同步覆盖，PG 为持久兜底），符合设计文档缓存键设计。
func (c *ToolCache) writeRedis(ctx context.Context, upstreamID string, tools []domain.ToolDef, updatedAt time.Time) bool {
	if c.redis == nil {
		return false
	}
	payload, err := json.Marshal(cacheEntry{Tools: tools, UpdatedAt: updatedAt})
	if err != nil {
		c.logger.Warn("序列化 Redis 工具缓存值失败", "upstreamID", upstreamID, "error", err)
		return false
	}
	if err := c.redis.Set(ctx, redisKey(upstreamID), payload, 0).Err(); err != nil {
		c.logger.Warn("写入 Redis 工具缓存失败", "upstreamID", upstreamID, "error", err)
		return false
	}
	return true
}
