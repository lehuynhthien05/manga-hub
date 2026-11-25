package cache

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"mangahub/pkg/protocol"
)

const (
	opTimeout   = 200 * time.Millisecond // a cache must never make a request slow
	warnEvery   = 10 * time.Second       // an outage logs one warning per 10 s, not one per request
	keyPrefix   = "manga:v1:"            // bump "v1" if the cached JSON shape changes
	skipFor     = time.Second            // after an error, skip Redis for this long
	healthLimit = 200 * time.Millisecond
)

// MangaCache is cache-aside for manga details; the catalog stays the source of truth.
// Every error behaves as a miss (fail-open).
type MangaCache struct {
	rdb      *redis.Client
	ttl      time.Duration
	log      *slog.Logger
	lastWarn atomic.Int64 // unix nanos of the last warning
	downTill atomic.Int64 // unix nanos until which Redis is skipped
}

// New creates the cache. It does not connect yet: the first command dials Redis.
func New(addr string, ttl time.Duration, log *slog.Logger) *MangaCache {
	return &MangaCache{ttl: ttl, log: log, rdb: redis.NewClient(&redis.Options{Addr: addr,
		DialTimeout: opTimeout, ReadTimeout: opTimeout, WriteTimeout: opTimeout, // defaults are 5 s / 3 s
		MaxRetries: -1, DialerRetries: 1, // v9: -1 (not 0) disables command retries; dials retry 5× by default
	})}
}

func key(id string) string { return keyPrefix + id }

// skip is a tiny circuit breaker: during an outage a request pays the timeout at
// most once per skipFor instead of on every Get and Set.
func (c *MangaCache) skip() bool { return time.Now().UnixNano() < c.downTill.Load() }

func (c *MangaCache) warn(op string, err error) {
	now := time.Now().UnixNano()
	c.downTill.Store(now + int64(skipFor))
	last := c.lastWarn.Load()
	if now-last >= int64(warnEvery) && c.lastWarn.CompareAndSwap(last, now) {
		c.log.Warn("redis unavailable: serving without cache", "op", op, "err", err)
	}
}

// Get returns the cached manga; any error or a corrupt value counts as a miss.
func (c *MangaCache) Get(ctx context.Context, id string) (protocol.Manga, bool) {
	if c.skip() {
		return protocol.Manga{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	b, err := c.rdb.Get(ctx, key(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return protocol.Manga{}, false
	}
	if err != nil {
		c.warn("get", err)
		return protocol.Manga{}, false
	}
	var m protocol.Manga
	if json.Unmarshal(b, &m) != nil { // corrupt or old shape: just a miss
		return protocol.Manga{}, false
	}
	return m, true
}

// Set stores m for the TTL. A failure is logged at most every 10 s and ignored.
func (c *MangaCache) Set(ctx context.Context, m protocol.Manga) {
	b, err := json.Marshal(m)
	if err != nil || c.skip() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	if err := c.rdb.Set(ctx, key(m.ID), b, c.ttl).Err(); err != nil {
		c.warn("set", err)
	}
}

// Invalidate implements api.CacheInvalidator. If it fails, the TTL bounds the staleness.
// It is not skipped by the breaker: a missed DEL is worse than a slow one.
func (c *MangaCache) Invalidate(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	if err := c.rdb.Del(ctx, key(id)).Err(); err != nil {
		c.warn("del", err)
	}
}

// Ping checks Redis for /api/health.
func (c *MangaCache) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, healthLimit)
	defer cancel()
	return c.rdb.Ping(ctx).Err()
}

// Close closes the Redis client.
func (c *MangaCache) Close() error { return c.rdb.Close() }
