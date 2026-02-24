package logging

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver registration
)

type postgresStore struct {
	db            *sql.DB
	insertCh      chan LogEntry
	done          chan struct{}
	batchSize     int
	flushInterval time.Duration
	retention     time.Duration
}

const pgCreateTableSQL = `
CREATE TABLE IF NOT EXISTS request_logs (
    id BIGSERIAL PRIMARY KEY,
    timestamp TIMESTAMPTZ NOT NULL,
    request_id TEXT NOT NULL,
    token_name TEXT NOT NULL,
    method TEXT NOT NULL,
    backend TEXT NOT NULL,
    status_code INTEGER NOT NULL,
    latency_ms DOUBLE PRECISION NOT NULL,
    request_bytes BIGINT NOT NULL,
    response_bytes BIGINT NOT NULL,
    client_ip TEXT NOT NULL,
    error TEXT,
    path TEXT
);

CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON request_logs(timestamp);
CREATE INDEX IF NOT EXISTS idx_logs_method ON request_logs(method);
CREATE INDEX IF NOT EXISTS idx_logs_token_name ON request_logs(token_name);
CREATE INDEX IF NOT EXISTS idx_logs_status ON request_logs(status_code);
CREATE INDEX IF NOT EXISTS idx_logs_backend ON request_logs(backend);
`

// NewPostgresStore creates a new PostgreSQL-backed log store.
func NewPostgresStore(dsn string, retention time.Duration) (Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening postgres db: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}

	if _, err := db.Exec(pgCreateTableSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("creating tables: %w", err)
	}

	s := &postgresStore{
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

func (s *postgresStore) Push(entry LogEntry) {
	select {
	case s.insertCh <- entry:
	default:
	}
}

func (s *postgresStore) batchWriter() {
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
			close(s.insertCh)
			for entry := range s.insertCh {
				batch = append(batch, entry)
			}
			flush()
			return
		}
	}
}

func (s *postgresStore) writeBatch(entries []LogEntry) {
	if len(entries) == 0 {
		return
	}

	// Build a multi-row INSERT for efficiency.
	var b strings.Builder
	b.WriteString("INSERT INTO request_logs (timestamp, request_id, token_name, method, backend, status_code, latency_ms, request_bytes, response_bytes, client_ip, error, path) VALUES ")

	args := make([]interface{}, 0, len(entries)*12)
	for i, e := range entries {
		if i > 0 {
			b.WriteString(", ")
		}
		base := i * 12
		fmt.Fprintf(&b, "($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6,
			base+7, base+8, base+9, base+10, base+11, base+12)
		args = append(args,
			e.Timestamp, e.RequestID, e.TokenName, e.Method, e.Backend,
			e.StatusCode, e.LatencyMs, e.RequestBytes, e.ResponseBytes,
			e.ClientIP, e.Error, e.Path,
		)
	}

	_, _ = s.db.Exec(b.String(), args...)
}

func (s *postgresStore) retentionCleaner() {
	if s.retention <= 0 {
		return
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-s.retention)
			_, _ = s.db.Exec("DELETE FROM request_logs WHERE timestamp < $1", cutoff)
		case <-s.done:
			return
		}
	}
}

func (s *postgresStore) Query(filter LogFilter) ([]LogEntry, error) {
	query, args := pgBuildQuery("SELECT id, timestamp, request_id, token_name, method, backend, status_code, latency_ms, request_bytes, response_bytes, client_ip, error, path FROM request_logs", filter)

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

func (s *postgresStore) Count(filter LogFilter) (int64, error) {
	query, args := pgBuildQuery("SELECT COUNT(*) FROM request_logs", filter)
	var count int64
	err := s.db.QueryRow(query, args...).Scan(&count)
	return count, err
}

// pgBuildQuery builds a parameterized query with PostgreSQL $N placeholders.
func pgBuildQuery(base string, filter LogFilter) (string, []interface{}) {
	var conditions []string
	var args []interface{}
	n := 1

	add := func(cond string, val interface{}) {
		conditions = append(conditions, fmt.Sprintf(cond, n))
		args = append(args, val)
		n++
	}

	if filter.Method != "" {
		add("method = $%d", filter.Method)
	}
	if filter.TokenName != "" {
		add("token_name = $%d", filter.TokenName)
	}
	if filter.Backend != "" {
		add("backend = $%d", filter.Backend)
	}
	if filter.StatusCode != 0 {
		add("status_code = $%d", filter.StatusCode)
	}
	if filter.StatusMin != 0 {
		add("status_code >= $%d", filter.StatusMin)
	}
	if filter.StatusMax != 0 {
		add("status_code <= $%d", filter.StatusMax)
	}
	if !filter.From.IsZero() {
		add("timestamp >= $%d", filter.From)
	}
	if !filter.To.IsZero() {
		add("timestamp <= $%d", filter.To)
	}
	if filter.LatencyMin > 0 {
		add("latency_ms >= $%d", filter.LatencyMin)
	}
	if filter.Cursor > 0 {
		if filter.SortDesc {
			add("id < $%d", filter.Cursor)
		} else {
			add("id > $%d", filter.Cursor)
		}
	}

	query := base
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	isSelect := strings.HasPrefix(base, "SELECT id") || strings.Contains(base, "request_id")
	if isSelect {
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

func (s *postgresStore) BackendStats(since time.Time) ([]BackendStat, error) {
	const query = `SELECT backend, AVG(latency_ms), COUNT(*),
		ARRAY_TO_STRING(ARRAY_AGG(DISTINCT method), ',')
		FROM request_logs WHERE timestamp >= $1
		GROUP BY backend`

	rows, err := s.db.Query(query, since)
	if err != nil {
		return nil, fmt.Errorf("querying backend stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var stats []BackendStat
	for rows.Next() {
		var bs BackendStat
		var methods sql.NullString
		if err := rows.Scan(&bs.Backend, &bs.AvgLatencyMs, &bs.TotalRequests, &methods); err != nil {
			return nil, fmt.Errorf("scanning backend stat: %w", err)
		}
		if methods.Valid && methods.String != "" {
			bs.Methods = strings.Split(methods.String, ",")
		}
		stats = append(stats, bs)
	}
	return stats, rows.Err()
}

func (s *postgresStore) Close() error {
	close(s.done)
	time.Sleep(100 * time.Millisecond)
	return s.db.Close()
}
