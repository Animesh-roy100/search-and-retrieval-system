// Package cache is a Redis-backed answer cache for the read path. It degrades
// gracefully: if Redis is unavailable, Get is a miss and Set is a no-op.
package cache

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
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
	// Tight timeouts so a slow/degraded Redis fails fast to a miss instead of
	// blowing the read-path latency SLO — the graceful-miss design absorbs it.
	return &Cache{rdb: redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  200 * time.Millisecond,
		ReadTimeout:  100 * time.Millisecond,
		WriteTimeout: 100 * time.Millisecond,
	}), ttl: ttl}
}

// Key normalizes a query + filters + generation into a stable cache key. Folding
// the generation in means a write that bumps the tenant's generation makes every
// prior key unreachable — instant, cheap invalidation without scanning keys.
func Key(query string, filters map[string]string, gen int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "g%d|", gen)
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

// genKey is the Redis key holding a tenant's cache generation.
func genKey(tenant string) string {
	if tenant == "" {
		tenant = "_global"
	}
	return "gen:" + tenant
}

// Generation returns the tenant's current cache generation (0 if unset / no Redis).
func (c *Cache) Generation(ctx context.Context, tenant string) int64 {
	if c == nil || c.rdb == nil {
		return 0
	}
	v, err := c.rdb.Get(ctx, genKey(tenant)).Result()
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

// BumpGeneration advances a tenant's cache generation, invalidating that tenant's
// cached answers. Called by the indexer whenever it writes docs for a tenant.
func (c *Cache) BumpGeneration(ctx context.Context, tenant string) {
	if c == nil || c.rdb == nil {
		return
	}
	_ = c.rdb.Incr(ctx, genKey(tenant)).Err()
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
