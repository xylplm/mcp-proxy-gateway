package stats

import (
	"context"
	"errors"

	"github.com/redis/go-redis/v9"
)

// redisStatBuffer 以 go-redis 客户端实现 StatBuffer，是统计异步缓冲的生产实现（Req 16.8）。
//
// 缓冲为单个 Redis List（键 StatBufferKey）：主流程侧 LPUSH 入头部，后台 worker 侧
// RPOP 自尾部批量取出，构成 FIFO 的持久异步缓冲。
type redisStatBuffer struct {
	rdb *redis.Client
}

// 编译期断言：redisStatBuffer 必须满足 StatBuffer 接口契约。
var _ StatBuffer = (*redisStatBuffer)(nil)

// NewRedisStatBuffer 将 go-redis 客户端适配为 StatBuffer。
func NewRedisStatBuffer(rdb *redis.Client) StatBuffer {
	return &redisStatBuffer{rdb: rdb}
}

// Push 将一批已序列化记录 LPUSH 入缓冲 List 头部。空切片为无操作。
func (b *redisStatBuffer) Push(ctx context.Context, items ...string) error {
	if len(items) == 0 {
		return nil
	}
	values := make([]any, len(items))
	for i, it := range items {
		values[i] = it
	}
	return b.rdb.LPush(ctx, StatBufferKey, values...).Err()
}

// PopBatch 自缓冲 List 尾部 RPOP 批量取出至多 max 条记录。
//
// 队列为空时 RPOPCOUNT 返回 redis.Nil，本方法将其归一化为空切片而非错误，
// 便于 worker 据「空切片」判定缓冲已清空（Req 16.8）。
func (b *redisStatBuffer) PopBatch(ctx context.Context, max int) ([]string, error) {
	if max <= 0 {
		max = 1
	}
	items, err := b.rdb.RPopCount(ctx, StatBufferKey, max).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return items, nil
}
