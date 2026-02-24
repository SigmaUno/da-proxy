package admin

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/SigmaUno/da-proxy/internal/logging"
)

type handlers struct {
	deps Dependencies
}

func (h *handlers) handleHealth(c echo.Context) error {
	if h.deps.HealthChecker == nil {
		return c.JSON(http.StatusOK, map[string]string{"status": "health checker not configured"})
	}
	return c.JSON(http.StatusOK, h.deps.HealthChecker.Status())
}

func (h *handlers) handleStatus(c echo.Context) error {
	activeTokens := 0
	if h.deps.Config != nil {
		for _, t := range h.deps.Config.Tokens {
			if t.Enabled {
				activeTokens++
			}
		}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"version":        h.deps.Version,
		"uptime_seconds": time.Since(h.deps.StartTime).Seconds(),
		"active_tokens":  activeTokens,
	})
}

func (h *handlers) handleLogs(c echo.Context) error {
	filter := parseLogFilter(c)

	var entries []logging.LogEntry
	var err error

	if h.deps.LogStore != nil {
		entries, err = h.deps.LogStore.Query(filter)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to query logs")
		}
	} else if h.deps.LogBuffer != nil {
		entries = h.deps.LogBuffer.Entries(filter.Limit)
	}

	var total int64
	if h.deps.LogStore != nil {
		total, _ = h.deps.LogStore.Count(filter)
	}

	var cursor int64
	if len(entries) > 0 {
		// Use a simple index-based cursor.
		cursor = int64(len(entries))
	}

	// Ensure non-nil slice for JSON serialization.
	if entries == nil {
		entries = []logging.LogEntry{}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"logs":     entries,
		"has_more": int64(len(entries)) < total,
		"total":    total,
		"cursor":   cursor,
	})
}

func (h *handlers) handleLogsStream(c echo.Context) error {
	if h.deps.LogBuffer == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "log buffer not available")
	}

	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	ch, cancel := h.deps.LogBuffer.Subscribe()
	defer cancel()

	ctx := c.Request().Context()
	for {
		select {
		case entry, ok := <-ch:
			if !ok {
				return nil
			}
			data := fmt.Sprintf("data: {\"request_id\":%q,\"method\":%q,\"backend\":%q,\"status_code\":%d,\"latency_ms\":%.2f}\n\n",
				entry.RequestID, entry.Method, entry.Backend, entry.StatusCode, entry.LatencyMs)
			if _, err := c.Response().Write([]byte(data)); err != nil {
				return nil
			}
			c.Response().Flush()
		case <-ctx.Done():
			return nil
		}
	}
}

func (h *handlers) handleLogsExport(c echo.Context) error {
	filter := parseLogFilter(c)
	if filter.Limit == 0 {
		filter.Limit = 1000
	}

	var entries []logging.LogEntry
	if h.deps.LogStore != nil {
		var err error
		entries, err = h.deps.LogStore.Query(filter)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to query logs")
		}
	}

	format := c.QueryParam("format")
	if format == "csv" {
		c.Response().Header().Set("Content-Type", "text/csv")
		c.Response().Header().Set("Content-Disposition", "attachment; filename=logs.csv")
		writer := csv.NewWriter(c.Response())
		_ = writer.Write([]string{"timestamp", "request_id", "token_name", "method", "backend", "status_code", "latency_ms", "client_ip", "error"})
		for _, e := range entries {
			_ = writer.Write([]string{
				e.Timestamp.Format(time.RFC3339),
				e.RequestID,
				e.TokenName,
				e.Method,
				e.Backend,
				strconv.Itoa(e.StatusCode),
				fmt.Sprintf("%.2f", e.LatencyMs),
				e.ClientIP,
				e.Error,
			})
		}
		writer.Flush()
		return nil
	}

	return c.JSON(http.StatusOK, entries)
}

func (h *handlers) handleMetricsSummary(c echo.Context) error {
	window := c.QueryParam("window")
	if window == "" {
		window = "24h"
	}

	// For Phase 1, return data from the log store.
	var duration time.Duration
	switch window {
	case "1h":
		duration = time.Hour
	case "7d":
		duration = 7 * 24 * time.Hour
	default:
		duration = 24 * time.Hour
		window = "24h"
	}

	filter := logging.LogFilter{
		From:     time.Now().Add(-duration),
		Limit:    0, // count only
		SortDesc: true,
	}

	var totalRequests int64
	if h.deps.LogStore != nil {
		totalRequests, _ = h.deps.LogStore.Count(filter)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"window":         window,
		"total_requests": totalRequests,
	})
}

func parseLogFilter(c echo.Context) logging.LogFilter {
	f := logging.LogFilter{
		Method:    c.QueryParam("method"),
		TokenName: c.QueryParam("token_name"),
		Backend:   c.QueryParam("backend"),
		SortDesc:  c.QueryParam("sort") != "asc",
	}

	if v := c.QueryParam("status_code"); v != "" {
		f.StatusCode, _ = strconv.Atoi(v)
	}
	if v := c.QueryParam("status_min"); v != "" {
		f.StatusMin, _ = strconv.Atoi(v)
	}
	if v := c.QueryParam("status_max"); v != "" {
		f.StatusMax, _ = strconv.Atoi(v)
	}
	if v := c.QueryParam("from"); v != "" {
		f.From, _ = time.Parse(time.RFC3339, v)
	}
	if v := c.QueryParam("to"); v != "" {
		f.To, _ = time.Parse(time.RFC3339, v)
	}
	if v := c.QueryParam("latency_min"); v != "" {
		f.LatencyMin, _ = strconv.ParseFloat(v, 64)
	}
	if v := c.QueryParam("limit"); v != "" {
		f.Limit, _ = strconv.Atoi(v)
	}
	if v := c.QueryParam("cursor"); v != "" {
		f.Cursor, _ = strconv.ParseInt(v, 10, 64)
	}

	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}

	return f
}
