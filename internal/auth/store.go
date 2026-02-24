package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver registration

	"github.com/SigmaUno/da-proxy/internal/config"
)

// Token represents a stored token with metadata.
type Token struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	TokenPrefix    string     `json:"token_prefix"`
	Enabled        bool       `json:"enabled"`
	RateLimit      int        `json:"rate_limit"`
	Scope          string     `json:"scope"`
	AllowedMethods []string   `json:"allowed_methods,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// CreateTokenRequest is the input for creating a new token.
type CreateTokenRequest struct {
	Name           string     `json:"name"`
	Enabled        *bool      `json:"enabled,omitempty"`
	RateLimit      int        `json:"rate_limit"`
	Scope          string     `json:"scope"`
	AllowedMethods []string   `json:"allowed_methods,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

// UpdateTokenRequest is the input for updating a token.
type UpdateTokenRequest struct {
	Name           *string    `json:"name,omitempty"`
	Enabled        *bool      `json:"enabled,omitempty"`
	RateLimit      *int       `json:"rate_limit,omitempty"`
	Scope          *string    `json:"scope,omitempty"`
	AllowedMethods []string   `json:"allowed_methods,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

// CreateTokenResult is returned when a new token is created.
type CreateTokenResult struct {
	Token
	PlaintextToken string `json:"token"`
}

// RotateTokenResult is returned when a token is rotated.
type RotateTokenResult struct {
	Token
	PlaintextToken string `json:"token"`
}

// ValidScopes defines valid token scope values.
var ValidScopes = map[string]bool{
	"read-only": true,
	"write":     true,
	"admin":     true,
	"":          true, // empty means no scope restriction
}

// SQLiteTokenStore is a database-backed token store with in-memory cache.
type SQLiteTokenStore struct {
	db       *sql.DB
	mu       sync.RWMutex
	cache    map[string]TokenInfo // keyed by token hash
	cacheTTL time.Duration
	lastLoad time.Time
}

// NewSQLiteTokenStore creates a new SQLite-backed token store.
func NewSQLiteTokenStore(dbPath string, cacheTTL time.Duration) (*SQLiteTokenStore, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("opening token database: %w", err)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connecting to token database: %w", err)
	}

	s := &SQLiteTokenStore{
		db:       db,
		cache:    make(map[string]TokenInfo),
		cacheTTL: cacheTTL,
	}

	if err := s.createTable(); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := s.loadCache(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return s, nil
}

func (s *SQLiteTokenStore) createTable() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			token_hash TEXT NOT NULL UNIQUE,
			token_prefix TEXT NOT NULL,
			name TEXT NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT 1,
			rate_limit INTEGER NOT NULL DEFAULT 0,
			scope TEXT NOT NULL DEFAULT '',
			allowed_methods TEXT NOT NULL DEFAULT '',
			expires_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_tokens_hash ON tokens(token_hash);
	`)
	return err
}

func (s *SQLiteTokenStore) loadCache() error {
	rows, err := s.db.Query(`SELECT token_hash, name, enabled, rate_limit, scope, allowed_methods, expires_at FROM tokens`)
	if err != nil {
		return fmt.Errorf("loading token cache: %w", err)
	}
	defer func() { _ = rows.Close() }()

	cache := make(map[string]TokenInfo)
	for rows.Next() {
		var hash, name, scope, methods string
		var enabled bool
		var rateLimit int
		var expiresAt sql.NullTime

		if err := rows.Scan(&hash, &name, &enabled, &rateLimit, &scope, &methods, &expiresAt); err != nil {
			return fmt.Errorf("scanning token row: %w", err)
		}

		info := TokenInfo{
			Name:      name,
			Enabled:   enabled,
			RateLimit: rateLimit,
			Scope:     scope,
		}
		if methods != "" {
			info.AllowedMethods = strings.Split(methods, ",")
		}
		if expiresAt.Valid {
			t := expiresAt.Time
			info.ExpiresAt = &t
		}
		cache[hash] = info
	}

	s.mu.Lock()
	s.cache = cache
	s.lastLoad = time.Now()
	s.mu.Unlock()

	return nil
}

