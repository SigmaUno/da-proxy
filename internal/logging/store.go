package logging

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver registration
)

// LogFilter holds query parameters for log searches.
type LogFilter struct {
	Method     string
	TokenName  string
	Backend    string
	StatusCode int
	StatusMin  int
	StatusMax  int
	From       time.Time
	To         time.Time
	LatencyMin float64
	Limit      int
	Cursor     int64
	SortDesc   bool
}

// Store is the interface for persistent log storage.
type Store interface {
	Push(entry LogEntry)
	Query(filter LogFilter) ([]LogEntry, error)
	Count(filter LogFilter) (int64, error)
	Close() error
}

type sqliteStore struct {
	db            *sql.DB
	insertCh      chan LogEntry
	done          chan struct{}
	batchSize     int
	flushInterval time.Duration
	retention     time.Duration
}

const createTableSQL = `
CREATE TABLE IF NOT EXISTS request_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp DATETIME NOT NULL,
    request_id TEXT NOT NULL,
    token_name TEXT NOT NULL,
    method TEXT NOT NULL,
    backend TEXT NOT NULL,
    status_code INTEGER NOT NULL,
    latency_ms REAL NOT NULL,
    request_bytes INTEGER NOT NULL,
    response_bytes INTEGER NOT NULL,
    client_ip TEXT NOT NULL,
    error TEXT,
    path TEXT
);

CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON request_logs(timestamp);
CREATE INDEX IF NOT EXISTS idx_logs_method ON request_logs(method);
CREATE INDEX IF NOT EXISTS idx_logs_token_name ON request_logs(token_name);
CREATE INDEX IF NOT EXISTS idx_logs_status ON request_logs(status_code);
`

const insertSQL = `
INSERT INTO request_logs (timestamp, request_id, token_name, method, backend, status_code, latency_ms, request_bytes, response_bytes, client_ip, error, path)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`

// NewSQLiteStore creates a new SQLite-backed log store.
func NewSQLiteStore(dbPath string, retention time.Duration) (Store, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("opening sqlite db: %w", err)
	}

	if _, err := db.Exec(createTableSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("creating tables: %w", err)
	}

	s := &sqliteStore{
		db:            db,
		insertCh:      make(chan LogEntry, 1000),
		done:          make(chan struct{}),
		batchSize:     100,
		flushInterval: time.Second,
		retention:     retention,
	}

	go s.batchWriter()
	go s.retentionCleaner()

	return s, nil
}

func (s *sqliteStore) Push(entry LogEntry) {
	select {
	case s.insertCh <- entry:
	default:
		// Drop if channel is full to avoid blocking the hot path.
	}
}

func (s *sqliteStore) batchWriter() {
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	batch := make([]LogEntry, 0, s.batchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		s.writeBatch(batch)
		batch = batch[:0]
	}

	for {
		select {
		case entry := <-s.insertCh:
			batch = append(batch, entry)
			if len(batch) >= s.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.done:
			// Drain remaining entries.
			close(s.insertCh)
			for entry := range s.insertCh {
				batch = append(batch, entry)
			}
			flush()
			return
		}
	}
}

func (s *sqliteStore) writeBatch(entries []LogEntry) {
	tx, err := s.db.Begin()
	if err != nil {
		return
	}

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		_ = tx.Rollback()
		return
	}
	defer func() { _ = stmt.Close() }()

	for _, e := range entries {
		_, _ = stmt.Exec(
			e.Timestamp, e.RequestID, e.TokenName, e.Method, e.Backend,
			e.StatusCode, e.LatencyMs, e.RequestBytes, e.ResponseBytes,
			e.ClientIP, e.Error, e.Path,
		)
	}

	_ = tx.Commit()
}

func (s *sqliteStore) retentionCleaner() {
	if s.retention <= 0 {
		return
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-s.retention)
			_, _ = s.db.Exec("DELETE FROM request_logs WHERE timestamp < ?", cutoff)
		case <-s.done:
			return
		}
	}
}

func (s *sqliteStore) Query(filter LogFilter) ([]LogEntry, error) {
	query, args := buildQuery("SELECT id, timestamp, request_id, token_name, method, backend, status_code, latency_ms, request_bytes, response_bytes, client_ip, error, path FROM request_logs", filter)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []LogEntry
	for rows.Next() {
		var e LogEntry
		var id int64
		var errStr sql.NullString
		if err := rows.Scan(&id, &e.Timestamp, &e.RequestID, &e.TokenName, &e.Method, &e.Backend, &e.StatusCode, &e.LatencyMs, &e.RequestBytes, &e.ResponseBytes, &e.ClientIP, &errStr, &e.Path); err != nil {
			return nil, fmt.Errorf("scanning log entry: %w", err)
		}
		if errStr.Valid {
			e.Error = errStr.String
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *sqliteStore) Count(filter LogFilter) (int64, error) {
	query, args := buildQuery("SELECT COUNT(*) FROM request_logs", filter)
	var count int64
	err := s.db.QueryRow(query, args...).Scan(&count)
	return count, err
}

func buildQuery(base string, filter LogFilter) (string, []interface{}) {
	var conditions []string
	var args []interface{}

	if filter.Method != "" {
		conditions = append(conditions, "method = ?")
		args = append(args, filter.Method)
	}
	if filter.TokenName != "" {
		conditions = append(conditions, "token_name = ?")
		args = append(args, filter.TokenName)
	}
	if filter.Backend != "" {
		conditions = append(conditions, "backend = ?")
		args = append(args, filter.Backend)
	}
	if filter.StatusCode != 0 {
		conditions = append(conditions, "status_code = ?")
		args = append(args, filter.StatusCode)
	}
	if filter.StatusMin != 0 {
		conditions = append(conditions, "status_code >= ?")
		args = append(args, filter.StatusMin)
	}
	if filter.StatusMax != 0 {
		conditions = append(conditions, "status_code <= ?")
		args = append(args, filter.StatusMax)
	}
	if !filter.From.IsZero() {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, filter.From)
	}
	if !filter.To.IsZero() {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, filter.To)
	}
	if filter.LatencyMin > 0 {
		conditions = append(conditions, "latency_ms >= ?")
		args = append(args, filter.LatencyMin)
	}
	if filter.Cursor > 0 {
		if filter.SortDesc {
			conditions = append(conditions, "id < ?")
		} else {
			conditions = append(conditions, "id > ?")
		}
		args = append(args, filter.Cursor)
	}

	query := base
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	// Only add ORDER BY and LIMIT for SELECT queries (not COUNT).
	if strings.HasPrefix(base, "SELECT id") || strings.HasPrefix(base, "SELECT timestamp") || strings.Contains(base, "request_id") {
		if filter.SortDesc {
			query += " ORDER BY id DESC"
		} else {
			query += " ORDER BY id ASC"
		}

		limit := filter.Limit
		if limit <= 0 {
			limit = 100
		}
		if limit > 1000 {
			limit = 1000
		}
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	return query, args
}

func (s *sqliteStore) Close() error {
	close(s.done)
	// Give batch writer time to flush.
	time.Sleep(100 * time.Millisecond)
	return s.db.Close()
}
