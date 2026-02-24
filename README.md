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
- **Dedicated gRPC port** (`:9090`) — unauthenticated transparent proxy to Cosmos SDK gRPC
- **Graceful shutdown** with in-flight request draining

## Architecture

```
                    ┌───────────────────────────────────────┐
                    │            DA-Proxy (Go)               │
                    │                                         │
  HTTPS :443        │  ┌──────────┐    ┌───────────────┐     │
─────────────────►  │  │   Auth   │───►│ Method Router  │     │
 (URL path token)   │  │Middleware│    │                │     │
                    │  └──────────┘    └──────┬────────┘     │
                    │                    │    │    │          │
                    │           ┌────────┘    │    └───────┐  │
                    │           ▼             ▼            ▼  │
                    │  ┌────────────┐ ┌──────────┐ ┌──────┐  │
                    │  │celestia-app│ │celestia-  │ │ REST │  │
                    │  │ RPC :26657 │ │node :26658│ │:1317 │  │
                    │  └────────────┘ └──────────┘ └──────┘  │
                    │                                         │
  gRPC :9090        │         (transparent passthrough)       │
─────────────────►  │  ┌──────────────────────────────────┐  │
 (no auth)          │  │   celestia-app gRPC backend       │  │
                    │  └──────────────────────────────────┘  │
                    └───────────────────────────────────────┘
```

## Routing Rules

**HTTP/JSON-RPC proxy (`:443`, authenticated):**

| Condition | Backend |
|-----------|---------|
| Path starts with `/cosmos/`, `/celestia/`, `/ibc/` | celestia-app:1317 (REST) |
| Method prefix in `blob`, `header`, `share`, `das`, `state`, `p2p`, `node` | celestia-node:26658 |
| Everything else | celestia-app:26657 (Tendermint RPC) |

**gRPC proxy (`:9090`, unauthenticated):**

All gRPC traffic on port `:9090` is forwarded directly to the `celestia_app_grpc` backend with no authentication or middleware.

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
  grpc_listen: ":9090"        # gRPC passthrough port (unauthenticated)
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

### Multiple Backend Endpoints

Every backend field accepts either a single URL or a list of URLs. When multiple URLs are provided, DA-Proxy load-balances across them using round-robin selection. Health checks run independently against every endpoint.

```yaml
backends:
  # Two consensus RPC nodes (round-robin)
  celestia_app_rpc:
    - "http://app-node-1:26657"
    - "http://app-node-2:26657"

  # Two gRPC endpoints
  celestia_app_grpc:
    - "app-node-1:9090"
    - "app-node-2:9090"

  # Two REST endpoints
  celestia_app_rest:
    - "http://app-node-1:1317"
    - "http://app-node-2:1317"

  # Two DA nodes
  celestia_node_rpc:
    - "http://da-node-1:26658"
    - "http://da-node-2:26658"

  # Two archival nodes for historical queries
  celestia_app_archival_rpc:
    - "http://app-archive-1:26657"
    - "http://app-archive-2:26657"
  celestia_node_archival_rpc:
    - "http://da-archive-1:26658"
    - "http://da-archive-2:26658"

  # Requests for heights older than (head - pruning_window) go to archival nodes.
  pruning_window: 100000

  health_check_interval: 30s
```

A single URL still works (no list needed):

```yaml
backends:
  celestia_app_rpc: "http://127.0.0.1:26657"
  celestia_node_rpc: "http://127.0.0.1:26658"
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

### gRPC

gRPC is served on a dedicated port (default `:9090`) with no authentication:

```bash
# List available services
grpcurl proxy.example.com:9090 list

# Get latest block
grpcurl proxy.example.com:9090 cosmos.base.tendermint.v1beta1.Service/GetLatestBlock

# Query a balance
grpcurl -d '{"address":"celestia1..."}' proxy.example.com:9090 \
  cosmos.bank.v1beta1.Query/Balance
```

## Admin API

The admin API is served on a separate port (default `:8080`) with HTTP Basic Auth.

| Endpoint | Description |
|----------|-------------|
| `GET /admin/api/health` | Backend health status |
| `GET /admin/api/status` | Proxy uptime, version, token count |
| `GET /admin/api/backends` | Configured backends with avg latency and used methods |
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
