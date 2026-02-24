// Package cache provides a Redis-backed response cache for immutable historical
// blockchain data. Only responses to height-bearing methods with a known block
// height are cached; latest/head queries, write operations, and dynamic state
// always bypass the cache.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Config holds cache configuration.
type Config struct {
	Enabled      bool          `yaml:"enabled"`
	RedisURL     string        `yaml:"redis_url"`
	TTL          time.Duration `yaml:"ttl"`
	MaxEntrySize int64         `yaml:"max_entry_size"`
}

// Cache is the interface for response caching.
type Cache interface {
	Get(ctx context.Context, method string, height int64, params []byte) ([]byte, bool)
	Set(ctx context.Context, method string, height int64, params []byte, response []byte)
	Close() error
}

// RedisCache implements Cache using Redis.
type RedisCache struct {
	client       *redis.Client
	ttl          time.Duration
	maxEntrySize int64
	logger       *zap.Logger
}

// NewRedisCache creates a Redis-backed cache.
func NewRedisCache(cfg Config, logger *zap.Logger) (*RedisCache, error) {
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parsing redis URL: %w", err)
	}

	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	ttl := cfg.TTL
	if ttl == 0 {
		ttl = 720 * time.Hour // 30 days default
	}

	maxEntrySize := cfg.MaxEntrySize
	if maxEntrySize == 0 {
		maxEntrySize = 5 * 1024 * 1024 // 5MB default
	}

	return &RedisCache{
		client:       client,
		ttl:          ttl,
		maxEntrySize: maxEntrySize,
		logger:       logger,
	}, nil
}

// Key generates a deterministic cache key: <method>:<height>:<hash(params)>.
func Key(method string, height int64, params []byte) string {
	h := sha256.Sum256(params)
	return fmt.Sprintf("%s:%d:%s", method, height, hex.EncodeToString(h[:16]))
}

// Get retrieves a cached response. Returns (nil, false) on miss.
func (c *RedisCache) Get(ctx context.Context, method string, height int64, params []byte) ([]byte, bool) {
	key := Key(method, height, params)
	data, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		if err != redis.Nil {
			c.logger.Debug("cache get error", zap.String("key", key), zap.Error(err))
		}
		return nil, false
	}
	return data, true
}

// Set stores a response in the cache. Skips entries larger than MaxEntrySize.
func (c *RedisCache) Set(ctx context.Context, method string, height int64, params []byte, response []byte) {
	if int64(len(response)) > c.maxEntrySize {
		c.logger.Debug("skipping cache set: response too large",
			zap.String("method", method),
			zap.Int("size", len(response)),
		)
		return
	}

	key := Key(method, height, params)
	if err := c.client.Set(ctx, key, response, c.ttl).Err(); err != nil {
		c.logger.Debug("cache set error", zap.String("key", key), zap.Error(err))
	}
}

// Close shuts down the Redis client.
func (c *RedisCache) Close() error {
	return c.client.Close()
}

// cacheable methods — these return immutable data for a given height.
var cacheableMethods = map[string]bool{
	// Celestia DA node
	"header.GetByHeight":         true,
	"blob.Get":                   true,
	"blob.GetAll":                true,
	"blob.GetProof":              true,
	"share.GetEDS":               true,
	"share.GetSharesByNamespace": true,
	"share.GetNamespaceData":     true,

	// Tendermint RPC
	"block":         true,
	"block_results": true,
	"validators":    true,
	"commit":        true,
	"header":        true,
}

// nonCacheableMethods are explicitly never cached, even if they carry a height.
var nonCacheableMethods = map[string]bool{
	// Dynamic/head state
	"header.NetworkHead": true,
	"header.SyncState":   true,
	"state.Balance":      true,
	"status":             true,
	"health":             true,
	"net_info":           true,
	"node.Info":          true,

	// Write operations
	"broadcast_tx_sync":               true,
	"broadcast_tx_async":              true,
	"broadcast_tx_commit":             true,
	"blob.Submit":                     true,
	"state.SubmitTx":                  true,
	"state.Transfer":                  true,
	"state.SubmitPayForBlob":          true,
	"state.CancelUnbondingDelegation": true,
	"state.BeginRedelegate":           true,
	"state.Undelegate":                true,
	"state.Delegate":                  true,
}

// IsCacheable returns true if the method+height combination can be cached.
// Only methods that return immutable data at a specific (non-zero) height are cacheable.
func IsCacheable(method string, height int64) bool {
	if height <= 0 {
		return false // latest/head queries are never cached
	}
	if nonCacheableMethods[method] {
		return false
	}
	return cacheableMethods[method]
}

// NoopCache is a no-op implementation of Cache for when caching is disabled.
type NoopCache struct{}

// Get always returns a miss.
func (NoopCache) Get(context.Context, string, int64, []byte) ([]byte, bool) {
	return nil, false
}

// Set is a no-op.
func (NoopCache) Set(context.Context, string, int64, []byte, []byte) {}

// Close is a no-op.
func (NoopCache) Close() error { return nil }