// Lookup implements TokenStore by checking the cache for a SHA-256 hashed token.
func (s *SQLiteTokenStore) Lookup(token string) (TokenInfo, bool) {
	hash := hashToken(token)

	s.mu.RLock()
	stale := time.Since(s.lastLoad) > s.cacheTTL
	info, ok := s.cache[hash]
	s.mu.RUnlock()

	if stale {
		_ = s.loadCache()
		s.mu.RLock()
		info, ok = s.cache[hash]
		s.mu.RUnlock()
	}

	return info, ok
}

// Create generates a new token and stores it.
func (s *SQLiteTokenStore) Create(req CreateTokenRequest) (*CreateTokenResult, error) {
	plaintext, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("generating token: %w", err)
	}

	hash := hashToken(plaintext)
	prefix := plaintext[:8]

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.Scope == "" {
		req.Scope = "write"
	}
	methods := strings.Join(req.AllowedMethods, ",")

	var expiresAt sql.NullTime
	if req.ExpiresAt != nil {
		expiresAt = sql.NullTime{Time: *req.ExpiresAt, Valid: true}
	}

	now := time.Now().UTC()
	result, err := s.db.Exec(`
		INSERT INTO tokens (token_hash, token_prefix, name, enabled, rate_limit, scope, allowed_methods, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, hash, prefix, req.Name, enabled, req.RateLimit, req.Scope, methods, expiresAt, now, now)
	if err != nil {
		return nil, fmt.Errorf("inserting token: %w", err)
	}

	id, _ := result.LastInsertId()

	_ = s.loadCache()

	return &CreateTokenResult{
		Token: Token{
			ID:             id,
			Name:           req.Name,
			TokenPrefix:    prefix,
			Enabled:        enabled,
			RateLimit:      req.RateLimit,
			Scope:          req.Scope,
			AllowedMethods: req.AllowedMethods,
			ExpiresAt:      req.ExpiresAt,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		PlaintextToken: plaintext,
	}, nil
}

// Get retrieves a token by ID.
func (s *SQLiteTokenStore) Get(id int64) (*Token, error) {
	return s.scanToken(s.db.QueryRow(`
		SELECT id, token_prefix, name, enabled, rate_limit, scope, allowed_methods, expires_at, created_at, updated_at
		FROM tokens WHERE id = ?
	`, id))
}

// List returns all tokens (without sensitive data).
func (s *SQLiteTokenStore) List() ([]Token, error) {
	rows, err := s.db.Query(`
		SELECT id, token_prefix, name, enabled, rate_limit, scope, allowed_methods, expires_at, created_at, updated_at
		FROM tokens ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var tokens []Token
	for rows.Next() {
		t, err := s.scanTokenRow(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, *t)
	}
	if tokens == nil {
		tokens = []Token{}
	}
	return tokens, nil
}

// Update modifies a token's settings.
func (s *SQLiteTokenStore) Update(id int64, req UpdateTokenRequest) (*Token, error) {
	existing, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}

	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.RateLimit != nil {
		existing.RateLimit = *req.RateLimit
	}
	if req.Scope != nil {
		existing.Scope = *req.Scope
	}
	if req.AllowedMethods != nil {
		existing.AllowedMethods = req.AllowedMethods
	}
	if req.ExpiresAt != nil {
		existing.ExpiresAt = req.ExpiresAt
	}

	methods := strings.Join(existing.AllowedMethods, ",")
	var expiresAt sql.NullTime
	if existing.ExpiresAt != nil {
		expiresAt = sql.NullTime{Time: *existing.ExpiresAt, Valid: true}
	}

	now := time.Now().UTC()
	_, err = s.db.Exec(`
		UPDATE tokens SET name=?, enabled=?, rate_limit=?, scope=?, allowed_methods=?, expires_at=?, updated_at=?
		WHERE id=?
	`, existing.Name, existing.Enabled, existing.RateLimit, existing.Scope, methods, expiresAt, now, id)
	if err != nil {
		return nil, err
	}

	existing.UpdatedAt = now
	_ = s.loadCache()
	return existing, nil
}

