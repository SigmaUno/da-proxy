package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

func setupRateLimitTest(ratePerMinute int) (*echo.Echo, *RateLimiterStore) {
	e := echo.New()
	store := NewRateLimiterStore()

	// Simulate auth middleware having set context values.
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(ContextKeyTokenName, "test-token")
			c.Set(ContextKeyRateLimit, ratePerMinute)
			return next(c)
		}
	})
	e.Use(RateLimit(store))
	e.GET("/", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	return e, store
}

func TestRateLimit_Unlimited(t *testing.T) {
	e, _ := setupRateLimitTest(0)

	// Should never be rate limited.
	for i := 0; i < 200; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	}
}

func TestRateLimit_Exceeded(t *testing.T) {
	// 6 requests per minute = 0.1 per second, burst of 1
	e, _ := setupRateLimitTest(6)

	// First request should pass (burst allows it).
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Rapid subsequent requests should eventually hit rate limit.
	hitRateLimit := false
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			hitRateLimit = true
			// Should have Retry-After header.
			assert.NotEmpty(t, rec.Header().Get("Retry-After"))
			break
		}
	}
	assert.True(t, hitRateLimit, "expected to hit rate limit")
}

func TestRateLimit_IndependentTokens(t *testing.T) {
	e := echo.New()
	store := NewRateLimiterStore()

	// Use query param to simulate different tokens.
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(ContextKeyTokenName, c.QueryParam("token"))
			c.Set(ContextKeyRateLimit, 6) // very low limit
			return next(c)
		}
	})
	e.Use(RateLimit(store))
	e.GET("/", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	// Exhaust token-a's rate limit.
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodGet, "/?token=token-a", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
	}

	// token-b should still work.
	req := httptest.NewRequest(http.MethodGet, "/?token=token-b", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRateLimit_NoContextValues(t *testing.T) {
	e := echo.New()
	store := NewRateLimiterStore()

	// No auth middleware setting context — rate limit should be skipped.
	e.Use(RateLimit(store))
	e.GET("/", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRateLimiterStore_GetLimiter(t *testing.T) {
	store := NewRateLimiterStore()

	t.Run("zero rate returns nil", func(t *testing.T) {
		limiter := store.GetLimiter("token-a", 0)
		assert.Nil(t, limiter)
	})

	t.Run("negative rate returns nil", func(t *testing.T) {
		limiter := store.GetLimiter("token-a", -1)
		assert.Nil(t, limiter)
	})

	t.Run("positive rate returns limiter", func(t *testing.T) {
		limiter := store.GetLimiter("token-b", 60)
		assert.NotNil(t, limiter)
	})

	t.Run("same token returns same limiter", func(t *testing.T) {
		l1 := store.GetLimiter("token-c", 120)
		l2 := store.GetLimiter("token-c", 120)
		assert.Same(t, l1, l2)
	})
}
