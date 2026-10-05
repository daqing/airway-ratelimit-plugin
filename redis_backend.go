package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisBackend stores fixed-window counters in Redis, so limits hold across
// processes. Keys are namespaced as <prefix>:<window seconds>:<key>; limiters
// that must share one budget share one backend (same prefix).
type RedisBackend struct {
	client *redis.Client
	prefix string
}

// NewRedisBackend builds a backend on client. prefix namespaces the keys and
// defaults to "ratelimit".
func NewRedisBackend(client *redis.Client, prefix string) *RedisBackend {
	if prefix == "" {
		prefix = "ratelimit"
	}
	return &RedisBackend{client: client, prefix: prefix}
}

// incrScript bumps the window counter and sets the expiry on first hit, in a
// single round trip.
var incrScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return count
`)

func (b *RedisBackend) key(key string, windowSeconds int64) string {
	return fmt.Sprintf("%s:%d:%s", b.prefix, windowSeconds, key)
}

func (b *RedisBackend) Allow(key string, max int, window time.Duration) bool {
	seconds := int64(window.Seconds())
	if seconds < 1 {
		seconds = 1
	}

	count, err := incrScript.Run(context.Background(), b.client,
		[]string{b.key(key, seconds)}, seconds).Int64()
	if err != nil {
		// A broken Redis must not take the site down: fail open.
		return true
	}

	return count <= int64(max)
}

// Reset deletes every key under the backend's prefix. For tests and ops.
func (b *RedisBackend) Reset() {
	ctx := context.Background()

	var cursor uint64
	for {
		keys, next, err := b.client.Scan(ctx, cursor, b.prefix+":*", 100).Result()
		if err != nil {
			return
		}
		if len(keys) > 0 {
			_ = b.client.Del(ctx, keys...).Err()
		}
		cursor = next
		if cursor == 0 {
			return
		}
	}
}

// Count exposes the current counter of a key. For tests.
func (b *RedisBackend) Count(key string, window time.Duration) int64 {
	seconds := int64(window.Seconds())
	if seconds < 1 {
		seconds = 1
	}

	value, err := b.client.Get(context.Background(), b.key(key, seconds)).Result()
	if err != nil {
		return 0
	}

	count, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return count
}
