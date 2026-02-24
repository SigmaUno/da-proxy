package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/SigmaUno/da-proxy/internal/logging"
)

type mockSink struct {
	mu      sync.Mutex
	entries []logging.LogEntry
}

func (s *mockSink) Push(entry logging.LogEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry)
}

func (s *mockSink) getEntries() []logging.LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]logging.LogEntry, len(s.entries))
	copy(cp, s.entries)
	return cp
}

func TestAccessLogger_LogsRequest(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)
	sink := &mockSink{}

	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(ContextKeyRequestID, "test-req-id")
			c.Set(ContextKeyTokenName, "test-token")
			c.Set(ContextKeyRPCMethod, "blob.Get")
			c.Set(ContextKeyBackend, "celestia-node-rpc")
			return next(c)
		}
	})
	e.Use(AccessLogger(logger, sink))
	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Check Zap log output.
	require.Equal(t, 1, logs.Len())
	logEntry := logs.All()[0]
	assert.Equal(t, "request_complete", logEntry.Message)

	fields := logEntry.ContextMap()
	assert.Equal(t, "test-req-id", fields["request_id"])
	assert.Equal(t, "test-token", fields["token_name"])
	assert.Equal(t, "blob.Get", fields["method"])
	assert.Equal(t, "celestia-node-rpc", fields["backend"])
	assert.Equal(t, int64(200), fields["status"])
	assert.NotNil(t, fields["latency_ms"])

	// Check sink received entry.
	entries := sink.getEntries()
	require.Len(t, entries, 1)
	assert.Equal(t, "test-req-id", entries[0].RequestID)
	assert.Equal(t, "blob.Get", entries[0].Method)
	assert.Equal(t, 200, entries[0].StatusCode)
}

func TestAccessLogger_NoTokenInLogs(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(ContextKeyTokenName, "test-token")
			return next(c)
		}
	})
	e.Use(AccessLogger(logger))
	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	// The raw token value should never appear — only token_name.
	logEntry := logs.All()[0]
	fields := logEntry.ContextMap()
	assert.Equal(t, "test-token", fields["token_name"])
	// No field should contain a raw token pattern.
	for key := range fields {
		assert.NotEqual(t, "token", key)
	}
}

func TestAccessLogger_MultipleSinks(t *testing.T) {
	logger := zap.NewNop()
	sink1 := &mockSink{}
	sink2 := &mockSink{}

	e := echo.New()
	e.Use(AccessLogger(logger, sink1, sink2))
	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Len(t, sink1.getEntries(), 1)
	assert.Len(t, sink2.getEntries(), 1)
}
