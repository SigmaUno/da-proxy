package middleware

import (
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/SigmaUno/da-proxy/internal/metrics"
)

// MetricsMiddleware returns Echo middleware that records Prometheus metrics
// for each proxied request.
func MetricsMiddleware(m *metrics.Metrics) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()

			err := next(c)

			duration := time.Since(start)
			status := c.Response().Status
			statusCode := strconv.Itoa(status)

			method := stringFromCtx(c, ContextKeyRPCMethod)
			backend := stringFromCtx(c, ContextKeyBackend)
			tokenName := stringFromCtx(c, ContextKeyTokenName)

			m.RequestsTotal.With(prometheus.Labels{
				"method":      method,
				"backend":     backend,
				"token_name":  tokenName,
				"status_code": statusCode,
			}).Inc()

			m.RequestDuration.With(prometheus.Labels{
				"method":     method,
				"backend":    backend,
				"token_name": tokenName,
			}).Observe(duration.Seconds())

			if method != "" && backend != "" {
				m.RequestSize.With(prometheus.Labels{
					"method":  method,
					"backend": backend,
				}).Observe(float64(c.Request().ContentLength))

				m.ResponseSize.With(prometheus.Labels{
					"method":  method,
					"backend": backend,
				}).Observe(float64(c.Response().Size))
			}

			return err
		}
	}
}

func stringFromCtx(c echo.Context, key string) string {
	val := c.Get(key)
	if val == nil {
		return ""
	}
	s, _ := val.(string)
	return s
}
