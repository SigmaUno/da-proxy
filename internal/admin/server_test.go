package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/SigmaUno/da-proxy/internal/logging"
)

func testPasswordHash(t *testing.T) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("testpass"), bcrypt.MinCost)
	require.NoError(t, err)
	return string(hash)
}

type testServerResult struct {
	server *Server
	store  logging.Store
}

func setupTestServer(t *testing.T) *Server {
	t.Helper()
	return setupTestServerWithDeps(t).server
}

func setupTestServerWithDeps(t *testing.T) testServerResult {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := logging.NewSQLiteStore(dbPath, 24*time.Hour)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	ringBuf := logging.NewRingBuffer(100)

	cfg := config.AdminConfig{
		Listen:       ":0",
		Username:     "admin",
		PasswordHash: testPasswordHash(t),
		CORSOrigins:  []string{"http://localhost:3000"},
	}

	deps := Dependencies{
		LogBuffer: ringBuf,
		LogStore:  store,
		Config: &config.Config{
			Backends: config.BackendsConfig{
				CelestiaAppRPC:  config.Endpoints{"http://127.0.0.1:26657"},
				CelestiaNodeRPC: config.Endpoints{"http://127.0.0.1:26658"},
			},
			Tokens: []config.TokenConfig{
				{Token: "abc", Name: "t1", Enabled: true},
				{Token: "def", Name: "t2", Enabled: false},
			},
		},
		Logger:    zap.NewNop(),
		StartTime: time.Now(),
		Version:   "test-v1",
	}

	return testServerResult{
		server: NewServer(cfg, deps),
		store:  store,
	}
}

func doAuthRequest(t *testing.T, s *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.SetBasicAuth("admin", "testpass")
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	return rec
}

func TestAdmin_Unauthenticated(t *testing.T) {
	s := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/health", nil)
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAdmin_WrongPassword(t *testing.T) {
	s := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/health", nil)
	req.SetBasicAuth("admin", "wrongpass")
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAdmin_HealthEndpoint(t *testing.T) {
	s := setupTestServer(t)
	rec := doAuthRequest(t, s, http.MethodGet, "/admin/api/health")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
}

func TestAdmin_StatusEndpoint(t *testing.T) {
	s := setupTestServer(t)
	rec := doAuthRequest(t, s, http.MethodGet, "/admin/api/status")

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)

	assert.Equal(t, "test-v1", body["version"])
	assert.Equal(t, float64(1), body["active_tokens"]) // only 1 enabled
	assert.NotNil(t, body["uptime_seconds"])
}

func TestAdmin_LogsEndpoint(t *testing.T) {
	s := setupTestServer(t)
	rec := doAuthRequest(t, s, http.MethodGet, "/admin/api/logs")

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)

	assert.NotNil(t, body["logs"])
	assert.NotNil(t, body["total"])
}

func TestAdmin_LogsExportJSON(t *testing.T) {
	s := setupTestServer(t)
	rec := doAuthRequest(t, s, http.MethodGet, "/admin/api/logs/export")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
}

func TestAdmin_LogsExportCSV(t *testing.T) {
	s := setupTestServer(t)
	rec := doAuthRequest(t, s, http.MethodGet, "/admin/api/logs/export?format=csv")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/csv")
	assert.Contains(t, rec.Body.String(), "timestamp,request_id")
}

func TestAdmin_MetricsSummary(t *testing.T) {
	s := setupTestServer(t)
	rec := doAuthRequest(t, s, http.MethodGet, "/admin/api/metrics/summary?window=1h")

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)

	assert.Equal(t, "1h", body["window"])
	assert.NotNil(t, body["total_requests"])
}

func TestAdmin_CORSHeaders(t *testing.T) {
	s := setupTestServer(t)

	req := httptest.NewRequest(http.MethodOptions, "/admin/api/health", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)

	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Origin"), "http://localhost:3000")
}

func TestAdmin_BackendsEndpoint(t *testing.T) {
	s := setupTestServer(t)
	rec := doAuthRequest(t, s, http.MethodGet, "/admin/api/backends")

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)

	backends, ok := body["backends"].([]interface{})
	require.True(t, ok)
	// 2 backends configured (archival not set so omitted)
	assert.Len(t, backends, 2)

	// Verify first backend structure.
	first := backends[0].(map[string]interface{})
	assert.Equal(t, "celestia-app-rpc", first["name"])
	assert.NotNil(t, first["endpoints"])
	assert.NotNil(t, first["methods"])
}

func TestAdmin_BackendsWithStats(t *testing.T) {
	ts := setupTestServerWithDeps(t)
	s := ts.server

	// Push some log entries to build stats.
	store := ts.store
	entries := []logging.LogEntry{
		{Timestamp: time.Now(), RequestID: "r1", TokenName: "t", Method: "blob.Get",
			Backend: "celestia-node-rpc", StatusCode: 200, LatencyMs: 40, ClientIP: "1.1.1.1"},
		{Timestamp: time.Now(), RequestID: "r2", TokenName: "t", Method: "blob.Submit",
			Backend: "celestia-node-rpc", StatusCode: 200, LatencyMs: 60, ClientIP: "1.1.1.1"},
		{Timestamp: time.Now(), RequestID: "r3", TokenName: "t", Method: "status",
			Backend: "celestia-app-rpc", StatusCode: 200, LatencyMs: 20, ClientIP: "1.1.1.1"},
	}
	for _, e := range entries {
		store.Push(e)
	}

	// Wait for batch flush.
	time.Sleep(2 * time.Second)

	rec := doAuthRequest(t, s, http.MethodGet, "/admin/api/backends")
	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)

	backends := body["backends"].([]interface{})

	// Build a map by name for easier assertions.
	byName := make(map[string]map[string]interface{})
	for _, b := range backends {
		bm := b.(map[string]interface{})
		byName[bm["name"].(string)] = bm
	}

	// celestia-node-rpc: avg_latency = 50, total = 2, methods = [blob.Get, blob.Submit]
	nodeRPC := byName["celestia-node-rpc"]
	assert.InDelta(t, 50.0, nodeRPC["avg_latency_ms"].(float64), 0.1)
	assert.Equal(t, float64(2), nodeRPC["total_requests"].(float64))
	methods := nodeRPC["methods"].([]interface{})
	assert.Len(t, methods, 2)

	// celestia-app-rpc: avg_latency = 20, total = 1, methods = [status]
	appRPC := byName["celestia-app-rpc"]
	assert.InDelta(t, 20.0, appRPC["avg_latency_ms"].(float64), 0.1)
	assert.Equal(t, float64(1), appRPC["total_requests"].(float64))
}

func TestAdmin_BackendsWindowParam(t *testing.T) {
	s := setupTestServer(t)
	rec := doAuthRequest(t, s, http.MethodGet, "/admin/api/backends?window=1h")

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.NotNil(t, body["backends"])
}
