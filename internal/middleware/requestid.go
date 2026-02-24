package middleware

import (
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// HeaderXRequestID is the HTTP header used to propagate request IDs.
const HeaderXRequestID = "X-Request-ID"

// RequestID returns Echo middleware that assigns a UUID to each request.
// If the incoming request already has an X-Request-ID header, it is preserved.
func RequestID() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			id := c.Request().Header.Get(HeaderXRequestID)
			if id == "" {
				id = uuid.New().String()
			}

			c.Set(ContextKeyRequestID, id)
			c.Response().Header().Set(HeaderXRequestID, id)

			return next(c)
		}
	}
}
