package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestID_GeneratesUUID(t *testing.T) {
	e := echo.New()
	e.Use(RequestID())

	var ctxID string
	e.GET("/", func(c echo.Context) error {
		ctxID = c.Get(ContextKeyRequestID).(string)
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Response header should have the ID
	respID := rec.Header().Get(HeaderXRequestID)
	assert.NotEmpty(t, respID)

	// Should be a valid UUID
	_, err := uuid.Parse(respID)
	require.NoError(t, err)

	// Context should have the same ID
	assert.Equal(t, respID, ctxID)
}

func TestRequestID_PreservesExisting(t *testing.T) {
	e := echo.New()
	e.Use(RequestID())

	existingID := "existing-request-id-12345"
	var ctxID string
	e.GET("/", func(c echo.Context) error {
		ctxID = c.Get(ContextKeyRequestID).(string)
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(HeaderXRequestID, existingID)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, existingID, rec.Header().Get(HeaderXRequestID))
	assert.Equal(t, existingID, ctxID)
}

func TestRequestID_UniquePerRequest(t *testing.T) {
	e := echo.New()
	e.Use(RequestID())
	e.GET("/", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		id := rec.Header().Get(HeaderXRequestID)
		assert.NotEmpty(t, id)
		assert.False(t, ids[id], "duplicate request ID generated")
		ids[id] = true
	}
}
