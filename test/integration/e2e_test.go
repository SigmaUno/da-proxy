//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/auth"
	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/SigmaUno/da-proxy/internal/logging"
	"github.com/SigmaUno/da-proxy/internal/metrics"
	"github.com/SigmaUno/da-proxy/internal/middleware"
	"github.com/SigmaUno/da-proxy/internal/proxy"
)

const testToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// setupE2EServer creates a full proxy server with all middleware wired up,
// using real Celestia backends.
func setupE2EServer(t *testing.T) *httptest.Server {
	t.Helper()

	backends := config.BackendsConfig{
		CelestiaAppRPC:  config.Endpoints{prunedRPC},
		CelestiaAppGRPC: config.Endpoints{prunedGRPC},
		CelestiaAppREST: config.Endpoints{"http://195.154.212.53:1317"},
		CelestiaNodeRPC: config.Endpoints{archivalDA},
	}

	tokens := []config.TokenConfig{
		{
			Token:     testToken,
			Name:      "integration-test",
			Enabled:   true,
			RateLimit: 0,
		},
		{
			Token:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Name:    "disabled-token",
			Enabled: false,
		},
	}

	logger, _ := zap.NewDevelopment()
	tokenStore := auth.NewMemoryTokenStore(tokens)
	rateLimiterStore := middleware.NewRateLimiterStore()
	ringBuffer := logging.NewRingBuffer(1000)

	reg := prometheus.NewRegistry()
	promMetrics := metrics.NewMetrics(reg)

	router := proxy.NewRouter(backends)
	handler := proxy.NewHandler(router, 10*1024*1024, logger)
	grpcProxy := proxy.NewGRPCProxy(backends.CelestiaAppGRPC, logger)

	e := echo.New()
	e.Use(
		middleware.RequestID(),
		middleware.Auth(tokenStore),
		middleware.RateLimit(rateLimiterStore),
		middleware.AccessLogger(logger, ringBuffer, nil),
		middleware.MetricsMiddleware(promMetrics),
	)

	e.Any("/*", func(c echo.Context) error {
		ct := c.Request().Header.Get("Content-Type")
		if len(ct) >= 16 && ct[:16] == "application/grpc" {
			grpcProxy.Handler().ServeHTTP(c.Response(), c.Request())
			return nil
		}
		return handler.HandleRequest(c)
	})

	return httptest.NewServer(e)
}

func e2eRequest(t *testing.T, serverURL, token string, body []byte) *http.Response {
	t.Helper()
	url := fmt.Sprintf("%s/%s/", serverURL, token)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

// --- E2E: Full request flow ---

func TestE2E_StatusThroughProxy(t *testing.T) {
	srv := setupE2EServer(t)
	defer srv.Close()

	resp := e2eRequest(t, srv.URL, testToken, jsonRPCRequest("status", []interface{}{}))
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var rpcResp map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &rpcResp))
	result := rpcResp["result"].(map[string]interface{})
	nodeInfo := result["node_info"].(map[string]interface{})
	assert.Equal(t, "mocha-4", nodeInfo["network"])

	// Check response headers.
	assert.Contains(t, resp.Header.Get("X-DA-Backend"), "celestia-app-rpc")
	assert.NotEmpty(t, resp.Header.Get("X-Request-Id"))
}

func TestE2E_DANodeThroughProxy(t *testing.T) {
	srv := setupE2EServer(t)
	defer srv.Close()

	resp := e2eRequest(t, srv.URL, testToken, jsonRPCRequest("node.Info", []interface{}{}))
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var rpcResp map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &rpcResp))
	result := rpcResp["result"].(map[string]interface{})
	assert.Contains(t, result, "type")
	assert.Contains(t, result, "api_version")
	assert.Contains(t, resp.Header.Get("X-DA-Backend"), "celestia-node-rpc")
}

func TestE2E_HeaderNetworkHeadThroughProxy(t *testing.T) {
	srv := setupE2EServer(t)
	defer srv.Close()

	resp := e2eRequest(t, srv.URL, testToken, jsonRPCRequest("header.NetworkHead", []interface{}{}))
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var rpcResp map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &rpcResp))
	result := rpcResp["result"].(map[string]interface{})
	header := result["header"].(map[string]interface{})
	assert.Equal(t, "mocha-4", header["chain_id"])
}

// --- E2E: Authentication ---

