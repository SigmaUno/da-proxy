package middleware

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/labstack/echo/v4"
	"golang.org/x/time/rate"
)

// RateLimiterStore manages per-token rate limiters.
type RateLimiterStore struct {
	mu       sync.RWMutex
	limiters map[string]*rate.Limiter
}

// NewRateLimiterStore creates a new store for per-token rate limiters.
func NewRateLimiterStore() *RateLimiterStore {
	return &RateLimiterStore{
		limiters: make(map[string]*rate.Limiter),
	}
}

// GetLimiter returns the rate limiter for a given token, creating one if needed.
// ratePerMinute is the maximum requests per minute; 0 means unlimited.
func (s *RateLimiterStore) GetLimiter(tokenName string, ratePerMinute int) *rate.Limiter {
	if ratePerMinute <= 0 {
		return nil
	}

	s.mu.RLock()
	limiter, ok := s.limiters[tokenName]
	s.mu.RUnlock()
	if ok {
		return limiter
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock.
	if limiter, ok := s.limiters[tokenName]; ok {
		return limiter
	}

	// rate.Limit is events per second.
	rps := rate.Limit(float64(ratePerMinute) / 60.0)
	burst := ratePerMinute / 10
	if burst < 1 {
		burst = 1
	}

	limiter = rate.NewLimiter(rps, burst)
	s.limiters[tokenName] = limiter
	return limiter
}

// RateLimit returns Echo middleware that enforces per-token rate limits.
// It reads token_name and rate_limit from the Echo context (set by Auth middleware).
func RateLimit(store *RateLimiterStore) echo.MiddlewareFunc {
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

			// 0 means unlimited.
			if ratePerMinute == 0 {
				return next(c)
			}

			limiter := store.GetLimiter(tokenName, ratePerMinute)
			if limiter == nil {
				return next(c)
			}

			if !limiter.Allow() {
				c.Response().Header().Set("Retry-After", fmt.Sprintf("%d", 60/ratePerMinute+1))
				return echo.NewHTTPError(http.StatusTooManyRequests, "rate limit exceeded")
			}

			return next(c)
		}
	}
}
