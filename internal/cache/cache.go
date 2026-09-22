// Package cache is a Redis-backed answer cache for the read path. It degrades
// gracefully: if Redis is unavailable, Get is a miss and Set is a no-op.
package cache

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type Cache struct {
	rdb *redis.Client
	ttl time.Duration
}

// New connects to Redis. A nil/unreachable Redis still returns a usable Cache that
// simply never hits.
func New(addr string, ttl time.Duration) *Cache {
	if addr == "" {
		return &Cache{ttl: ttl}
	}
	return &Cache{rdb: redis.NewClient(&redis.Options{Addr: addr}), ttl: ttl}
}

// Key normalizes a query + filters into a stable cache key.
func Key(query string, filters map[string]string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(strings.TrimSpace(query)))
	// deterministic filter order
	keys := make([]string, 0, len(filters))
	for k := range filters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString("|" + k + "=" + filters[k])
	}
	sum := sha1.Sum([]byte(b.String()))
	return "ans:" + hex.EncodeToString(sum[:])
}

func (c *Cache) Get(ctx context.Context, key string, out any) bool {
	if c == nil || c.rdb == nil {
		return false
	}
	val, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return false
	}
	return json.Unmarshal(val, out) == nil
}

func (c *Cache) Set(ctx context.Context, key string, val any) {
	if c == nil || c.rdb == nil {
		return
	}
	b, err := json.Marshal(val)
	if err != nil {
		return
	}
	_ = c.rdb.Set(ctx, key, b, c.ttl).Err()
}
