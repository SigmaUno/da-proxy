package auth

import (
	"testing"

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
