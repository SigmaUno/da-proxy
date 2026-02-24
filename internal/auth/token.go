// Package auth provides token-based authentication for proxied requests.
package auth

import "github.com/SigmaUno/da-proxy/internal/config"

// TokenInfo holds metadata for an authenticated token.
type TokenInfo struct {
	Name           string
	Enabled        bool
	RateLimit      int
	AllowedMethods []string
}

// TokenStore is the interface for token lookup.
type TokenStore interface {
	Lookup(token string) (TokenInfo, bool)
}

type memoryTokenStore struct {
	tokens map[string]TokenInfo
}

// NewMemoryTokenStore creates a TokenStore from config token entries.
func NewMemoryTokenStore(tokens []config.TokenConfig) TokenStore {
	m := &memoryTokenStore{
		tokens: make(map[string]TokenInfo, len(tokens)),
	}
	for _, t := range tokens {
		m.tokens[t.Token] = TokenInfo{
			Name:           t.Name,
			Enabled:        t.Enabled,
			RateLimit:      t.RateLimit,
			AllowedMethods: t.AllowedMethods,
		}
	}
	return m
}

func (s *memoryTokenStore) Lookup(token string) (TokenInfo, bool) {
	info, ok := s.tokens[token]
	return info, ok
}
