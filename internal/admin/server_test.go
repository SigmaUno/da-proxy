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

func setupTestServer(t *testing.T) *Server {
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
			Tokens: []config.TokenConfig{
				{Token: "abc", Name: "t1", Enabled: true},
				{Token: "def", Name: "t2", Enabled: false},
			},
		},
		Logger:    zap.NewNop(),
		StartTime: time.Now(),
		Version:   "test-v1",
	}

	return NewServer(cfg, deps)
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
