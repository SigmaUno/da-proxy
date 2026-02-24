package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SigmaUno/da-proxy/internal/auth"
	"github.com/SigmaUno/da-proxy/internal/config"
)

func newTestTokenStore() auth.TokenStore {
	return auth.NewMemoryTokenStore([]config.TokenConfig{
		{Token: "validtoken123", Name: "test-service", Enabled: true, RateLimit: 100},
		{Token: "disabledtoken", Name: "disabled-svc", Enabled: false, RateLimit: 0},
	})
}

func TestAuth_ValidToken(t *testing.T) {
	e := echo.New()
	store := newTestTokenStore()
	e.Use(Auth(store))

	var (
		ctxTokenName string
		ctxRateLimit int
		receivedPath string
	)
	e.Any("/*", func(c echo.Context) error {
		ctxTokenName = c.Get(ContextKeyTokenName).(string)
		ctxRateLimit = c.Get(ContextKeyRateLimit).(int)
		receivedPath = c.Request().URL.Path
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/validtoken123/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "test-service", ctxTokenName)
	assert.Equal(t, 100, ctxRateLimit)
	assert.Equal(t, "/", receivedPath)
}

func TestAuth_DisabledToken(t *testing.T) {
	e := echo.New()
	store := newTestTokenStore()
	e.Use(Auth(store))
	e.Any("/*", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/disabledtoken/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAuth_InvalidToken(t *testing.T) {
	e := echo.New()
	store := newTestTokenStore()
	e.Use(Auth(store))
	e.Any("/*", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/nonexistenttoken/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuth_MissingToken(t *testing.T) {
	e := echo.New()
	store := newTestTokenStore()
	e.Use(Auth(store))
	e.Any("/*", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuth_PathReconstruction(t *testing.T) {
	tests := []struct {
		name         string
		requestPath  string
		expectedPath string
	}{
		{
			name:         "token with trailing slash",
			requestPath:  "/validtoken123/",
			expectedPath: "/",
		},
		{
			name:         "token without trailing slash",
			requestPath:  "/validtoken123",
			expectedPath: "/",
		},
		{
			name:         "token with cosmos REST path",
			requestPath:  "/validtoken123/cosmos/bank/v1beta1/balances/celestia1abc",
			expectedPath: "/cosmos/bank/v1beta1/balances/celestia1abc",
		},
		{
			name:         "token with nested path",
			requestPath:  "/validtoken123/celestia/blob/v1/params",
			expectedPath: "/celestia/blob/v1/params",
		},
		{
			name:         "token with ibc path",
			requestPath:  "/validtoken123/ibc/core/client/v1/client_states",
			expectedPath: "/ibc/core/client/v1/client_states",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			store := newTestTokenStore()
			e.Use(Auth(store))

			var receivedPath string
			e.Any("/*", func(c echo.Context) error {
				receivedPath = c.Request().URL.Path
				return c.NoContent(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodPost, tt.requestPath, nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tt.expectedPath, receivedPath)
		})
	}
}

func TestAuth_TokenNotInResponse(t *testing.T) {
	e := echo.New()
	store := newTestTokenStore()
	e.Use(Auth(store))
	e.Any("/*", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodPost, "/validtoken123/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	// Token should not appear in any response headers
	for key, values := range rec.Header() {
		for _, v := range values {
			assert.NotContains(t, v, "validtoken123", "token leaked in header %s", key)
		}
	}
	// Token should not appear in response body
	assert.NotContains(t, rec.Body.String(), "validtoken123")
}

func TestExtractToken(t *testing.T) {
	tests := []struct {
		path          string
		wantToken     string
		wantRemaining string
	}{
		{"/", "", "/"},
		{"/token123", "token123", "/"},
		{"/token123/", "token123", "/"},
		{"/token123/cosmos/bank", "token123", "/cosmos/bank"},
		{"/token123/a/b/c", "token123", "/a/b/c"},
		{"", "", "/"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			token, remaining := extractToken(tt.path)
			assert.Equal(t, tt.wantToken, token)
			assert.Equal(t, tt.wantRemaining, remaining)
		})
	}
}
