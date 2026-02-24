package middleware

import (
	"time"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/logging"
)

// LogSink receives log entries for storage.
type LogSink interface {
	Push(entry logging.LogEntry)
}

// AccessLogger returns Echo middleware that logs every request via Zap
// and pushes entries to the provided sinks (ring buffer, SQLite, etc.).
func AccessLogger(logger *zap.Logger, sinks ...LogSink) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()

			err := next(c)

			latency := time.Since(start)
			latencyMs := float64(latency.Nanoseconds()) / 1e6

			req := c.Request()
			resp := c.Response()

			requestID := stringFromCtx(c, ContextKeyRequestID)
			tokenName := stringFromCtx(c, ContextKeyTokenName)
			method := stringFromCtx(c, ContextKeyRPCMethod)
			backend := stringFromCtx(c, ContextKeyBackend)

			status := resp.Status
			var errMsg string
			if err != nil {
				if he, ok := err.(*echo.HTTPError); ok {
					status = he.Code
					if he.Message != nil {
						errMsg, _ = he.Message.(string)
					}
				} else {
					errMsg = err.Error()
				}
			}

			// Structured Zap log with typed fields (zero-allocation hot path).
			logger.Info("request_complete",
				zap.String("request_id", requestID),
				zap.String("token_name", tokenName),
				zap.String("method", method),
				zap.String("backend", backend),
				zap.Int("status", status),
				zap.Float64("latency_ms", latencyMs),
				zap.Int64("request_bytes", req.ContentLength),
				zap.Int64("response_bytes", resp.Size),
				zap.String("client_ip", c.RealIP()),
				zap.String("path", req.URL.Path),
			)

			entry := logging.LogEntry{
				Timestamp:     start,
				RequestID:     requestID,
				TokenName:     tokenName,
				Method:        method,
				Backend:       backend,
				StatusCode:    status,
				LatencyMs:     latencyMs,
				RequestBytes:  req.ContentLength,
				ResponseBytes: resp.Size,
				ClientIP:      c.RealIP(),
				Error:         errMsg,
				Path:          req.URL.Path,
			}

			// Push to sinks.
			for _, sink := range sinks {
				if sink != nil {
					sink.Push(entry)
				}
			}

			return err
		}
	}
}
