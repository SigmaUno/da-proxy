//nolint:revive // metrics is the correct domain name for this package
package metrics

import "github.com/prometheus/client_golang/prometheus"

// Metrics holds all Prometheus metric collectors for DA-Proxy.
type Metrics struct {
	RequestsTotal         *prometheus.CounterVec
	RequestDuration       *prometheus.HistogramVec
	RequestSize           *prometheus.HistogramVec
	ResponseSize          *prometheus.HistogramVec
	ErrorsTotal           *prometheus.CounterVec
	BackendErrorsTotal    *prometheus.CounterVec
	BackendUp             *prometheus.GaugeVec
	BackendHealthDuration *prometheus.GaugeVec
	RateLimitRemaining    *prometheus.GaugeVec
	RateLimitHitsTotal    *prometheus.CounterVec
	GRPCRequestsTotal     *prometheus.CounterVec
	GRPCRequestDuration   *prometheus.HistogramVec
	TCPConnectionsTotal   *prometheus.CounterVec
	TCPConnectionDuration *prometheus.HistogramVec
	TCPBytesTotal         *prometheus.CounterVec
}

// NewMetrics creates and registers all Prometheus metrics with the given registerer.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		RequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "daproxy_requests_total",
			Help: "Total number of proxied requests.",
		}, []string{"method", "backend", "token_name", "status_code"}),

		RequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "daproxy_request_duration_seconds",
			Help:    "Request latency distribution in seconds.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
		}, []string{"method", "backend", "token_name"}),

		RequestSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "daproxy_request_size_bytes",
			Help:    "Request body size distribution in bytes.",
			Buckets: prometheus.ExponentialBuckets(64, 4, 10),
		}, []string{"method", "backend"}),

		ResponseSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "daproxy_response_size_bytes",
			Help:    "Response body size distribution in bytes.",
			Buckets: prometheus.ExponentialBuckets(64, 4, 10),
		}, []string{"method", "backend"}),

		ErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "daproxy_errors_total",
			Help: "Total errors by type.",
		}, []string{"type"}),

		BackendErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "daproxy_backend_errors_total",
			Help: "Upstream errors per backend.",
		}, []string{"backend"}),

		BackendUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "daproxy_backend_up",
			Help: "Backend health status (1=healthy, 0=unreachable).",
		}, []string{"backend"}),

		BackendHealthDuration: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "daproxy_backend_health_check_duration_seconds",
			Help: "Last health check round-trip time in seconds.",
		}, []string{"backend"}),

		RateLimitRemaining: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "daproxy_rate_limit_remaining",
			Help: "Remaining requests in current rate limit window.",
		}, []string{"token_name"}),

		RateLimitHitsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "daproxy_rate_limit_hits_total",
			Help: "Total times rate limit was triggered.",
		}, []string{"token_name"}),

		GRPCRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "daproxy_grpc_requests_total",
			Help: "Total number of proxied gRPC requests.",
		}, []string{"method", "grpc_code"}),

		GRPCRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "daproxy_grpc_request_duration_seconds",
			Help:    "gRPC request latency distribution in seconds.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
		}, []string{"method"}),

		TCPConnectionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "daproxy_tcp_connections_total",
			Help: "Total number of TCP proxy connections.",
		}, []string{"status"}),

		TCPConnectionDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "daproxy_tcp_connection_duration_seconds",
			Help:    "TCP proxy connection duration in seconds.",
			Buckets: []float64{0.1, 0.5, 1, 5, 10, 30, 60, 300, 600},
		}, []string{}),

		TCPBytesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "daproxy_tcp_bytes_total",
			Help: "Total bytes transferred through TCP proxy.",
		}, []string{"direction"}),
	}

	reg.MustRegister(
		m.RequestsTotal,
		m.RequestDuration,
		m.RequestSize,
		m.ResponseSize,
		m.ErrorsTotal,
		m.BackendErrorsTotal,
		m.BackendUp,
		m.BackendHealthDuration,
		m.RateLimitRemaining,
		m.RateLimitHitsTotal,
		m.GRPCRequestsTotal,
		m.GRPCRequestDuration,
		m.TCPConnectionsTotal,
		m.TCPConnectionDuration,
		m.TCPBytesTotal,
	)

	return m
}
