package proxy

import (
	"context"
	"io"
	"net"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/SigmaUno/da-proxy/internal/metrics"
)

// rawCodec is a gRPC codec that passes through raw bytes without
// marshaling/unmarshaling via protobuf. This enables transparent proxying
// of any gRPC service without proto definitions.
type rawCodec struct{}

func (rawCodec) Marshal(v interface{}) ([]byte, error) {
	out, ok := v.(*frame)
	if !ok {
		return nil, status.Errorf(codes.Internal, "rawCodec: unexpected type %T", v)
	}
	return out.payload, nil
}

func (rawCodec) Unmarshal(data []byte, v interface{}) error {
	dst, ok := v.(*frame)
	if !ok {
		return status.Errorf(codes.Internal, "rawCodec: unexpected type %T", v)
	}
	dst.payload = data
	return nil
}

func (rawCodec) Name() string { return "raw" }

// frame holds a raw gRPC message payload for transparent forwarding.
type frame struct {
	payload []byte
}

// GRPCProxy is a transparent gRPC reverse proxy that forwards all gRPC
// calls to configured backends with latency-aware load balancing.
type GRPCProxy struct {
	server  *grpc.Server
	router  Router
	logger  *zap.Logger
	metrics *metrics.Metrics
}

// NewGRPCProxy creates a new transparent gRPC reverse proxy.
func NewGRPCProxy(router Router, logger *zap.Logger, m *metrics.Metrics) *GRPCProxy {
	p := &GRPCProxy{
		router:  router,
		logger:  logger,
		metrics: m,
	}
	p.server = grpc.NewServer(
		grpc.UnknownServiceHandler(p.streamHandler),
		grpc.ForceServerCodec(rawCodec{}),
	)
	return p
}

// Serve starts the gRPC server on the given listener.
func (p *GRPCProxy) Serve(lis net.Listener) error {
	return p.server.Serve(lis)
}

// GracefulStop gracefully stops the gRPC server.
func (p *GRPCProxy) GracefulStop() {
	p.server.GracefulStop()
}

// streamHandler is the grpc.UnknownServiceHandler that intercepts all calls.
func (p *GRPCProxy) streamHandler(_ interface{}, serverStream grpc.ServerStream) error {
	fullMethod, ok := grpc.MethodFromServerStream(serverStream)
	if !ok {
		return status.Error(codes.Internal, "failed to get method from stream")
	}

	start := time.Now()

	// Select a backend endpoint.
	endpoint := p.router.TargetURL(BackendCelestiaAppGRPC)
	if endpoint == "" {
		return status.Error(codes.Unavailable, "no gRPC backend available")
	}

	p.logger.Debug("grpc_request",
		zap.String("method", fullMethod),
		zap.String("backend", endpoint),
	)

	// Dial the backend. Use passthrough resolver so gRPC uses the address as-is
	// without DNS resolution (endpoints are already resolved host:port).
	target := "passthrough:///" + endpoint
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})),
	)
	if err != nil {
		p.logger.Error("gRPC backend dial failed",
			zap.String("endpoint", endpoint),
			zap.Error(err),
		)
		return status.Errorf(codes.Unavailable, "backend connection failed: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Forward incoming metadata.
	md, _ := metadata.FromIncomingContext(serverStream.Context())

	// Create a cancellable context derived from the server stream's context.
	// When either forwarding direction finishes (error or EOF from backend),
	// we cancel this context to unblock the other goroutine's RecvMsg.
	ctx, cancel := context.WithCancel(serverStream.Context())
	defer cancel()

	ctx = metadata.NewOutgoingContext(ctx, md)

	// Open a stream to the backend.
	desc := &grpc.StreamDesc{
		ServerStreams: true,
		ClientStreams: true,
	}

	clientStream, err := conn.NewStream(ctx, desc, fullMethod)
	if err != nil {
		p.logger.Error("gRPC backend stream failed",
			zap.String("endpoint", endpoint),
			zap.String("method", fullMethod),
			zap.Error(err),
		)
		p.recordMetrics(fullMethod, start, err)
		return err
	}

	// Bidirectional forwarding with cancellation.
	// When one direction completes, we cancel the context to unblock the other.
	errc := make(chan error, 2)

	// Client -> Backend.
	go func() {
		err := p.forwardClientToBackend(serverStream, clientStream)
		// If client finished sending (EOF), don't cancel — let backend finish responding.
		// If client errored, cancel to unblock backend->client.
		if err != nil {
			cancel()
		}
		errc <- err
	}()

	// Backend -> Client.
	go func() {
		err := p.forwardBackendToClient(clientStream, serverStream)
		// Backend finished (EOF or error) — cancel to unblock client->backend RecvMsg.
		cancel()
		errc <- err
	}()

	// Wait for both directions to finish.
	// After cancel(), the blocked goroutine will unblock quickly.
	var retErr error
	for i := 0; i < 2; i++ {
		if err := <-errc; err != nil {
			// Keep the first meaningful error (ignore context cancelled from our own cancel).
			if retErr == nil {
				retErr = err
			}
		}
	}

	// Record latency and metrics.
	p.router.RecordLatency(BackendCelestiaAppGRPC, endpoint, time.Since(start))
	p.recordMetrics(fullMethod, start, retErr)

	// Forward backend trailers to client.
	serverStream.SetTrailer(clientStream.Trailer())

	grpcCode := codes.OK
	if retErr != nil {
		if s, ok := status.FromError(retErr); ok {
			grpcCode = s.Code()
		} else {
			grpcCode = codes.Unknown
		}
	}

	p.logger.Info("grpc_request_complete",
		zap.String("method", fullMethod),
		zap.String("backend", endpoint),
		zap.String("grpc_code", grpcCode.String()),
		zap.Duration("latency", time.Since(start)),
	)

	return retErr
}

// forwardClientToBackend reads frames from the client and sends them to the backend.
func (p *GRPCProxy) forwardClientToBackend(src grpc.ServerStream, dst grpc.ClientStream) error {
	for {
		f := &frame{}
		if err := src.RecvMsg(f); err != nil {
			_ = dst.CloseSend()
			if err == io.EOF {
				return nil
			}
			return err
		}
		if err := dst.SendMsg(f); err != nil {
			return err
		}
	}
}

// forwardBackendToClient reads frames from the backend and sends them to the client.
func (p *GRPCProxy) forwardBackendToClient(src grpc.ClientStream, dst grpc.ServerStream) error {
	for {
		f := &frame{}
		if err := src.RecvMsg(f); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if err := dst.SendMsg(f); err != nil {
			return err
		}
	}
}

// recordMetrics records Prometheus metrics for a completed gRPC request.
func (p *GRPCProxy) recordMetrics(fullMethod string, start time.Time, err error) {
	if p.metrics == nil {
		return
	}

	code := codes.OK
	if err != nil {
		if s, ok := status.FromError(err); ok {
			code = s.Code()
		} else {
			code = codes.Unknown
		}
	}

	p.metrics.GRPCRequestsTotal.With(prometheus.Labels{
		"method":    fullMethod,
		"grpc_code": code.String(),
	}).Inc()

	p.metrics.GRPCRequestDuration.With(prometheus.Labels{
		"method": fullMethod,
	}).Observe(time.Since(start).Seconds())
}
