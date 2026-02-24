// Package auth provides token-based authentication for proxied requests.
package auth

import (
	"strings"
	"time"

	"github.com/SigmaUno/da-proxy/internal/config"
)

// TokenInfo holds metadata for an authenticated token.
type TokenInfo struct {
	Name           string
	Enabled        bool
	RateLimit      int
	Scope          string
	AllowedMethods []string
	ExpiresAt      *time.Time
}

// IsExpired returns true if the token has an expiry time that has passed.
func (t TokenInfo) IsExpired() bool {
	if t.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*t.ExpiresAt)
}

// IsMethodAllowed checks if the given method is permitted by this token's
// scope and allowlist. Empty allowlist means all methods are allowed.
func (t TokenInfo) IsMethodAllowed(method string) bool {
	// Check scope-based restrictions first.
	if !isScopeAllowed(t.Scope, method) {
		return false
	}

	// If no explicit allowlist, allow all.
	if len(t.AllowedMethods) == 0 {
		return true
	}

	for _, m := range t.AllowedMethods {
		if m == method {
			return true
		}
		// Support wildcard namespace matching: "blob.*" matches "blob.Get".
		if strings.HasSuffix(m, ".*") {
			prefix := strings.TrimSuffix(m, "*")
			if strings.HasPrefix(method, prefix) {
				return true
			}
		}
	}
	return false
}

// writeMethods are methods that modify state (broadcast, submit, transfer).
var writeMethods = map[string]bool{
	"broadcast_tx_sync":               true,
	"broadcast_tx_async":              true,
	"broadcast_tx_commit":             true,
	"blob.Submit":                     true,
	"state.SubmitTx":                  true,
	"state.Transfer":                  true,
	"state.SubmitPayForBlob":          true,
	"state.CancelUnbondingDelegation": true,
	"state.BeginRedelegate":           true,
	"state.Undelegate":                true,
	"state.Delegate":                  true,
}

func isScopeAllowed(scope, method string) bool {
	switch scope {
	case "read-only":
		return !writeMethods[method]
	case "write", "admin", "":
		return true
	default:
		return true
	}
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
