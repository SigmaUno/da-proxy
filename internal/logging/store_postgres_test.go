package logging

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPgBuildQuery_BasicFilters(t *testing.T) {
	filter := LogFilter{
		Method:    "blob.Get",
		TokenName: "test-token",
		Backend:   "celestia-node-rpc",
		Limit:     50,
	}

	query, args := pgBuildQuery("SELECT id, timestamp, request_id, token_name, method, backend, status_code, latency_ms, request_bytes, response_bytes, client_ip, error, path FROM request_logs", filter)

	assert.Contains(t, query, "method = $1")
	assert.Contains(t, query, "token_name = $2")
	assert.Contains(t, query, "backend = $3")
	assert.Contains(t, query, "ORDER BY id ASC")
	assert.Contains(t, query, "LIMIT 50")
	assert.Equal(t, 3, len(args))
	assert.Equal(t, "blob.Get", args[0])
	assert.Equal(t, "test-token", args[1])
	assert.Equal(t, "celestia-node-rpc", args[2])
}

func TestPgBuildQuery_TimeRange(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)

	filter := LogFilter{
		From: from,
		To:   to,
	}

	query, args := pgBuildQuery("SELECT id, timestamp, request_id, token_name, method, backend, status_code, latency_ms, request_bytes, response_bytes, client_ip, error, path FROM request_logs", filter)

	assert.Contains(t, query, "timestamp >= $1")
	assert.Contains(t, query, "timestamp <= $2")
	assert.Equal(t, 2, len(args))
}

func TestPgBuildQuery_Cursor(t *testing.T) {
	filter := LogFilter{
		Cursor:   100,
		SortDesc: true,
	}

	query, args := pgBuildQuery("SELECT id, timestamp, request_id, token_name, method, backend, status_code, latency_ms, request_bytes, response_bytes, client_ip, error, path FROM request_logs", filter)

	assert.Contains(t, query, "id < $1")
	assert.Contains(t, query, "ORDER BY id DESC")
	assert.Equal(t, int64(100), args[0])
}

func TestPgBuildQuery_Count(t *testing.T) {
	filter := LogFilter{
		Method: "status",
	}

	query, args := pgBuildQuery("SELECT COUNT(*) FROM request_logs", filter)

	assert.Contains(t, query, "method = $1")
	assert.NotContains(t, query, "ORDER BY")
	assert.NotContains(t, query, "LIMIT")
	assert.Equal(t, 1, len(args))
}

func TestPgBuildQuery_StatusRange(t *testing.T) {
	filter := LogFilter{
		StatusMin: 400,
		StatusMax: 599,
	}

	query, args := pgBuildQuery("SELECT id, timestamp, request_id, token_name, method, backend, status_code, latency_ms, request_bytes, response_bytes, client_ip, error, path FROM request_logs", filter)

	assert.Contains(t, query, "status_code >= $1")
	assert.Contains(t, query, "status_code <= $2")
	assert.Equal(t, 2, len(args))
	assert.Equal(t, 400, args[0])
	assert.Equal(t, 599, args[1])
}

func TestPgBuildQuery_DefaultLimit(t *testing.T) {
	filter := LogFilter{}

	query, _ := pgBuildQuery("SELECT id, timestamp, request_id, token_name, method, backend, status_code, latency_ms, request_bytes, response_bytes, client_ip, error, path FROM request_logs", filter)

	assert.Contains(t, query, "LIMIT 100")
}

func TestPgBuildQuery_MaxLimit(t *testing.T) {
	filter := LogFilter{Limit: 5000}

	query, _ := pgBuildQuery("SELECT id, timestamp, request_id, token_name, method, backend, status_code, latency_ms, request_bytes, response_bytes, client_ip, error, path FROM request_logs", filter)

	assert.Contains(t, query, "LIMIT 1000")
}
