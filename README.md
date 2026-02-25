# DA-Proxy
[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2FSigmaUno%2Fda-proxy.svg?type=shield)](https://app.fossa.com/projects/git%2Bgithub.com%2FSigmaUno%2Fda-proxy?ref=badge_shield)
[![Build Status](https://github.com/SigmaUno/da-proxy/workflows/CI/badge.svg)](https://github.com/SigmaUno/da-proxy/actions?query=branch%3Amain+workflow%3A%22CI%22)
[![made_with golang](https://img.shields.io/badge/made_with-golang-blue.svg)](https://golang.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![codecov](https://codecov.io/gh/SigmaUno/da-proxy/branch/main/graph/badge.svg)](https://codecov.io/gh/SigmaUno/da-proxy)
[![Latest release](https://img.shields.io/github/v/release/SigmaUno/da-proxy.svg)](https://github.com/SigmaUno/da-proxy/releases)

A unified HTTPS reverse proxy gateway for Celestia infrastructure. DA-Proxy exposes a single authenticated endpoint that intelligently routes incoming requests to the appropriate Celestia backend service based on the RPC method being called.

## Features

- **Unified endpoint** with method-based routing to celestia-app (consensus) and celestia-node (DA)
- **gRPC reverse proxy** on a dedicated port (`:9090`) — transparent proxying of all gRPC services without authentication
- **TCP/P2P proxy** on a dedicated port (`:26656`) — raw TCP forwarding to CometBFT P2P backends without authentication
- **Latency-aware load balancing** — EWMA-based routing to the fastest backend endpoint with 10% exploration to prevent starvation
- **URL path token authentication** (QuickNode-style: `/<token>/...`)
- **Per-token rate limiting** with configurable requests-per-minute
- **Structured logging** via Uber Zap (JSON to stdout) with client IP on every request (HTTP and gRPC)
- **Prometheus metrics** on a dedicated port (`:9191`)
- **Admin REST API** with Basic Auth for request logs, health, per-endpoint latency stats, and metrics summaries
- **Unified request log storage** — HTTP/RPC, gRPC, and TCP requests are stored in the ring buffer and database, queryable via the admin API
- **Backend health checks** with automatic monitoring
- **Graceful shutdown** with in-flight request draining

## Architecture

```
                    ┌─────────────────────────────────────────┐
                    │             DA-Proxy (Go)                │
                    │                                           │
  HTTPS :443        │  ┌──────────┐    ┌───────────────┐       │
─────────────────►  │  │   Auth   │───►│ Method Router  │       │
 (URL path token)   │  │Middleware│    │                │       │
                    │  └──────────┘    └──────┬────────┘       │
                    │                    │              │       │
                    │           ┌────────┘              │       │
                    │           ▼                       ▼       │
                    │  ┌────────────┐          ┌──────────┐    │
                    │  │celestia-app│          │celestia-  │    │
                    │  │ RPC :26657 │          │node :26658│    │
                    │  └────────────┘          └──────────┘    │
                    │                                           │
  gRPC :9090        │  ┌───────────────────────────────┐       │
─────────────────►  │  │  Transparent gRPC Proxy       │       │
 (no auth)          │  │  (UnknownServiceHandler)      │       │
                    │  └──────────────┬────────────────┘       │
                    │                 ▼                         │
                    │        ┌────────────────┐                │
                    │        │ celestia-app   │                │
                    │        │  gRPC :9090    │                │
                    │        └────────────────┘                │
                    │                                           │
  TCP :26656        │  ┌───────────────────────────────┐       │
─────────────────►  │  │  TCP/P2P Proxy (raw forward)  │       │
 (no auth)          │  └──────────────┬────────────────┘       │
                    │                 ▼                         │
                    │        ┌────────────────┐                │
                    │        │ celestia-app   │                │
                    │        │  P2P :26656    │                │
                    │        └────────────────┘                │
                    └─────────────────────────────────────────┘
```

## Routing Rules

**HTTP/JSON-RPC proxy (`:443`, authenticated):**

| Condition | Backend |
|-----------|---------|
| Method prefix in `blob`, `header`, `share`, `das`, `state`, `p2p`, `node` | celestia-node:26658 |
| Everything else | celestia-app:26657 (Tendermint RPC) |

**gRPC proxy (`:9090`, no authentication):**

| Condition | Backend |
|-----------|---------|
| All gRPC services/methods | celestia-app gRPC backends |

The gRPC proxy transparently forwards all gRPC calls without requiring proto definitions. It supports unary, client-streaming, server-streaming, and bidirectional RPCs.

**TCP/P2P proxy (`:26656`, no authentication):**

| Condition | Backend |
|-----------|---------|
| All TCP connections | celestia-app P2P backends |

The TCP proxy performs raw bidirectional byte forwarding for CometBFT P2P traffic.

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
  grpc_listen: ":9090"        # gRPC proxy listen address
  p2p_listen: ":26656"        # TCP/P2P proxy listen address
  tls_cert: ""                # Path to TLS cert (empty = no TLS)
  tls_key: ""                 # Path to TLS key
  read_timeout: 30s
  write_timeout: 30s
  max_body_size: "10MB"

backends:
  celestia_app_rpc: "http://127.0.0.1:26657"
  celestia_node_rpc: "http://127.0.0.1:26658"
  # celestia_app_grpc: "127.0.0.1:9090"  # Optional: enables gRPC proxy
  # celestia_app_p2p: "127.0.0.1:26656"  # Optional: enables TCP/P2P proxy
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

  # gRPC backends (optional, enables gRPC proxy on grpc_listen port)
  celestia_app_grpc:
    - "app-node-1:9090"
    - "app-node-2:9090"

  # P2P backends (optional, enables TCP proxy on p2p_listen port)
  celestia_app_p2p:
    - "app-node-1:26656"
    - "app-node-2:26656"

  # Requests for heights older than (head - pruning_window) go to archival nodes.
  pruning_window: 100000

  health_check_interval: 30s
```

A single URL still works (no list needed):

```yaml
backends:
  celestia_app_rpc: "http://127.0.0.1:26657"
  celestia_node_rpc: "http://127.0.0.1:26658"
  celestia_app_grpc: "127.0.0.1:9090"
  celestia_app_p2p: "127.0.0.1:26656"
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

Pre-built images are published to GitHub Container Registry on every push to `main` and on version tags.

```bash
# Pull the latest image
docker pull ghcr.io/sigmauno/da-proxy:latest

# Or a specific version
docker pull ghcr.io/sigmauno/da-proxy:0.1.0
```

### Docker Compose

The included `docker-compose.yaml` runs DA-Proxy. Configure your Celestia backend endpoints in `configs/config.yaml`.

```bash
# 1. Copy and edit the config
cp configs/config.example.yaml configs/config.yaml

# 2. Start the full stack (pulls the pre-built image)
docker compose up -d

# 3. Build from source instead of pulling (optional)
docker compose up -d --build

# 4. Check status
docker compose ps

# 5. View logs
docker compose logs -f da-proxy

# 6. Stop everything
docker compose down
```

To run a specific version, set the `DA_PROXY_VERSION` environment variable:

```bash
DA_PROXY_VERSION=0.1.0 docker compose up -d
```

The compose file exposes these ports by default:

| Port | Service |
|------|---------|
| 443 | HTTPS / JSON-RPC proxy |
| 8080 | Admin API |
| 9090 | gRPC proxy |
| 9191 | Prometheus metrics |
| 26656 | TCP/P2P proxy |

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
```

The token is stripped from the path before forwarding to backends and is never logged in plaintext.

### gRPC Queries

gRPC requests go directly to port `:9090` without authentication:

```bash
# List all available gRPC services
grpcurl -plaintext proxy.example.com:9090 list

# Query bank balance
grpcurl -plaintext -d '{"address":"celestia1..."}' \
  proxy.example.com:9090 cosmos.bank.v1beta1.Query/AllBalances
```

## Admin API

The admin API is served on a separate port (default `:8080`) with HTTP Basic Auth.

| Endpoint | Description |
|----------|-------------|
| `GET /admin/api/health` | Backend health status |
| `GET /admin/api/status` | Proxy uptime, version, token count |
| `GET /admin/api/backends` | Configured backends with per-endpoint EWMA latency, request counts, and health |
| `GET /admin/api/logs` | Query request logs with filtering |
| `GET /admin/api/logs/stream` | SSE stream of real-time logs |
| `GET /admin/api/logs/export` | Export logs as JSON or CSV |
| `GET /admin/api/metrics/summary` | Aggregated metrics summary |

### Log Query Parameters

HTTP/RPC, gRPC, and TCP/P2P requests all appear in the logs endpoint. Each log entry includes `client_ip` for identifying callers. gRPC entries use `path: "grpc"` and `backend: "celestia-app-grpc"`, TCP entries use `path: "tcp"` and `backend: "celestia-app-p2p"`.

| Parameter | Type | Description |
|-----------|------|-------------|
| `method` | string | Filter by RPC method (gRPC uses full method path, e.g. `/cosmos.bank.v1beta1.Query/Balance`) |
| `token_name` | string | Filter by token name (empty for gRPC — no auth) |
| `backend` | string | Filter by backend (e.g. `celestia-app-grpc`) |
| `status_code` | int | Filter by status code (HTTP status for RPC, gRPC code for gRPC) |
| `status_min` / `status_max` | int | Status code range |
| `from` / `to` | ISO 8601 | Time range |
| `latency_min` | float | Minimum latency in ms |
| `limit` | int | Page size (default: 100, max: 1000) |
| `sort` | string | `asc` or `desc` (default: `desc`) |

## Prometheus Metrics

Metrics are served at `http://localhost:9191/metrics`:

| Metric | Type | Description |
|--------|------|-------------|
| `daproxy_requests_total` | Counter | Total proxied HTTP requests |
| `daproxy_request_duration_seconds` | Histogram | HTTP request latency |
| `daproxy_grpc_requests_total` | Counter | Total proxied gRPC requests |
| `daproxy_grpc_request_duration_seconds` | Histogram | gRPC request latency |
| `daproxy_tcp_connections_total` | Counter | Total TCP proxy connections |
| `daproxy_tcp_connection_duration_seconds` | Histogram | TCP connection duration |
| `daproxy_tcp_bytes_total` | Counter | Bytes transferred (sent/received) |
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
│   ├── proxy/                     # Router, HTTP handler, health checks
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
- The gRPC proxy (`:9090`) and TCP/P2P proxy (`:26656`) have no authentication — restrict access via firewall rules or bind to an internal interface
- Backend ports (26657, 26658, 9090, 26656) should not be publicly accessible
- Token values are never logged or forwarded to backends
- TLS termination can be done at the proxy or an upstream load balancer

## License

See [LICENSE](LICENSE) for details.


[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2FSigmaUno%2Fda-proxy.svg?type=large)](https://app.fossa.com/projects/git%2Bgithub.com%2FSigmaUno%2Fda-proxy?ref=badge_large)