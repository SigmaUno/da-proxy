package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/auth"
	"github.com/SigmaUno/da-proxy/internal/cache"
	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/SigmaUno/da-proxy/internal/middleware"
)

// mockCache records cache operations for testing.
type mockCache struct {
	mu    sync.Mutex
	store map[string][]byte
	gets  int
	sets  int
	hits  int
}

func newMockCache() *mockCache {
	return &mockCache{store: make(map[string][]byte)}
}

func (m *mockCache) Get(_ context.Context, method string, height int64, params []byte) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	key := cache.Key(method, height, params)
	if data, ok := m.store[key]; ok {
		m.hits++
		return data, true
	}
	return nil, false
}

func (m *mockCache) Set(_ context.Context, method string, height int64, params []byte, response []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sets++
	key := cache.Key(method, height, params)
	m.store[key] = append([]byte(nil), response...)
}

func (m *mockCache) Close() error { return nil }

func (m *mockCache) preload(method string, height int64, params []byte, response []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := cache.Key(method, height, params)
	m.store[key] = append([]byte(nil), response...)
}

func setupHandlerTest(t *testing.T, backendHandler http.HandlerFunc) (*echo.Echo, *httptest.Server) {
	t.Helper()
	backend := httptest.NewServer(backendHandler)
	t.Cleanup(backend.Close)

	backends := config.BackendsConfig{
		CelestiaAppRPC:  backend.URL,
		CelestiaAppGRPC: backend.Listener.Addr().String(),
		CelestiaAppREST: backend.URL,
		CelestiaNodeRPC: backend.URL,
	}

	router := NewRouter(backends)
	handler := NewHandler(router, 10*1024*1024, zap.NewNop())

	e := echo.New()
	// Simulate request ID being set.
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(middleware.ContextKeyRequestID, "test-request-id")
			return next(c)
		}
	})
	e.Any("/*", handler.HandleRequest)

	return e, backend
}

func TestHandler_JSONRPCToDANode(t *testing.T) {
	var receivedBody string
	var receivedPath string
	e, _ := setupHandlerTest(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{"data":"test"},"id":1}`))
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"blob.Get","params":[2683915]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, receivedBody, "blob.Get")
	assert.Equal(t, "/", receivedPath)
	assert.Equal(t, "celestia-node-rpc", rec.Header().Get(HeaderXDABackend))
	assert.Equal(t, "test-request-id", rec.Header().Get("X-Request-ID"))
}

func TestHandler_JSONRPCToConsensus(t *testing.T) {
	var receivedBody string
	e, _ := setupHandlerTest(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{},"id":1}`))
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"status","params":[]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, receivedBody, "status")
	assert.Equal(t, "celestia-app-rpc", rec.Header().Get(HeaderXDABackend))
}

