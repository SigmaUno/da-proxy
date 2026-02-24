// Package middleware provides Echo middleware for authentication, rate limiting, logging, and metrics.
package middleware

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/SigmaUno/da-proxy/internal/auth"
)

// Auth returns Echo middleware that extracts and validates URL path tokens.
// The token is the first segment of the URL path. It is stripped before
// forwarding to backends.
func Auth(store auth.TokenStore) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := c.Request().URL.Path

			// Extract first path segment as token.
			// Path format: /<token>/rest-of-path or /<token>
			token, remaining := extractToken(path)
			if token == "" {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing authentication token")
			}

			info, found := store.Lookup(token)
			if !found {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid authentication token")
			}

			if !info.Enabled {
				return echo.NewHTTPError(http.StatusForbidden, "token is disabled")
			}

			if info.IsExpired() {
				return echo.NewHTTPError(http.StatusUnauthorized, "token has expired")
			}

			// Strip token from path — backends never see it.
			c.Request().URL.Path = remaining
			c.Request().URL.RawPath = ""

			// Attach token metadata to context for downstream middleware.
			c.Set(ContextKeyTokenName, info.Name)
			c.Set(ContextKeyRateLimit, info.RateLimit)
			c.Set(ContextKeyTokenInfo, info)

			return next(c)
		}
	}
}

// extractToken splits the URL path into the token (first segment) and the
// remaining path. Returns empty token if the path has no non-empty first segment.
func extractToken(path string) (token, remaining string) {
	// Trim leading slash
	trimmed := strings.TrimPrefix(path, "/")
	if trimmed == "" {
		return "", "/"
	}

	idx := strings.IndexByte(trimmed, '/')
	if idx == -1 {
		// Path is just /<token>
		return trimmed, "/"
	}

	// Path is /<token>/rest...
	return trimmed[:idx], trimmed[idx:]
}