func TestE2E_InvalidToken(t *testing.T) {
	srv := setupE2EServer(t)
	defer srv.Close()

	resp := e2eRequest(t, srv.URL, "invalid-token-that-does-not-exist", jsonRPCRequest("status", []interface{}{}))
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestE2E_DisabledToken(t *testing.T) {
	srv := setupE2EServer(t)
	defer srv.Close()

	resp := e2eRequest(t, srv.URL, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", jsonRPCRequest("status", []interface{}{}))
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestE2E_MissingToken(t *testing.T) {
	srv := setupE2EServer(t)
	defer srv.Close()

	// Request to root without token segment.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(jsonRPCRequest("status", []interface{}{})))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	// The auth middleware treats the first path segment as the token.
	// "/" has no path segment, so we expect 401.
	// However, "/" will be treated as empty token -> 401.
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// --- E2E: Request ID ---

func TestE2E_RequestIDGenerated(t *testing.T) {
	srv := setupE2EServer(t)
	defer srv.Close()

	resp := e2eRequest(t, srv.URL, testToken, jsonRPCRequest("status", []interface{}{}))
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	reqID := resp.Header.Get("X-Request-Id")
	assert.NotEmpty(t, reqID)
	assert.Len(t, reqID, 36, "should be a UUID")
}

func TestE2E_RequestIDPreserved(t *testing.T) {
	srv := setupE2EServer(t)
	defer srv.Close()

	url := fmt.Sprintf("%s/%s/", srv.URL, testToken)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonRPCRequest("status", []interface{}{})))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "my-custom-id-12345")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "my-custom-id-12345", resp.Header.Get("X-Request-Id"))
}

// --- E2E: Multiple methods in sequence ---

func TestE2E_MultipleMethodsSequence(t *testing.T) {
	srv := setupE2EServer(t)
	defer srv.Close()

	methods := []struct {
		method          string
		params          interface{}
		expectedBackend string
	}{
		{"status", []interface{}{}, "celestia-app-rpc"},
		{"node.Info", []interface{}{}, "celestia-node-rpc"},
		{"health", []interface{}{}, "celestia-app-rpc"},
		{"header.NetworkHead", []interface{}{}, "celestia-node-rpc"},
		{"p2p.Info", []interface{}{}, "celestia-node-rpc"},
		{"net_info", []interface{}{}, "celestia-app-rpc"},
		{"state.AccountAddress", []interface{}{}, "celestia-node-rpc"},
	}

	for _, tc := range methods {
		t.Run(tc.method, func(t *testing.T) {
			resp := e2eRequest(t, srv.URL, testToken, jsonRPCRequest(tc.method, tc.params))
			defer func() { _ = resp.Body.Close() }()

			require.Equal(t, http.StatusOK, resp.StatusCode,
				"method %s should return 200", tc.method)
			assert.Contains(t, resp.Header.Get("X-DA-Backend"), tc.expectedBackend,
				"method %s should route to %s", tc.method, tc.expectedBackend)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.True(t, json.Valid(body),
				"response for %s should be valid JSON", tc.method)
		})
	}
}

// --- E2E: Batch request through proxy ---

func TestE2E_BatchRequestThroughProxy(t *testing.T) {
	srv := setupE2EServer(t)
	defer srv.Close()

	batch := []map[string]interface{}{
		{"jsonrpc": "2.0", "id": 1, "method": "status", "params": []interface{}{}},
		{"jsonrpc": "2.0", "id": 2, "method": "health", "params": []interface{}{}},
	}
	body, _ := json.Marshal(batch)

	resp := e2eRequest(t, srv.URL, testToken, body)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var batchResp []map[string]interface{}
	require.NoError(t, json.Unmarshal(respBody, &batchResp))
	require.Len(t, batchResp, 2)
	assert.NotNil(t, batchResp[0]["result"])
	assert.NotNil(t, batchResp[1]["result"])
}

// --- E2E: Token is stripped from path ---

func TestE2E_TokenStrippedFromPath(t *testing.T) {
	srv := setupE2EServer(t)
	defer srv.Close()

	// A Tendermint RPC HTTP GET request — the path after token should reach backend.
	url := fmt.Sprintf("%s/%s/health", srv.URL, testToken)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	// Tendermint /health endpoint should return 200 via GET.
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var rpcResp map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &rpcResp))
	assert.NotNil(t, rpcResp["result"])
}
