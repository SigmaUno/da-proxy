package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/SigmaUno/da-proxy/internal/middleware"
)

func setupTestTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return exporter
}

func TestMiddleware_CreatesSpan(t *testing.T) {
	exporter := setupTestTracer(t)

	e := echo.New()
	e.Use(Middleware())
	e.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /test", spans[0].Name)
}

func TestMiddleware_RecordsBackend(t *testing.T) {
	exporter := setupTestTracer(t)

	e := echo.New()
	e.Use(Middleware())
	e.GET("/test", func(c echo.Context) error {
		c.Set(middleware.ContextKeyBackend, "celestia-node-rpc")
		c.Set(middleware.ContextKeyRPCMethod, "blob.Get")
		c.Set(middleware.ContextKeyTokenName, "test-token")
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	attrs := spans[0].Attributes
	found := make(map[string]string)
	for _, attr := range attrs {
		found[string(attr.Key)] = attr.Value.AsString()
	}
	assert.Equal(t, "celestia-node-rpc", found["da.backend"])
	assert.Equal(t, "blob.Get", found["da.rpc_method"])
	assert.Equal(t, "test-token", found["da.token_name"])
}

func TestMiddleware_SetsTraceIDHeader(t *testing.T) {
	_ = setupTestTracer(t)

	e := echo.New()
	e.Use(Middleware())
	e.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	traceID := rec.Header().Get("X-Trace-ID")
	assert.NotEmpty(t, traceID)
	assert.Len(t, traceID, 32) // 16 bytes hex
}

func TestMiddleware_RecordsError(t *testing.T) {
	exporter := setupTestTracer(t)

	e := echo.New()
	e.Use(Middleware())
	e.GET("/error", func(_ echo.Context) error {
		return echo.NewHTTPError(http.StatusInternalServerError, "internal error")
	})

	req := httptest.NewRequest(http.MethodGet, "/error", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Len(t, spans[0].Events, 1) // error event recorded
}

func TestInit_Disabled(t *testing.T) {
	cfg := Config{Enabled: false}
	shutdown, err := Init(context.Background(), cfg, "test")
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	assert.NoError(t, shutdown(context.Background()))
}

func TestTracer_ReturnsNamedTracer(t *testing.T) {
	tracer := Tracer()
	assert.NotNil(t, tracer)
}
