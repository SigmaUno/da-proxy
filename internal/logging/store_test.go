package logging

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewSQLiteStore(dbPath, 24*time.Hour)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSQLiteStore_PushAndQuery(t *testing.T) {
	store := newTestStore(t)

	entry := LogEntry{
		Timestamp:     time.Now(),
		RequestID:     "req-1",
		TokenName:     "test-token",
		Method:        "blob.Get",
		Backend:       "celestia-node-rpc",
		StatusCode:    200,
		LatencyMs:     42.5,
		RequestBytes:  256,
		ResponseBytes: 1024,
		ClientIP:      "127.0.0.1",
		Path:          "/",
	}
	store.Push(entry)

	// Wait for batch flush.
	time.Sleep(2 * time.Second)

	entries, err := store.Query(LogFilter{Limit: 10, SortDesc: true})
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "req-1", entries[0].RequestID)
	assert.Equal(t, "blob.Get", entries[0].Method)
	assert.Equal(t, 200, entries[0].StatusCode)
	assert.InDelta(t, 42.5, entries[0].LatencyMs, 0.01)
}

func TestSQLiteStore_QueryWithFilters(t *testing.T) {
	store := newTestStore(t)

	entries := []LogEntry{
		{Timestamp: time.Now(), RequestID: "r1", TokenName: "token-a", Method: "blob.Get", Backend: "celestia-node-rpc", StatusCode: 200, ClientIP: "1.1.1.1", Path: "/"},
		{Timestamp: time.Now(), RequestID: "r2", TokenName: "token-b", Method: "status", Backend: "celestia-app-rpc", StatusCode: 200, ClientIP: "2.2.2.2", Path: "/"},
		{Timestamp: time.Now(), RequestID: "r3", TokenName: "token-a", Method: "blob.Submit", Backend: "celestia-node-rpc", StatusCode: 500, ClientIP: "1.1.1.1", Path: "/", Error: "backend error"},
	}
	for _, e := range entries {
		store.Push(e)
	}

	time.Sleep(2 * time.Second)

	t.Run("filter by method", func(t *testing.T) {
		result, err := store.Query(LogFilter{Method: "blob.Get", SortDesc: true})
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, "blob.Get", result[0].Method)
	})

	t.Run("filter by token", func(t *testing.T) {
		result, err := store.Query(LogFilter{TokenName: "token-a", SortDesc: true})
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("filter by status code", func(t *testing.T) {
		result, err := store.Query(LogFilter{StatusCode: 500, SortDesc: true})
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, "blob.Submit", result[0].Method)
	})

	t.Run("filter by status range", func(t *testing.T) {
		result, err := store.Query(LogFilter{StatusMin: 400, StatusMax: 599, SortDesc: true})
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})

	t.Run("count", func(t *testing.T) {
		count, err := store.Count(LogFilter{})
		require.NoError(t, err)
		assert.Equal(t, int64(3), count)
	})

	t.Run("count with filter", func(t *testing.T) {
		count, err := store.Count(LogFilter{TokenName: "token-a"})
		require.NoError(t, err)
		assert.Equal(t, int64(2), count)
	})

	t.Run("limit", func(t *testing.T) {
		result, err := store.Query(LogFilter{Limit: 1, SortDesc: true})
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})
}

func TestSQLiteStore_ErrorField(t *testing.T) {
	store := newTestStore(t)

	store.Push(LogEntry{
		Timestamp: time.Now(), RequestID: "r1", TokenName: "t", Method: "m",
		Backend: "b", StatusCode: 500, ClientIP: "1.1.1.1",
		Error: "something went wrong",
	})

	time.Sleep(2 * time.Second)

	entries, err := store.Query(LogFilter{SortDesc: true})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "something went wrong", entries[0].Error)
}
