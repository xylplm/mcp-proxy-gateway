package security

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCache struct {
	rdb *redis.Client
}

func NewRedisCache(rdb *redis.Client) *RedisCache {
	return &RedisCache{rdb: rdb}
}

func (c *RedisCache) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	n, err := c.rdb.Incr(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	if n == 1 && ttl > 0 {
		if err := c.rdb.Expire(ctx, key, ttl).Err(); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (c *RedisCache) SetBlock(ctx context.Context, subjectType, subject string, ttl time.Duration) error {
	return c.rdb.Set(ctx, blockKey(subjectType, subject), "1", ttl).Err()
}

func (c *RedisCache) IsBlocked(ctx context.Context, subjectType, subject string) (bool, error) {
	n, err := c.rdb.Exists(ctx, blockKey(subjectType, subject)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (c *RedisCache) DeleteBlock(ctx context.Context, subjectType, subject string) error {
	return c.rdb.Del(ctx, blockKey(subjectType, subject)).Err()
}

func blockKey(subjectType, subject string) string {
	return fmt.Sprintf("mpg:sec:block:%s:%s", subjectType, subjectHash(subject))
}
