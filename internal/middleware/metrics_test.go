package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SigmaUno/da-proxy/internal/metrics"
)

func TestMetricsMiddleware_RecordsMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)

	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(ContextKeyRPCMethod, "blob.Get")
			c.Set(ContextKeyBackend, "celestia-node-rpc")
			c.Set(ContextKeyTokenName, "test-service")
			return next(c)
		}
	})
	e.Use(MetricsMiddleware(m))
	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	families, err := reg.Gather()
	require.NoError(t, err)

	var foundCounter, foundHistogram bool
	for _, fam := range families {
		switch fam.GetName() {
		case "daproxy_requests_total":
			foundCounter = true
			require.Len(t, fam.GetMetric(), 1)
			assert.Equal(t, float64(1), fam.GetMetric()[0].GetCounter().GetValue())
		case "daproxy_request_duration_seconds":
			foundHistogram = true
			require.Len(t, fam.GetMetric(), 1)
			assert.Equal(t, uint64(1), fam.GetMetric()[0].GetHistogram().GetSampleCount())
		}
	}
	assert.True(t, foundCounter, "daproxy_requests_total not found")
	assert.True(t, foundHistogram, "daproxy_request_duration_seconds not found")
}

func TestMetricsMiddleware_NoContextValues(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)

	e := echo.New()
	e.Use(MetricsMiddleware(m))
	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Should still record metrics with empty labels.
	families, err := reg.Gather()
	require.NoError(t, err)
	found := false
	for _, fam := range families {
		if fam.GetName() == "daproxy_requests_total" {
			found = true
		}
	}
	assert.True(t, found)
}
