package auth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SigmaUno/da-proxy/internal/config"
)

func newTestStore(t *testing.T) *SQLiteTokenStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "tokens.db")
	store, err := NewSQLiteTokenStore(dbPath, 5*time.Minute)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSQLiteTokenStore_CreateAndLookup(t *testing.T) {
	store := newTestStore(t)

	result, err := store.Create(CreateTokenRequest{
		Name:      "test-service",
		RateLimit: 100,
		Scope:     "write",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Len(t, result.PlaintextToken, 40, "token should be 40 hex chars (160 bits)")
	assert.Equal(t, "test-service", result.Name)
	assert.True(t, result.Enabled)
	assert.Equal(t, 100, result.RateLimit)
	assert.Equal(t, "write", result.Scope)
	assert.Equal(t, result.PlaintextToken[:8], result.TokenPrefix)

	// Lookup by plaintext token.
	info, found := store.Lookup(result.PlaintextToken)
	require.True(t, found)
	assert.Equal(t, "test-service", info.Name)
	assert.True(t, info.Enabled)
	assert.Equal(t, 100, info.RateLimit)
	assert.Equal(t, "write", info.Scope)
}

func TestSQLiteTokenStore_CreateWithExpiry(t *testing.T) {
	store := newTestStore(t)

	expiry := time.Now().Add(24 * time.Hour).UTC()
	result, err := store.Create(CreateTokenRequest{
		Name:      "expiring-token",
		ExpiresAt: &expiry,
	})
	require.NoError(t, err)

	info, found := store.Lookup(result.PlaintextToken)
	require.True(t, found)
	assert.NotNil(t, info.ExpiresAt)
	assert.WithinDuration(t, expiry, *info.ExpiresAt, time.Second)
}

func TestSQLiteTokenStore_CreateDisabled(t *testing.T) {
	store := newTestStore(t)

	disabled := false
	result, err := store.Create(CreateTokenRequest{
		Name:    "disabled-token",
		Enabled: &disabled,
	})
	require.NoError(t, err)

	info, found := store.Lookup(result.PlaintextToken)
	require.True(t, found)
	assert.False(t, info.Enabled)
}

func TestSQLiteTokenStore_LookupNotFound(t *testing.T) {
	store := newTestStore(t)

	_, found := store.Lookup("nonexistent-token")
	assert.False(t, found)
}

func TestSQLiteTokenStore_Get(t *testing.T) {
	store := newTestStore(t)

	result, err := store.Create(CreateTokenRequest{
		Name:           "get-test",
		Scope:          "read-only",
		AllowedMethods: []string{"status", "health"},
	})
	require.NoError(t, err)

	token, err := store.Get(result.ID)
	require.NoError(t, err)
	require.NotNil(t, token)
	assert.Equal(t, "get-test", token.Name)
	assert.Equal(t, "read-only", token.Scope)
	assert.Equal(t, []string{"status", "health"}, token.AllowedMethods)
}

func TestSQLiteTokenStore_GetNotFound(t *testing.T) {
	store := newTestStore(t)

	token, err := store.Get(999)
	require.NoError(t, err)
	assert.Nil(t, token)
}

func TestSQLiteTokenStore_List(t *testing.T) {
	store := newTestStore(t)

	_, err := store.Create(CreateTokenRequest{Name: "token-a"})
	require.NoError(t, err)
	_, err = store.Create(CreateTokenRequest{Name: "token-b"})
	require.NoError(t, err)

	tokens, err := store.List()
	require.NoError(t, err)
	assert.Len(t, tokens, 2)
	// Listed in descending creation order.
	assert.Equal(t, "token-b", tokens[0].Name)
	assert.Equal(t, "token-a", tokens[1].Name)
}

func TestSQLiteTokenStore_ListEmpty(t *testing.T) {
	store := newTestStore(t)

	tokens, err := store.List()
	require.NoError(t, err)
	assert.NotNil(t, tokens)
	assert.Len(t, tokens, 0)
}

func TestSQLiteTokenStore_Update(t *testing.T) {
	store := newTestStore(t)

	result, err := store.Create(CreateTokenRequest{
		Name:      "original",
		RateLimit: 100,
		Scope:     "write",
	})
	require.NoError(t, err)

	newName := "updated"
	newRate := 500
	newScope := "read-only"
	updated, err := store.Update(result.ID, UpdateTokenRequest{
		Name:      &newName,
		RateLimit: &newRate,
		Scope:     &newScope,
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, "updated", updated.Name)
	assert.Equal(t, 500, updated.RateLimit)
	assert.Equal(t, "read-only", updated.Scope)

	// Verify lookup reflects changes.
	info, found := store.Lookup(result.PlaintextToken)
	require.True(t, found)
	assert.Equal(t, "updated", info.Name)
	assert.Equal(t, 500, info.RateLimit)
}

func TestSQLiteTokenStore_UpdateNotFound(t *testing.T) {
	store := newTestStore(t)

	name := "new"
	updated, err := store.Update(999, UpdateTokenRequest{Name: &name})
	require.NoError(t, err)
	assert.Nil(t, updated)
}

func TestSQLiteTokenStore_Delete(t *testing.T) {
	store := newTestStore(t)

	result, err := store.Create(CreateTokenRequest{Name: "to-delete"})
	require.NoError(t, err)

	err = store.Delete(result.ID)
	require.NoError(t, err)

	// Verify token is gone.
	_, found := store.Lookup(result.PlaintextToken)
	assert.False(t, found)

	token, err := store.Get(result.ID)
	require.NoError(t, err)
	assert.Nil(t, token)
}

func TestSQLiteTokenStore_DeleteNotFound(t *testing.T) {
	store := newTestStore(t)

	err := store.Delete(999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSQLiteTokenStore_Rotate(t *testing.T) {
	store := newTestStore(t)

	result, err := store.Create(CreateTokenRequest{Name: "rotate-me"})
	require.NoError(t, err)
	oldToken := result.PlaintextToken

	rotated, err := store.Rotate(result.ID)
	require.NoError(t, err)
	require.NotNil(t, rotated)

	assert.Len(t, rotated.PlaintextToken, 40)
	assert.NotEqual(t, oldToken, rotated.PlaintextToken)
	assert.Equal(t, "rotate-me", rotated.Name)

	// Old token should no longer work.
	_, found := store.Lookup(oldToken)
	assert.False(t, found)

	// New token should work.
	info, found := store.Lookup(rotated.PlaintextToken)
	assert.True(t, found)
	assert.Equal(t, "rotate-me", info.Name)
}

func TestSQLiteTokenStore_RotateNotFound(t *testing.T) {
	store := newTestStore(t)

	_, err := store.Rotate(999)
	require.Error(t, err)
}

func TestSQLiteTokenStore_MigrateConfigTokens(t *testing.T) {
	store := newTestStore(t)

	tokens := []config.TokenConfig{
		{Token: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "svc-a", Enabled: true, RateLimit: 100},
		{Token: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Name: "svc-b", Enabled: false},
	}

	imported, err := store.MigrateConfigTokens(tokens)
	require.NoError(t, err)
	assert.Equal(t, 2, imported)

	// Lookup by the raw config token.
	info, found := store.Lookup("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	require.True(t, found)
	assert.Equal(t, "svc-a", info.Name)
	assert.True(t, info.Enabled)

	// Running migration again should not duplicate.
	imported, err = store.MigrateConfigTokens(tokens)
	require.NoError(t, err)
	assert.Equal(t, 0, imported)

	all, err := store.List()
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestSQLiteTokenStore_TokenCount(t *testing.T) {
	store := newTestStore(t)

	disabled := false
	_, err := store.Create(CreateTokenRequest{Name: "active"})
	require.NoError(t, err)
	_, err = store.Create(CreateTokenRequest{Name: "disabled", Enabled: &disabled})
	require.NoError(t, err)

	total, active := store.TokenCount()
	assert.Equal(t, 2, total)
	assert.Equal(t, 1, active)
}

func TestSQLiteTokenStore_CacheTTL(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tokens.db")
	store, err := NewSQLiteTokenStore(dbPath, 1*time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	result, err := store.Create(CreateTokenRequest{Name: "cached"})
	require.NoError(t, err)

	// Lookup should work.
	info, found := store.Lookup(result.PlaintextToken)
	require.True(t, found)
	assert.Equal(t, "cached", info.Name)

	// Wait for cache to expire.
	time.Sleep(5 * time.Millisecond)

	// Lookup should still work (triggers cache reload).
	info, found = store.Lookup(result.PlaintextToken)
	require.True(t, found)
	assert.Equal(t, "cached", info.Name)
}

func TestHashToken(t *testing.T) {
	h1 := hashToken("test-token")
	h2 := hashToken("test-token")
	h3 := hashToken("different-token")

	assert.Equal(t, h1, h2, "same input should produce same hash")
	assert.NotEqual(t, h1, h3, "different input should produce different hash")
	assert.Len(t, h1, 64, "SHA-256 hex should be 64 chars")
}

func TestGenerateToken(t *testing.T) {
	t1, err := generateToken()
	require.NoError(t, err)
	t2, err := generateToken()
	require.NoError(t, err)

	assert.Len(t, t1, 40, "token should be 40 hex chars")
	assert.Len(t, t2, 40)
	assert.NotEqual(t, t1, t2, "tokens should be unique")
}
