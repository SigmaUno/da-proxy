# DA-Proxy

A unified HTTPS reverse proxy gateway for Celestia infrastructure. DA-Proxy exposes a single authenticated endpoint that intelligently routes incoming requests to the appropriate Celestia backend service based on the RPC method being called.

## Features

- **Unified endpoint** with method-based routing to celestia-app (consensus) and celestia-node (DA)
- **URL path token authentication** (QuickNode-style: `/<token>/...`)
- **Per-token rate limiting** with configurable requests-per-minute
- **Structured logging** via Uber Zap (JSON to stdout)
- **Prometheus metrics** on a dedicated port (`:9191`)
- **Admin REST API** with Basic Auth for request logs, health, and metrics summaries
- **Request log storage** via in-memory ring buffer and SQLite
- **Backend health checks** with automatic monitoring
- **gRPC passthrough** for Cosmos SDK gRPC queries
- **Graceful shutdown** with in-flight request draining

## Architecture

```
                    ┌──────────────────────────────────────┐
                    │            DA-Proxy (Go)              │
                    │                                        │
  HTTPS request     │  ┌──────────┐    ┌───────────────┐    │
─────────────────►  │  │   Auth   │───►│ Method Router  │    │
 (URL path token)   │  │Middleware│    │                │    │
                    │  └──────────┘    └──────┬────────┘    │
                    │                    │    │    │         │
                    │           ┌────────┘    │    └──────┐  │
                    │           ▼             ▼           ▼  │
                    │  ┌────────────┐ ┌──────────┐ ┌─────┐  │
                    │  │celestia-app│ │celestia-  │ │gRPC │  │
                    │  │ RPC :26657 │ │node :26658│ │:9090│  │
                    │  └────────────┘ └──────────┘ └─────┘  │
                    └──────────────────────────────────────┘
```

## Routing Rules

| Condition | Backend |
|-----------|---------|
| `Content-Type: application/grpc` | celestia-app:9090 (gRPC) |
| Path starts with `/cosmos/`, `/celestia/`, `/ibc/` | celestia-app:1317 (REST) |
| Method prefix in `blob`, `header`, `share`, `das`, `state`, `p2p`, `node` | celestia-node:26658 |
| Everything else | celestia-app:26657 (Tendermint RPC) |

## Quick Start

### Prerequisites

- Go 1.26+
- GCC (for SQLite CGO compilation)
- Docker (optional, for containerised deployment)

### Build

```bash
# Clone the repository
git clone https://github.com/SigmaUno/da-proxy.git
cd da-proxy

# Build the binary
make build

# Run tests
make test

# Lint
make lint
```

### Configuration

Copy and edit the example configuration:

```bash
cp configs/config.example.yaml configs/config.yaml
```

Key configuration sections:

```yaml
server:
  listen: ":443"              # Proxy listen address
  tls_cert: ""                # Path to TLS cert (empty = no TLS)
  tls_key: ""                 # Path to TLS key
  read_timeout: 30s
  write_timeout: 30s
  max_body_size: "10MB"

backends:
  celestia_app_rpc: "http://127.0.0.1:26657"
  celestia_app_grpc: "127.0.0.1:9090"
  celestia_app_rest: "http://127.0.0.1:1317"
  celestia_node_rpc: "http://127.0.0.1:26658"
  health_check_interval: 30s

tokens:
  - token: "your-40-char-hex-token-here"
    name: "my-service"
    enabled: true
    rate_limit: 0             # 0 = unlimited

admin:
  listen: ":8080"
  username: "admin"
  password_hash: "$2a$10$..."  # bcrypt hash
  cors_origins:
    - "http://localhost:3000"

logging:
  level: "info"               # debug, info, warn, error
  format: "json"              # json or console

metrics:
  listen: ":9191"
  enabled: true
```

Generate a bcrypt password hash:

```bash
htpasswd -nbBC 10 "" your-password | cut -d: -f2
```

