package proxy

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/SigmaUno/da-proxy/internal/metrics"
)

// testEchoServer is a simple gRPC server that echoes back request payloads
// for any method using the UnknownServiceHandler.
type testEchoServer struct {
	server *grpc.Server
	lis    net.Listener
}

func newTestEchoServer(t *testing.T) *testEchoServer {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := grpc.NewServer(
		grpc.ForceServerCodec(rawCodec{}),
		grpc.UnknownServiceHandler(func(_ interface{}, stream grpc.ServerStream) error {
			// Read one message and echo it back.
			msg := &frame{}
			if err := stream.RecvMsg(msg); err != nil {
				return err
			}
			return stream.SendMsg(msg)
		}),
	)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	return &testEchoServer{server: srv, lis: lis}
}

func (s *testEchoServer) Addr() string {
	return s.lis.Addr().String()
}

func testMetrics(t *testing.T) *metrics.Metrics {
	t.Helper()
	reg := prometheus.NewRegistry()
	return metrics.NewMetrics(reg)
}

func TestGRPCProxy_NoBackends(t *testing.T) {
	// Create a router with no gRPC backends.
	r := NewRouter(config.BackendsConfig{
		CelestiaAppRPC:  config.Endpoints{"http://127.0.0.1:26657"},
		CelestiaNodeRPC: config.Endpoints{"http://127.0.0.1:26658"},
	})

	p := NewGRPCProxy(r, zap.NewNop(), testMetrics(t))

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = p.Serve(lis) }()
	t.Cleanup(p.GracefulStop)

	// Dial the proxy.
	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})),
	)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	// Make a unary call.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp := &frame{}
	err = conn.Invoke(ctx, "/test.Service/Method", &frame{payload: []byte("hello")}, resp)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
	assert.Contains(t, st.Message(), "no gRPC backend available")
}

func TestGRPCProxy_ForwardUnary(t *testing.T) {
	// Start a test echo backend.
	backend := newTestEchoServer(t)

	// Create a router with the test backend as a gRPC endpoint.
	r := NewRouter(config.BackendsConfig{
		CelestiaAppRPC:  config.Endpoints{"http://127.0.0.1:26657"},
		CelestiaNodeRPC: config.Endpoints{"http://127.0.0.1:26658"},
		CelestiaAppGRPC: config.Endpoints{backend.Addr()},
	})

	m := testMetrics(t)
	p := NewGRPCProxy(r, zap.NewNop(), m)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = p.Serve(lis) }()
	t.Cleanup(p.GracefulStop)

	// Dial the proxy.
	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})),
	)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	// Make a unary call.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &frame{payload: []byte("test-payload")}
	resp := &frame{}
	err = conn.Invoke(ctx, "/cosmos.bank.v1beta1.Query/Balance", req, resp)
	require.NoError(t, err)

	// Verify echo response.
	assert.Equal(t, []byte("test-payload"), resp.payload)
}

func TestGRPCProxy_LatencyRecording(t *testing.T) {
	// Start a test echo backend.
	backend := newTestEchoServer(t)

	r := NewRouter(config.BackendsConfig{
		CelestiaAppRPC:  config.Endpoints{"http://127.0.0.1:26657"},
		CelestiaNodeRPC: config.Endpoints{"http://127.0.0.1:26658"},
		CelestiaAppGRPC: config.Endpoints{backend.Addr()},
	})

	m := testMetrics(t)
	p := NewGRPCProxy(r, zap.NewNop(), m)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = p.Serve(lis) }()
	t.Cleanup(p.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})),
	)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Send a few requests.
	for i := 0; i < 3; i++ {
		resp := &frame{}
		err = conn.Invoke(ctx, "/test.Service/Ping", &frame{payload: []byte("ping")}, resp)
		require.NoError(t, err)
	}

	// Verify endpoint stats were recorded.
	stats := r.EndpointStats(BackendCelestiaAppGRPC)
	require.Len(t, stats, 1)
	assert.Equal(t, backend.Addr(), stats[0].URL)
	assert.Equal(t, uint64(3), stats[0].RequestCount)
	assert.Greater(t, stats[0].EWMALatencyMs, 0.0)
}

func TestGRPCProxy_LoadBalancing(t *testing.T) {
	// Start two test echo backends.
	backend1 := newTestEchoServer(t)
	backend2 := newTestEchoServer(t)

	r := NewRouter(config.BackendsConfig{
		CelestiaAppRPC:  config.Endpoints{"http://127.0.0.1:26657"},
		CelestiaNodeRPC: config.Endpoints{"http://127.0.0.1:26658"},
		CelestiaAppGRPC: config.Endpoints{backend1.Addr(), backend2.Addr()},
	})

	m := testMetrics(t)
	p := NewGRPCProxy(r, zap.NewNop(), m)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = p.Serve(lis) }()
	t.Cleanup(p.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})),
	)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Send enough requests to exercise both backends.
	for i := 0; i < 20; i++ {
		resp := &frame{}
		err = conn.Invoke(ctx, "/test.Service/Ping", &frame{payload: []byte("ping")}, resp)
		require.NoError(t, err)
	}

	// Verify both backends received traffic.
	stats := r.EndpointStats(BackendCelestiaAppGRPC)
	require.Len(t, stats, 2)

	// Both should have at least 1 request.
	assert.Greater(t, stats[0].RequestCount, uint64(0), "backend 1 should have received requests")
	assert.Greater(t, stats[1].RequestCount, uint64(0), "backend 2 should have received requests")

	// Total should be 20.
	assert.Equal(t, uint64(20), stats[0].RequestCount+stats[1].RequestCount)
}
