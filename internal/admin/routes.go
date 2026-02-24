package admin

import "github.com/labstack/echo/v4"

func registerRoutes(e *echo.Echo, h *handlers) {
	api := e.Group("/admin/api")

	api.GET("/health", h.handleHealth)
	api.GET("/status", h.handleStatus)
	api.GET("/logs", h.handleLogs)
	api.GET("/logs/stream", h.handleLogsStream)
	api.GET("/logs/export", h.handleLogsExport)
	api.GET("/metrics/summary", h.handleMetricsSummary)
	api.GET("/backends", h.handleBackends)

	// Token management.
	api.GET("/tokens", h.handleListTokens)
	api.POST("/tokens", h.handleCreateToken)
	api.GET("/tokens/:id", h.handleGetToken)
	api.PUT("/tokens/:id", h.handleUpdateToken)
	api.DELETE("/tokens/:id", h.handleDeleteToken)
	api.POST("/tokens/:id/rotate", h.handleRotateToken)
}
