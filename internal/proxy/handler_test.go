package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/SigmaUno/da-proxy/internal/middleware"
)

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

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