// Delete removes a token by ID.
func (s *SQLiteTokenStore) Delete(id int64) error {
	result, err := s.db.Exec(`DELETE FROM tokens WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("token not found")
	}
	_ = s.loadCache()
	return nil
}

// Rotate generates a new token value for an existing token, invalidating the old one.
func (s *SQLiteTokenStore) Rotate(id int64) (*RotateTokenResult, error) {
	existing, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("token not found")
	}

	plaintext, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("generating new token: %w", err)
	}

	hash := hashToken(plaintext)
	prefix := plaintext[:8]
	now := time.Now().UTC()

	_, err = s.db.Exec(`UPDATE tokens SET token_hash=?, token_prefix=?, updated_at=? WHERE id=?`,
		hash, prefix, now, id)
	if err != nil {
		return nil, err
	}

	existing.TokenPrefix = prefix
	existing.UpdatedAt = now
	_ = s.loadCache()

	return &RotateTokenResult{
		Token:          *existing,
		PlaintextToken: plaintext,
	}, nil
}

// MigrateConfigTokens imports tokens from config file into the database.
// Only imports tokens that don't already exist (by name).
func (s *SQLiteTokenStore) MigrateConfigTokens(tokens []config.TokenConfig) (int, error) {
	imported := 0
	for _, t := range tokens {
		// Check if a token with this name already exists.
		var count int
		err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens WHERE name = ?`, t.Name).Scan(&count)
		if err != nil {
			return imported, err
		}
		if count > 0 {
			continue
		}

		hash := hashToken(t.Token)
		prefix := t.Token[:min(8, len(t.Token))]
		methods := strings.Join(t.AllowedMethods, ",")
		now := time.Now().UTC()

		_, err = s.db.Exec(`
			INSERT INTO tokens (token_hash, token_prefix, name, enabled, rate_limit, scope, allowed_methods, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, hash, prefix, t.Name, t.Enabled, t.RateLimit, "", methods, now, now)
		if err != nil {
			return imported, err
		}
		imported++
	}

	if imported > 0 {
		_ = s.loadCache()
	}
	return imported, nil
}

// TokenCount returns the total number of active tokens.
func (s *SQLiteTokenStore) TokenCount() (int, int) {
	var total, active int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&total)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM tokens WHERE enabled = 1`).Scan(&active)
	return total, active
}

// Close closes the database connection.
func (s *SQLiteTokenStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteTokenStore) scanToken(row *sql.Row) (*Token, error) {
	t := &Token{}
	var methods string
	var expiresAt sql.NullTime

	err := row.Scan(&t.ID, &t.TokenPrefix, &t.Name, &t.Enabled, &t.RateLimit, &t.Scope, &methods, &expiresAt, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if methods != "" {
		t.AllowedMethods = strings.Split(methods, ",")
	}
	if expiresAt.Valid {
		t.ExpiresAt = &expiresAt.Time
	}
	return t, nil
}

type scannable interface {
	Scan(dest ...interface{}) error
}

func (s *SQLiteTokenStore) scanTokenRow(row scannable) (*Token, error) {
	t := &Token{}
	var methods string
	var expiresAt sql.NullTime

	err := row.Scan(&t.ID, &t.TokenPrefix, &t.Name, &t.Enabled, &t.RateLimit, &t.Scope, &methods, &expiresAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}

	if methods != "" {
		t.AllowedMethods = strings.Split(methods, ",")
	}
	if expiresAt.Valid {
		t.ExpiresAt = &expiresAt.Time
	}
	return t, nil
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func generateToken() (string, error) {
	b := make([]byte, 20) // 160 bits
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
