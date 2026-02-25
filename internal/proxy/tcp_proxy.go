package proxy

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/logging"
	"github.com/SigmaUno/da-proxy/internal/metrics"
)

// TCPProxy is a transparent TCP reverse proxy that forwards connections
// to configured backends with latency-aware load balancing.
type TCPProxy struct {
	router  Router
	logger  *zap.Logger
	metrics *metrics.Metrics
	sinks   []LogSink

	mu       sync.Mutex
	listener net.Listener
	wg       sync.WaitGroup
	closed   atomic.Bool
}

// NewTCPProxy creates a new TCP reverse proxy.
func NewTCPProxy(router Router, logger *zap.Logger, m *metrics.Metrics, sinks ...LogSink) *TCPProxy {
	return &TCPProxy{
		router:  router,
		logger:  logger,
		metrics: m,
		sinks:   sinks,
	}
}

// Serve accepts TCP connections on the given listener and proxies them
// to a selected backend. It blocks until the listener is closed.
func (p *TCPProxy) Serve(lis net.Listener) error {
	p.mu.Lock()
	p.listener = lis
	p.mu.Unlock()

	for {
		conn, err := lis.Accept()
		if err != nil {
			if p.closed.Load() {
				return nil
			}
			p.logger.Error("tcp_accept_error", zap.Error(err))
			continue
		}

		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.handleConn(conn)
		}()
	}
}

// Close closes the listener and waits for active connections to drain.
func (p *TCPProxy) Close() {
	p.closed.Store(true)
	p.mu.Lock()
	lis := p.listener
	p.mu.Unlock()
	if lis != nil {
		_ = lis.Close()
	}
	p.wg.Wait()
}

// handleConn proxies a single TCP connection to a backend.
func (p *TCPProxy) handleConn(client net.Conn) {
	defer func() { _ = client.Close() }()

	start := time.Now()
	clientIP := client.RemoteAddr().String()

	// Select a backend endpoint.
	endpoint := p.router.TargetURL(BackendCelestiaAppP2P)
	if endpoint == "" {
		p.logger.Warn("tcp_no_backend", zap.String("client_ip", clientIP))
		p.recordMetrics("error", 0, 0, start)
		return
	}

	p.logger.Info("tcp_connect",
		zap.String("client_ip", clientIP),
		zap.String("backend", endpoint),
	)

	// Dial the backend.
	backend, err := net.DialTimeout("tcp", endpoint, 10*time.Second)
	if err != nil {
		p.logger.Error("tcp_backend_dial_failed",
			zap.String("endpoint", endpoint),
			zap.String("client_ip", clientIP),
			zap.Error(err),
		)
		p.recordMetrics("error", 0, 0, start)
		return
	}
	defer func() { _ = backend.Close() }()

	// Bidirectional copy.
	done := make(chan struct{})
	var sent, received int64

	// Client -> Backend.
	go func() {
		n, _ := io.Copy(backend, client)
		atomic.AddInt64(&sent, n)
		// Half-close: signal backend that client is done sending.
		if tc, ok := backend.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		close(done)
	}()

	// Backend -> Client (foreground).
	n, _ := io.Copy(client, backend)
	atomic.AddInt64(&received, n)
	// Half-close: signal client that backend is done sending.
	if tc, ok := client.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}

	// Wait for client->backend to finish.
	<-done

	duration := time.Since(start)
	sentBytes := atomic.LoadInt64(&sent)
	recvBytes := atomic.LoadInt64(&received)

	// Record latency.
	p.router.RecordLatency(BackendCelestiaAppP2P, endpoint, duration)

	// Record metrics.
	p.recordMetrics("success", sentBytes, recvBytes, start)

	p.logger.Info("tcp_connection_complete",
		zap.String("client_ip", clientIP),
		zap.String("backend", endpoint),
		zap.Duration("duration", duration),
		zap.Int64("bytes_sent", sentBytes),
		zap.Int64("bytes_received", recvBytes),
	)

	// Push to log sinks.
	latencyMs := float64(duration.Nanoseconds()) / 1e6
	entry := logging.LogEntry{
		Timestamp: start,
		Method:    "tcp",
		Backend:   string(BackendCelestiaAppP2P),
		LatencyMs: latencyMs,
		ClientIP:  clientIP,
		Path:      "tcp",
	}
	for _, sink := range p.sinks {
		if sink != nil {
			sink.Push(entry)
		}
	}
}

// recordMetrics records Prometheus metrics for a TCP connection.
func (p *TCPProxy) recordMetrics(status string, sent, received int64, start time.Time) {
	if p.metrics == nil {
		return
	}

	p.metrics.TCPConnectionsTotal.With(prometheus.Labels{
		"status": status,
	}).Inc()

	p.metrics.TCPConnectionDuration.With(prometheus.Labels{}).Observe(time.Since(start).Seconds())

	if sent > 0 {
		p.metrics.TCPBytesTotal.With(prometheus.Labels{
			"direction": "sent",
		}).Add(float64(sent))
	}
	if received > 0 {
		p.metrics.TCPBytesTotal.With(prometheus.Labels{
			"direction": "received",
		}).Add(float64(received))
	}
}
