package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// LokiPusher sends log entries directly to the Loki push API.
type LokiPusher struct {
	url    string
	labels string
	client *http.Client
	ch     chan LogEntry
	done   chan struct{}
}

// NewLokiPusher creates a direct Loki push client.
// url is the Loki push endpoint (e.g. "http://loki:3100/loki/api/v1/push").
func NewLokiPusher(url string) *LokiPusher {
	p := &LokiPusher{
		url:    url,
		labels: `{app="da-proxy"}`,
		client: &http.Client{Timeout: 5 * time.Second},
		ch:     make(chan LogEntry, 1000),
		done:   make(chan struct{}),
	}
	go p.batchLoop()
	return p
}

// Push sends a log entry to the Loki batch queue.
func (p *LokiPusher) Push(entry LogEntry) {
	select {
	case p.ch <- entry:
	default:
	}
}

func (p *LokiPusher) batchLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	batch := make([]LogEntry, 0, 100)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		p.send(batch)
		batch = batch[:0]
	}

	for {
		select {
		case entry := <-p.ch:
			batch = append(batch, entry)
			if len(batch) >= 100 {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-p.done:
			close(p.ch)
			for entry := range p.ch {
				batch = append(batch, entry)
			}
			flush()
			return
		}
	}
}

type lokiPushRequest struct {
	Streams []lokiStream `json:"streams"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"`
}

func (p *LokiPusher) send(entries []LogEntry) {
	values := make([][]string, 0, len(entries))
	for _, e := range entries {
		line, _ := json.Marshal(map[string]interface{}{
			"request_id":     e.RequestID,
			"token_name":     e.TokenName,
			"method":         e.Method,
			"backend":        e.Backend,
			"status_code":    e.StatusCode,
			"latency_ms":     e.LatencyMs,
			"request_bytes":  e.RequestBytes,
			"response_bytes": e.ResponseBytes,
			"client_ip":      e.ClientIP,
			"error":          e.Error,
			"path":           e.Path,
		})
		ts := strconv.FormatInt(e.Timestamp.UnixNano(), 10)
		values = append(values, []string{ts, string(line)})
	}

	payload := lokiPushRequest{
		Streams: []lokiStream{
			{
				Stream: map[string]string{"app": "da-proxy"},
				Values: values,
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	req, err := http.NewRequest(http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// Close flushes remaining entries and shuts down the pusher.
func (p *LokiPusher) Close() error {
	close(p.done)
	time.Sleep(100 * time.Millisecond)
	return nil
}

// LokiStore wraps a LokiPusher to satisfy the Store interface.
// Queries are not supported (they always return empty results)
// since Loki is a write-only sink.
type LokiStore struct {
	pusher *LokiPusher
}

// NewLokiStore creates a Loki-backed log store for push-only use.
func NewLokiStore(url string) *LokiStore {
	return &LokiStore{pusher: NewLokiPusher(url)}
}

// Push sends a log entry to Loki.
func (s *LokiStore) Push(entry LogEntry) {
	s.pusher.Push(entry)
}

// Query is a no-op for Loki — queries should go through LogQL.
func (s *LokiStore) Query(_ LogFilter) ([]LogEntry, error) {
	return nil, fmt.Errorf("query not supported for Loki store; use LogQL via Grafana")
}

// Count is a no-op for Loki.
func (s *LokiStore) Count(_ LogFilter) (int64, error) {
	return 0, fmt.Errorf("count not supported for Loki store; use LogQL via Grafana")
}

// Close shuts down the Loki pusher.
func (s *LokiStore) Close() error {
	return s.pusher.Close()
}
