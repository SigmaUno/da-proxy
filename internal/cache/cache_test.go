package cache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKey_Deterministic(t *testing.T) {
	params := []byte(`[100,"AAAA"]`)
	key1 := Key("blob.Get", 100, params)
	key2 := Key("blob.Get", 100, params)
	assert.Equal(t, key1, key2)

	// Different params produce different keys.
	key3 := Key("blob.Get", 100, []byte(`[100,"BBBB"]`))
	assert.NotEqual(t, key1, key3)

	// Different height produces different keys.
	key4 := Key("blob.Get", 200, params)
	assert.NotEqual(t, key1, key4)

	// Different method produces different keys.
	key5 := Key("blob.GetAll", 100, params)
	assert.NotEqual(t, key1, key5)
}

func TestKey_Format(t *testing.T) {
	key := Key("block", 42, []byte(`["42"]`))
	assert.Contains(t, key, "block:42:")
	assert.Len(t, key, len("block:42:")+32) // 16 bytes hex = 32 chars
}

func TestIsCacheable(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		height   int64
		expected bool
	}{
		{"cacheable DA method", "blob.Get", 100, true},
		{"cacheable tendermint method", "block", 100, true},
		{"cacheable validators", "validators", 500, true},
		{"cacheable header.GetByHeight", "header.GetByHeight", 200, true},
		{"cacheable share.GetEDS", "share.GetEDS", 300, true},

		// Not cacheable: zero/latest height.
		{"zero height", "blob.Get", 0, false},
		{"negative height", "block", -1, false},

		// Not cacheable: write methods.
		{"broadcast tx", "broadcast_tx_sync", 100, false},
		{"blob submit", "blob.Submit", 100, false},
		{"state transfer", "state.Transfer", 100, false},

		// Not cacheable: dynamic state.
		{"network head", "header.NetworkHead", 100, false},
		{"sync state", "header.SyncState", 100, false},
		{"status", "status", 100, false},
		{"health", "health", 100, false},
		{"node info", "node.Info", 100, false},

		// Not cacheable: unknown method.
		{"unknown method", "custom.Method", 100, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsCacheable(tt.method, tt.height))
		})
	}
}

func TestNoopCache(t *testing.T) {
	c := NoopCache{}

	ctx := context.Background()

	// Get always misses.
	data, hit := c.Get(ctx, "block", 100, nil)
	assert.Nil(t, data)
	assert.False(t, hit)

	// Set and Close don't panic.
	c.Set(ctx, "block", 100, nil, []byte("data"))
	assert.NoError(t, c.Close())
}
