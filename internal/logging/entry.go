// Package logging provides request log capture, buffering, and persistent storage.
package logging

import "time"

// LogEntry is the canonical log structure for every proxied request.
type LogEntry struct {
	Timestamp     time.Time `json:"timestamp"`
	RequestID     string    `json:"request_id"`
	TokenName     string    `json:"token_name"`
	Method        string    `json:"method"`
	Backend       string    `json:"backend"`
	StatusCode    int       `json:"status_code"`
	LatencyMs     float64   `json:"latency_ms"`
	RequestBytes  int64     `json:"request_bytes"`
	ResponseBytes int64     `json:"response_bytes"`
	ClientIP      string    `json:"client_ip"`
	Error         string    `json:"error,omitempty"`
	Path          string    `json:"path"`
}
