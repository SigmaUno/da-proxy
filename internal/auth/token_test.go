package auth

import (
	"testing"
	"time"

	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestMemoryTokenStore_Lookup(t *testing.T) {
	tokens := []config.TokenConfig{
		{Token: "abc123", Name: "service-a", Enabled: true, RateLimit: 100},
		{Token: "def456", Name: "service-b", Enabled: false, RateLimit: 0},
		{Token: "ghi789", Name: "service-c", Enabled: true, RateLimit: 500, AllowedMethods: []string{"blob.Get"}},
	}

	store := NewMemoryTokenStore(tokens)

	tests := []struct {
		name      string
		token     string
		wantInfo  TokenInfo
		wantFound bool
	}{
		{
			name:  "existing enabled token",
			token: "abc123",
			wantInfo: TokenInfo{
				Name:      "service-a",
				Enabled:   true,
				RateLimit: 100,
			},
			wantFound: true,
		},
		{
			name:  "existing disabled token",
			token: "def456",
			wantInfo: TokenInfo{
				Name:      "service-b",
				Enabled:   false,
				RateLimit: 0,
			},
			wantFound: true,
		},
		{
			name:  "token with allowed methods",
			token: "ghi789",
			wantInfo: TokenInfo{
				Name:           "service-c",
				Enabled:        true,
				RateLimit:      500,
				AllowedMethods: []string{"blob.Get"},
			},
			wantFound: true,
		},
		{
			name:      "non-existent token",
			token:     "nonexistent",
			wantInfo:  TokenInfo{},
			wantFound: false,
		},
		{
			name:      "empty token",
			token:     "",
			wantInfo:  TokenInfo{},
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, found := store.Lookup(tt.token)
			assert.Equal(t, tt.wantFound, found)
			assert.Equal(t, tt.wantInfo, info)
		})
	}
}

func TestMemoryTokenStore_EmptyList(t *testing.T) {
	store := NewMemoryTokenStore(nil)
	_, found := store.Lookup("anything")
	assert.False(t, found)
}

func TestTokenInfo_IsExpired(t *testing.T) {
	t.Run("no expiry", func(t *testing.T) {
		info := TokenInfo{Name: "test"}
		assert.False(t, info.IsExpired())
	})

	t.Run("future expiry", func(t *testing.T) {
		future := time.Now().Add(time.Hour)
		info := TokenInfo{Name: "test", ExpiresAt: &future}
		assert.False(t, info.IsExpired())
	})

	t.Run("past expiry", func(t *testing.T) {
		past := time.Now().Add(-time.Hour)
		info := TokenInfo{Name: "test", ExpiresAt: &past}
		assert.True(t, info.IsExpired())
	})
}

func TestTokenInfo_IsMethodAllowed(t *testing.T) {
	tests := []struct {
		name    string
		info    TokenInfo
		method  string
		allowed bool
	}{
		{
			name:    "empty allowlist allows all",
			info:    TokenInfo{},
			method:  "blob.Get",
			allowed: true,
		},
		{
			name:    "exact match",
			info:    TokenInfo{AllowedMethods: []string{"blob.Get", "status"}},
			method:  "blob.Get",
			allowed: true,
		},
		{
			name:    "not in allowlist",
			info:    TokenInfo{AllowedMethods: []string{"status"}},
			method:  "blob.Get",
			allowed: false,
		},
		{
			name:    "wildcard namespace",
			info:    TokenInfo{AllowedMethods: []string{"blob.*"}},
			method:  "blob.Get",
			allowed: true,
		},
		{
			name:    "wildcard no match",
			info:    TokenInfo{AllowedMethods: []string{"blob.*"}},
			method:  "header.NetworkHead",
			allowed: false,
		},
		{
			name:    "read-only scope blocks write",
			info:    TokenInfo{Scope: "read-only"},
			method:  "blob.Submit",
			allowed: false,
		},
		{
			name:    "read-only scope allows read",
			info:    TokenInfo{Scope: "read-only"},
			method:  "blob.Get",
			allowed: true,
		},
		{
			name:    "write scope allows write",
			info:    TokenInfo{Scope: "write"},
			method:  "blob.Submit",
			allowed: true,
		},
		{
			name:    "read-only blocks broadcast",
			info:    TokenInfo{Scope: "read-only"},
			method:  "broadcast_tx_sync",
			allowed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.allowed, tt.info.IsMethodAllowed(tt.method))
		})
	}
}