### Run

```bash
# With a config file
./bin/da-proxy -config configs/config.yaml

# Or via environment variable
DA_PROXY_CONFIG=configs/config.yaml ./bin/da-proxy

# Or using make
make run
```

### Docker

```bash
# Build the Docker image
make docker-build

# Run with Docker Compose (includes Celestia nodes)
docker compose up -d
```

## Usage

All requests go through a single endpoint with the token as the first URL path segment:

```bash
# JSON-RPC: DA node method (blob.Get)
curl -X POST https://proxy.example.com/<token>/ \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"blob.Get","params":[2683915,"AAAA..."]}'

# JSON-RPC: Consensus method (status)
curl -X POST https://proxy.example.com/<token>/ \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"status","params":[]}'

# Cosmos REST API
curl https://proxy.example.com/<token>/cosmos/bank/v1beta1/balances/celestia1...
```

The token is stripped from the path before forwarding to backends and is never logged in plaintext.

## Admin API

The admin API is served on a separate port (default `:8080`) with HTTP Basic Auth.

| Endpoint | Description |
|----------|-------------|
| `GET /admin/api/health` | Backend health status |
| `GET /admin/api/status` | Proxy uptime, version, token count |
| `GET /admin/api/logs` | Query request logs with filtering |
| `GET /admin/api/logs/stream` | SSE stream of real-time logs |
| `GET /admin/api/logs/export` | Export logs as JSON or CSV |
| `GET /admin/api/metrics/summary` | Aggregated metrics summary |

### Log Query Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `method` | string | Filter by RPC method |
| `token_name` | string | Filter by token name |
| `backend` | string | Filter by backend |
| `status_code` | int | Filter by status code |
| `status_min` / `status_max` | int | Status code range |
| `from` / `to` | ISO 8601 | Time range |
| `latency_min` | float | Minimum latency in ms |
| `limit` | int | Page size (default: 100, max: 1000) |
| `sort` | string | `asc` or `desc` (default: `desc`) |

## Prometheus Metrics

Metrics are served at `http://localhost:9191/metrics`:

| Metric | Type | Description |
|--------|------|-------------|
| `daproxy_requests_total` | Counter | Total proxied requests |
| `daproxy_request_duration_seconds` | Histogram | Request latency |
| `daproxy_errors_total` | Counter | Errors by type |
| `daproxy_backend_up` | Gauge | Backend health (1/0) |
| `daproxy_rate_limit_hits_total` | Counter | Rate limit triggers |

Plus Go runtime metrics (`go_goroutines`, `go_memstats_*`, etc.).

## Project Structure

```
da-proxy/
├── cmd/da-proxy/main.go          # Entrypoint, wires everything together
├── internal/
│   ├── config/                    # YAML configuration loading
│   ├── auth/                      # Token store and validation
│   ├── proxy/                     # Router, HTTP/gRPC handler, health checks
│   ├── middleware/                # Auth, rate limit, logging, metrics, request ID
│   ├── logging/                   # Zap logger, ring buffer, SQLite store
│   ├── metrics/                   # Prometheus metrics and server
│   └── admin/                     # Admin REST API
├── configs/config.example.yaml
├── .github/workflows/             # CI/CD pipelines
├── Dockerfile
├── docker-compose.yaml
├── Makefile
└── README.md
```

## Development

```bash
# Install dev tools
make install-tools

# Run tests with race detector
make test

# Generate coverage report
make coverage

# Lint
make lint

# Format code
make fmt
```

## Security

- Tokens should be generated with at least 160 bits of entropy (`crypto/rand`), hex-encoded (40 characters)
- The admin API should be firewalled to internal/VPN access only
- Backend ports (26657, 26658, 9090, 1317) should not be publicly accessible
- Token values are never logged or forwarded to backends
- TLS termination can be done at the proxy or an upstream load balancer

## License

See [LICENSE](LICENSE) for details.
