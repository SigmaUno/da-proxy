package proxy

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/config"
)

// testTCPEchoServer is a simple TCP server that echoes back everything it receives.
type testTCPEchoServer struct {
	lis     net.Listener
	conns   atomic.Int64
	stopped chan struct{}
}

func newTestTCPEchoServer(t *testing.T) *testTCPEchoServer {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	s := &testTCPEchoServer{lis: lis, stopped: make(chan struct{})}
	go func() {
		defer close(s.stopped)
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			s.conns.Add(1)
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	t.Cleanup(func() {
		_ = lis.Close()
		<-s.stopped
	})

	return s
}

func (s *testTCPEchoServer) Addr() string {
	return s.lis.Addr().String()
}

func (s *testTCPEchoServer) ConnCount() int64 {
	return s.conns.Load()
}

func TestTCPProxy_NoBackends(t *testing.T) {
	// Create a router with no P2P backends.
	r := NewRouter(config.BackendsConfig{
		CelestiaAppRPC:  config.Endpoints{"http://127.0.0.1:26657"},
		CelestiaNodeRPC: config.Endpoints{"http://127.0.0.1:26658"},
	})

	p := NewTCPProxy(r, zap.NewNop(), testMetrics(t))

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = p.Serve(lis) }()
	t.Cleanup(p.Close)

	// Connect to the proxy — should close immediately since no backends.
	conn, err := net.DialTimeout("tcp", lis.Addr().String(), 2*time.Second)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	// Try to read — should get EOF since proxy closes the connection.
	buf := make([]byte, 1)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Read(buf)
	assert.Error(t, err) // EOF or connection reset
}

func TestTCPProxy_Forward(t *testing.T) {
	// Start a test echo backend.
	backend := newTestTCPEchoServer(t)

	r := NewRouter(config.BackendsConfig{
		CelestiaAppRPC:  config.Endpoints{"http://127.0.0.1:26657"},
		CelestiaNodeRPC: config.Endpoints{"http://127.0.0.1:26658"},
		CelestiaAppP2P:  config.Endpoints{backend.Addr()},
	})

	m := testMetrics(t)
	p := NewTCPProxy(r, zap.NewNop(), m)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = p.Serve(lis) }()
	t.Cleanup(p.Close)

	// Connect through the proxy.
	conn, err := net.DialTimeout("tcp", lis.Addr().String(), 2*time.Second)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	// Send data and verify echo.
	msg := []byte("hello p2p proxy")
	_, err = conn.Write(msg)
	require.NoError(t, err)

	buf := make([]byte, len(msg))
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, msg, buf)
}

func TestTCPProxy_LoadBalancing(t *testing.T) {
	// Start two backends.
	backend1 := newTestTCPEchoServer(t)
	backend2 := newTestTCPEchoServer(t)

	r := NewRouter(config.BackendsConfig{
		CelestiaAppRPC:  config.Endpoints{"http://127.0.0.1:26657"},
		CelestiaNodeRPC: config.Endpoints{"http://127.0.0.1:26658"},
		CelestiaAppP2P:  config.Endpoints{backend1.Addr(), backend2.Addr()},
	})

	m := testMetrics(t)
	p := NewTCPProxy(r, zap.NewNop(), m)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = p.Serve(lis) }()
	t.Cleanup(p.Close)

	// Make multiple connections.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", lis.Addr().String(), 2*time.Second)
			if err != nil {
				return
			}
			// Send and receive to ensure the connection is established through to backend.
			msg := []byte("ping")
			_, _ = conn.Write(msg)
			buf := make([]byte, len(msg))
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, _ = io.ReadFull(conn, buf)
			_ = conn.Close()
		}()
	}
	wg.Wait()

	// Give a moment for connection counts to settle.
	time.Sleep(50 * time.Millisecond)

	// Both backends should have received connections.
	assert.Greater(t, backend1.ConnCount(), int64(0), "backend 1 should have received connections")
	assert.Greater(t, backend2.ConnCount(), int64(0), "backend 2 should have received connections")
	assert.Equal(t, int64(10), backend1.ConnCount()+backend2.ConnCount())
}
