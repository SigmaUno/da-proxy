package tracing

import (
	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"

	"go.opentelemetry.io/otel"

	"github.com/SigmaUno/da-proxy/internal/middleware"
)

// Middleware returns Echo middleware that creates spans for each request
// and propagates trace context to backends.
func Middleware() echo.MiddlewareFunc {
	tracer := Tracer()
	propagator := otel.GetTextMapPropagator()

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()

			// Extract incoming trace context.
			ctx := propagator.Extract(req.Context(), propagation.HeaderCarrier(req.Header))

			spanName := req.Method + " " + req.URL.Path
			ctx, span := tracer.Start(ctx, spanName,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					semconv.HTTPMethod(req.Method),
					semconv.HTTPTarget(req.URL.Path),
					semconv.HTTPScheme(req.URL.Scheme),
					attribute.String("http.user_agent", req.UserAgent()),
				),
			)
			defer span.End()

			// Store the span context for downstream use.
			c.SetRequest(req.WithContext(ctx))

			// Execute the handler.
			err := next(c)

			// Record response attributes.
			status := c.Response().Status
			span.SetAttributes(semconv.HTTPStatusCode(status))

			if backend, ok := c.Get(middleware.ContextKeyBackend).(string); ok {
				span.SetAttributes(attribute.String("da.backend", backend))
			}
			if method, ok := c.Get(middleware.ContextKeyRPCMethod).(string); ok {
				span.SetAttributes(attribute.String("da.rpc_method", method))
			}
			if tokenName, ok := c.Get(middleware.ContextKeyTokenName).(string); ok {
				span.SetAttributes(attribute.String("da.token_name", tokenName))
			}

			// Add trace ID to response header for correlation.
			traceID := span.SpanContext().TraceID()
			if traceID.IsValid() {
				c.Response().Header().Set("X-Trace-ID", traceID.String())
			}

			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			} else if status >= 500 {
				span.SetStatus(codes.Error, "server error")
			}

			return err
		}
	}
}
