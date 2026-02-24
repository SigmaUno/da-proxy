package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
)

// RedisRateLimiter uses Redis for distributed rate limiting across instances.
type RedisRateLimiter struct {
	client *redis.Client
}

// NewRedisRateLimiter creates a Redis-backed rate limiter.
func NewRedisRateLimiter(redisURL string) (*RedisRateLimiter, error) {
	opts, err := redis.ParseURL(redisURL)
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

	return &RedisRateLimiter{client: client}, nil
}

// Allow checks if the request is within the rate limit using a sliding window.
// Returns true if allowed, false if rate-limited.
func (r *RedisRateLimiter) Allow(ctx context.Context, tokenName string, ratePerMinute int) bool {
	key := fmt.Sprintf("ratelimit:%s", tokenName)
	now := time.Now().UnixMilli()
	windowStart := now - 60000 // 1-minute window

	pipe := r.client.Pipeline()
	// Remove expired entries.
	pipe.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", windowStart))
	// Count current entries.
	countCmd := pipe.ZCard(ctx, key)
	// Add current request.
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: now})
	// Set expiry on the key.
	pipe.Expire(ctx, key, 2*time.Minute)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return true // allow on error to avoid blocking traffic
	}

	count := countCmd.Val()
	return count < int64(ratePerMinute)
}

// Close shuts down the Redis client.
func (r *RedisRateLimiter) Close() error {
	return r.client.Close()
}

// RedisRateLimit returns Echo middleware using Redis-backed distributed rate limiting.
func RedisRateLimit(limiter *RedisRateLimiter) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			tokenNameVal := c.Get(ContextKeyTokenName)
			rateLimitVal := c.Get(ContextKeyRateLimit)

			if tokenNameVal == nil || rateLimitVal == nil {
				return next(c)
			}

			tokenName, ok := tokenNameVal.(string)
			if !ok {
				return next(c)
			}
			ratePerMinute, ok := rateLimitVal.(int)
			if !ok {
				return next(c)
			}

			if ratePerMinute == 0 {
				return next(c)
			}

			if !limiter.Allow(c.Request().Context(), tokenName, ratePerMinute) {
				c.Response().Header().Set("Retry-After", fmt.Sprintf("%d", 60/ratePerMinute+1))
				return echo.NewHTTPError(http.StatusTooManyRequests, "rate limit exceeded")
			}

			return next(c)
		}
	}
}
