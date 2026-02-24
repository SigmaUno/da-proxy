package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/auth"
	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/SigmaUno/da-proxy/internal/logging"
)

func setupTokenTestServer(t *testing.T) *Server {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := logging.NewSQLiteStore(dbPath, 24*time.Hour)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	tokenStore, err := auth.NewSQLiteTokenStore(filepath.Join(tmpDir, "tokens.db"), 30*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tokenStore.Close() })

	ringBuf := logging.NewRingBuffer(100)

	cfg := config.AdminConfig{
		Listen:       ":0",
		Username:     "admin",
		PasswordHash: testPasswordHash(t),
		CORSOrigins:  []string{"http://localhost:3000"},
	}

	deps := Dependencies{
		LogBuffer:  ringBuf,
		LogStore:   store,
		TokenStore: tokenStore,
		Config:     &config.Config{},
		Logger:     zap.NewNop(),
		StartTime:  time.Now(),
		Version:    "test-v1",
	}

	return NewServer(cfg, deps)
}

func doTokenAuthRequest(t *testing.T, s *Server, method, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.SetBasicAuth("admin", "testpass")
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	return rec
}

func TestTokenHandlers_ListEmpty(t *testing.T) {
	s := setupTokenTestServer(t)
	rec := doTokenAuthRequest(t, s, http.MethodGet, "/admin/api/tokens", "")

	assert.Equal(t, http.StatusOK, rec.Code)

	var tokens []auth.Token
	err := json.Unmarshal(rec.Body.Bytes(), &tokens)
	require.NoError(t, err)
	assert.Empty(t, tokens)
}

func TestTokenHandlers_CreateAndGet(t *testing.T) {
	s := setupTokenTestServer(t)

	// Create token.
	rec := doTokenAuthRequest(t, s, http.MethodPost, "/admin/api/tokens",
		`{"name":"test-token","rate_limit":100,"scope":"write"}`)
	assert.Equal(t, http.StatusCreated, rec.Code)

	var result auth.CreateTokenResult
	err := json.Unmarshal(rec.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, "test-token", result.Name)
	assert.Equal(t, "write", result.Scope)
	assert.Equal(t, 100, result.RateLimit)
	assert.True(t, result.Enabled)
	assert.NotEmpty(t, result.PlaintextToken)
	assert.Equal(t, result.PlaintextToken[:8], result.TokenPrefix)

	// Get by ID.
	rec = doTokenAuthRequest(t, s, http.MethodGet,
		fmt.Sprintf("/admin/api/tokens/%d", result.ID), "")
	assert.Equal(t, http.StatusOK, rec.Code)

	var token auth.Token
	err = json.Unmarshal(rec.Body.Bytes(), &token)
	require.NoError(t, err)
	assert.Equal(t, "test-token", token.Name)
	assert.Equal(t, result.ID, token.ID)
}