func TestHandler_RESTProxy(t *testing.T) {
	var receivedPath string
	e, _ := setupHandlerTest(t, func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"balances":[]}`))
	})

	req := httptest.NewRequest(http.MethodGet, "/cosmos/bank/v1beta1/balances/celestia1abc", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "/cosmos/bank/v1beta1/balances/celestia1abc", receivedPath)
	assert.Equal(t, "celestia-app-rest", rec.Header().Get(HeaderXDABackend))
}

func TestHandler_BodyTooLarge(t *testing.T) {
	backends := config.BackendsConfig{
		CelestiaAppRPC:  "http://localhost:26657",
		CelestiaNodeRPC: "http://localhost:26658",
		CelestiaAppGRPC: "localhost:9090",
		CelestiaAppREST: "http://localhost:1317",
	}
	router := NewRouter(backends)
	handler := NewHandler(router, 10, zap.NewNop()) // 10 bytes max

	e := echo.New()
	e.Any("/*", handler.HandleRequest)

	body := `{"jsonrpc":"2.0","id":1,"method":"status","params":[]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestHandler_MalformedJSON(t *testing.T) {
	e, _ := setupHandlerTest(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{invalid json`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandler_BackendDown(t *testing.T) {
	backends := config.BackendsConfig{
		CelestiaAppRPC:  "http://127.0.0.1:1", // unreachable port
		CelestiaNodeRPC: "http://127.0.0.1:1",
		CelestiaAppGRPC: "127.0.0.1:1",
		CelestiaAppREST: "http://127.0.0.1:1",
	}
	router := NewRouter(backends)
	handler := NewHandler(router, 10*1024*1024, zap.NewNop())

	e := echo.New()
	e.Any("/*", handler.HandleRequest)

	body := `{"jsonrpc":"2.0","id":1,"method":"status","params":[]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestHandler_ResponseForwarded(t *testing.T) {
	expectedResponse := `{"jsonrpc":"2.0","result":{"height":"100"},"id":1}`
	e, _ := setupHandlerTest(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(expectedResponse))
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"status","params":[]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	respBody, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	assert.Equal(t, expectedResponse, string(respBody))
}

func TestHandler_EmptyBody(t *testing.T) {
	e, _ := setupHandlerTest(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Empty body defaults to Tendermint RPC backend (supports GET-style requests).
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func setupHandlerTestWithTokenInfo(t *testing.T, info auth.TokenInfo, backendHandler http.HandlerFunc) *echo.Echo {
	t.Helper()
	backend := httptest.NewServer(backendHandler)
	t.Cleanup(backend.Close)

	backends := config.BackendsConfig{
		CelestiaAppRPC:  backend.URL,
		CelestiaAppGRPC: backend.Listener.Addr().String(),
		CelestiaAppREST: backend.URL,
		CelestiaNodeRPC: backend.URL,
	}

	router := NewRouter(backends)
	handler := NewHandler(router, 10*1024*1024, zap.NewNop())

	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(middleware.ContextKeyRequestID, "test-request-id")
			c.Set(middleware.ContextKeyTokenInfo, info)
			return next(c)
		}
	})
	e.Any("/*", handler.HandleRequest)
	return e
}

func TestHandler_MethodAuth_ReadOnlyBlocksWrite(t *testing.T) {
	info := auth.TokenInfo{
		Name:    "readonly-token",
		Enabled: true,
		Scope:   "read-only",
	}

	e := setupHandlerTestWithTokenInfo(t, info, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{},"id":1}`))
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"broadcast_tx_sync","params":["abc"]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandler_MethodAuth_ReadOnlyAllowsRead(t *testing.T) {
	info := auth.TokenInfo{
		Name:    "readonly-token",
		Enabled: true,
		Scope:   "read-only",
	}

	e := setupHandlerTestWithTokenInfo(t, info, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{},"id":1}`))
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"status","params":[]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestHandler_MethodAuth_AllowlistBlocks(t *testing.T) {
	info := auth.TokenInfo{
		Name:           "restricted-token",
		Enabled:        true,
		Scope:          "write",
		AllowedMethods: []string{"blob.Get", "blob.Submit"},
	}

	e := setupHandlerTestWithTokenInfo(t, info, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{},"id":1}`))
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"status","params":[]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandler_MethodAuth_AllowlistPermits(t *testing.T) {
	info := auth.TokenInfo{
		Name:           "restricted-token",
		Enabled:        true,
		Scope:          "write",
		AllowedMethods: []string{"blob.*"},
	}

	e := setupHandlerTestWithTokenInfo(t, info, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{"data":"test"},"id":1}`))
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"blob.Get","params":[1]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestHandler_MethodAuth_NoTokenInfo(t *testing.T) {
	// Without token info in context, method auth is skipped.
	e, _ := setupHandlerTest(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{},"id":1}`))
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"broadcast_tx_sync","params":["abc"]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func setupHandlerTestWithCache(t *testing.T, mc *mockCache, backendHandler http.HandlerFunc) *echo.Echo {
	t.Helper()
	backend := httptest.NewServer(backendHandler)
	t.Cleanup(backend.Close)

	backends := config.BackendsConfig{
		CelestiaAppRPC:  backend.URL,
		CelestiaAppGRPC: backend.Listener.Addr().String(),
		CelestiaAppREST: backend.URL,
		CelestiaNodeRPC: backend.URL,
	}

	router := NewRouter(backends)
	handler := NewHandler(router, 10*1024*1024, zap.NewNop(), WithCache(mc))

	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(middleware.ContextKeyRequestID, "test-request-id")
			return next(c)
		}
	})
	e.Any("/*", handler.HandleRequest)
	return e
}

func TestHandler_Cache_Hit(t *testing.T) {
	mc := newMockCache()
	backendCalled := false

	e := setupHandlerTestWithCache(t, mc, func(w http.ResponseWriter, _ *http.Request) {
		backendCalled = true
		w.WriteHeader(http.StatusOK)
	})

	// Preload cache with a response for block at height 100.
	reqBody := `{"jsonrpc":"2.0","id":1,"method":"block","params":["100"]}`
	cachedResp := `{"jsonrpc":"2.0","result":{"block":{}},"id":1}`
	mc.preload("block", 100, []byte(reqBody), []byte(cachedResp))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, backendCalled, "backend should not be called on cache hit")
	assert.Equal(t, "HIT", rec.Header().Get(HeaderXCacheStatus))
	assert.Equal(t, "celestia-app-rpc", rec.Header().Get(HeaderXDABackend))

	respBody, _ := io.ReadAll(rec.Body)
	assert.Equal(t, cachedResp, string(respBody))
}

func TestHandler_Cache_Miss(t *testing.T) {
	mc := newMockCache()
	backendResp := `{"jsonrpc":"2.0","result":{"block":{}},"id":1}`

	e := setupHandlerTestWithCache(t, mc, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(backendResp))
	})

	reqBody := `{"jsonrpc":"2.0","id":1,"method":"block","params":["100"]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "MISS", rec.Header().Get(HeaderXCacheStatus))

	// Cache should have been populated.
	mc.mu.Lock()
	assert.Equal(t, 1, mc.gets)
	assert.Equal(t, 1, mc.sets)
	mc.mu.Unlock()
}

func TestHandler_Cache_Bypass_NonCacheable(t *testing.T) {
	mc := newMockCache()

	e := setupHandlerTestWithCache(t, mc, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{},"id":1}`))
	})

	// "status" is non-cacheable.
	body := `{"jsonrpc":"2.0","id":1,"method":"status","params":[]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get(HeaderXCacheStatus))

	mc.mu.Lock()
	assert.Equal(t, 0, mc.gets)
	assert.Equal(t, 0, mc.sets)
	mc.mu.Unlock()
}

func TestHandler_Cache_Bypass_WriteMethod(t *testing.T) {
	mc := newMockCache()

	e := setupHandlerTestWithCache(t, mc, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{},"id":1}`))
	})

	// "broadcast_tx_sync" is a write operation - never cached.
	body := `{"jsonrpc":"2.0","id":1,"method":"broadcast_tx_sync","params":["abc"]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	mc.mu.Lock()
	assert.Equal(t, 0, mc.sets)
	mc.mu.Unlock()
}

func TestHandler_Cache_Bypass_LatestHeight(t *testing.T) {
	mc := newMockCache()

	e := setupHandlerTestWithCache(t, mc, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{},"id":1}`))
	})

	// "block" with no height param (latest) - not cacheable.
	body := `{"jsonrpc":"2.0","id":1,"method":"block","params":[]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	mc.mu.Lock()
	assert.Equal(t, 0, mc.gets)
	assert.Equal(t, 0, mc.sets)
	mc.mu.Unlock()
}