func TestTokenHandlers_CreateRequiresName(t *testing.T) {
	s := setupTokenTestServer(t)
	rec := doTokenAuthRequest(t, s, http.MethodPost, "/admin/api/tokens", `{"rate_limit":10}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTokenHandlers_CreateInvalidScope(t *testing.T) {
	s := setupTokenTestServer(t)
	rec := doTokenAuthRequest(t, s, http.MethodPost, "/admin/api/tokens",
		`{"name":"bad","scope":"superadmin"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTokenHandlers_Update(t *testing.T) {
	s := setupTokenTestServer(t)

	// Create.
	rec := doTokenAuthRequest(t, s, http.MethodPost, "/admin/api/tokens",
		`{"name":"original","rate_limit":50,"scope":"write"}`)
	require.Equal(t, http.StatusCreated, rec.Code)

	var created auth.CreateTokenResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	// Update name and rate limit.
	rec = doTokenAuthRequest(t, s, http.MethodPut,
		fmt.Sprintf("/admin/api/tokens/%d", created.ID),
		`{"name":"updated","rate_limit":200}`)
	assert.Equal(t, http.StatusOK, rec.Code)

	var updated auth.Token
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	assert.Equal(t, "updated", updated.Name)
	assert.Equal(t, 200, updated.RateLimit)
	assert.Equal(t, "write", updated.Scope) // unchanged
}

func TestTokenHandlers_UpdateNotFound(t *testing.T) {
	s := setupTokenTestServer(t)
	rec := doTokenAuthRequest(t, s, http.MethodPut, "/admin/api/tokens/9999",
		`{"name":"nope"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTokenHandlers_Delete(t *testing.T) {
	s := setupTokenTestServer(t)

	// Create.
	rec := doTokenAuthRequest(t, s, http.MethodPost, "/admin/api/tokens",
		`{"name":"to-delete"}`)
	require.Equal(t, http.StatusCreated, rec.Code)

	var created auth.CreateTokenResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	// Delete.
	rec = doTokenAuthRequest(t, s, http.MethodDelete,
		fmt.Sprintf("/admin/api/tokens/%d", created.ID), "")
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Verify gone.
	rec = doTokenAuthRequest(t, s, http.MethodGet,
		fmt.Sprintf("/admin/api/tokens/%d", created.ID), "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTokenHandlers_DeleteNotFound(t *testing.T) {
	s := setupTokenTestServer(t)
	rec := doTokenAuthRequest(t, s, http.MethodDelete, "/admin/api/tokens/9999", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTokenHandlers_Rotate(t *testing.T) {
	s := setupTokenTestServer(t)

	// Create.
	rec := doTokenAuthRequest(t, s, http.MethodPost, "/admin/api/tokens",
		`{"name":"rotatable"}`)
	require.Equal(t, http.StatusCreated, rec.Code)

	var created auth.CreateTokenResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	// Rotate.
	rec = doTokenAuthRequest(t, s, http.MethodPost,
		fmt.Sprintf("/admin/api/tokens/%d/rotate", created.ID), "")
	assert.Equal(t, http.StatusOK, rec.Code)

	var rotated auth.RotateTokenResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rotated))

	assert.NotEqual(t, created.PlaintextToken, rotated.PlaintextToken)
	assert.NotEqual(t, created.TokenPrefix, rotated.TokenPrefix)
	assert.Equal(t, "rotatable", rotated.Name)
}

func TestTokenHandlers_RotateNotFound(t *testing.T) {
	s := setupTokenTestServer(t)
	rec := doTokenAuthRequest(t, s, http.MethodPost, "/admin/api/tokens/9999/rotate", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTokenHandlers_List(t *testing.T) {
	s := setupTokenTestServer(t)

	// Create two tokens.
	doTokenAuthRequest(t, s, http.MethodPost, "/admin/api/tokens", `{"name":"first"}`)
	doTokenAuthRequest(t, s, http.MethodPost, "/admin/api/tokens", `{"name":"second"}`)

	rec := doTokenAuthRequest(t, s, http.MethodGet, "/admin/api/tokens", "")
	assert.Equal(t, http.StatusOK, rec.Code)

	var tokens []auth.Token
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &tokens))
	assert.Len(t, tokens, 2)
}

func TestTokenHandlers_StatusWithTokenStore(t *testing.T) {
	s := setupTokenTestServer(t)

	// Create one enabled and one disabled token.
	doTokenAuthRequest(t, s, http.MethodPost, "/admin/api/tokens", `{"name":"enabled"}`)
	rec := doTokenAuthRequest(t, s, http.MethodPost, "/admin/api/tokens", `{"name":"disabled","enabled":false}`)
	require.Equal(t, http.StatusCreated, rec.Code)

	rec = doTokenAuthRequest(t, s, http.MethodGet, "/admin/api/status", "")
	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, float64(2), body["total_tokens"])
	assert.Equal(t, float64(1), body["active_tokens"])
}

func TestTokenHandlers_InvalidID(t *testing.T) {
	s := setupTokenTestServer(t)

	rec := doTokenAuthRequest(t, s, http.MethodGet, "/admin/api/tokens/abc", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = doTokenAuthRequest(t, s, http.MethodPut, "/admin/api/tokens/abc", `{"name":"x"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = doTokenAuthRequest(t, s, http.MethodDelete, "/admin/api/tokens/abc", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
