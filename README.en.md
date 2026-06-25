<div align="center">

<img src="web/public/images/logo/auth-logo.svg" alt="MCP Proxy Gateway" width="280" />

# MCP Proxy Gateway

**An MCP aggregation, governance and observability gateway for production AI systems**

Connect heterogeneous MCP services once, aggregate tools into one governed catalog, distribute them per API Key, and operate the whole surface from a modern admin console.

[![Release](https://img.shields.io/github/v/release/xylplm/mcp-proxy-gateway?sort=semver&label=release)](https://github.com/xylplm/mcp-proxy-gateway/releases)
[![Docker Image](https://img.shields.io/docker/v/xylplm/mcp-proxy-gateway?sort=semver&label=docker&logo=docker)](https://hub.docker.com/r/xylplm/mcp-proxy-gateway)
[![Docker Pulls](https://img.shields.io/docker/pulls/xylplm/mcp-proxy-gateway?logo=docker)](https://hub.docker.com/r/xylplm/mcp-proxy-gateway)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Vue](https://img.shields.io/badge/Vue-3-42b883?logo=vuedotjs&logoColor=white)](https://vuejs.org/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](#license)

[中文](README.md) · **English**

[Capabilities](#capabilities) · [Quick Start](#quick-start) · [Service Endpoints](#service-endpoints) · [Configuration](#configuration-and-persistence) · [Development](#development) · [Release](#release)

</div>

---

## What It Is

MCP Proxy Gateway is a self-hosted control plane and service gateway for MCP. It connects MCP servers scattered across teams, runtimes and transports, then exposes a stable, auditable, rate-limited and tenant-aware MCP surface to clients such as Claude Code, Cursor, Cherry Studio, XiaoZhi AI and any other MCP-compatible consumer.

The backend is written in Go. The admin console is built with Vue 3, Vite and Tailwind CSS. Frontend assets are embedded into the Go binary with `go:embed`; the released Docker image ships both the gateway and the console. PostgreSQL stores business data, while Redis backs cache, rate limiting and statistics buffers.

```text
┌────────────────────────────────────────────────────────────────────────────┐
│                              Admin Control Plane                           │
│  Vue 3 Console + REST API + JWT Login + Settings + Backup + Diagnostics     │
└──────────────────────────────────┬─────────────────────────────────────────┘
                                   │
┌──────────────────────────────────▼─────────────────────────────────────────┐
│                              MCP Service Plane                             │
│  /mcp/sse  /mcp/http  /mcp/ws  /mcp/smart/*                                 │
│  API Key auth · IP/CIDR ACL · Rate limit · Security Center · Smart/Full mode │
└──────────────────────────────────┬─────────────────────────────────────────┘
                                   │
┌──────────────────────────────────▼─────────────────────────────────────────┐
│                         Aggregation and Governance Pipeline                 │
│  Tool sync · Cache-first catalog · Alias · Filters · Tool policy · Routing   │
└──────────────────────────────────┬─────────────────────────────────────────┘
                                   │
┌──────────────────────────────────▼─────────────────────────────────────────┐
│                              Upstream MCP Cluster                          │
│  stdio · SSE · Streamable HTTP · WebSocket · OpenAPI/REST as virtual tools   │
└────────────────────────────────────────────────────────────────────────────┘
                                   │
                PostgreSQL business data + Redis cache/rate-limit/stat buffer
```

## Capabilities

### Multi-Transport MCP Gateway

- Connect upstream MCP servers over `stdio`, `sse`, `streamable-http` and `websocket`.
- Convert OpenAPI/REST services into virtual MCP tools, bringing existing HTTP APIs into the AI tool layer.
- Expose the aggregated capability over SSE, Streamable HTTP and WebSocket.
- Provide `/mcp/smart/*` endpoints where clients discover and call tools on demand, reducing context-window pressure.

### Tool Aggregation, Routing and Governance

- Merge tools from multiple upstreams into a single global catalog with ordering, enable/disable, manual refresh, scheduled sync and change summaries.
- Route same-name tools across multiple sources using smart balancing or priority fill, automatically avoiding unavailable or quota-exhausted sources.
- Configure upstream quotas per second, minute, hour, day, week or month with timezone support.
- Rename tools and rewrite descriptions through alias rules; filter tools globally, per upstream or per API Key.
- Apply tool policies by name pattern to override routing, enable short-TTL successful-result caching, and attach risk tags.
- Inspect the catalog, source details, schema conflicts, risk hints and invoke tools directly from the admin Playground.

### API Keys, Tenancy and Access Control

- Manage the full API Key lifecycle: create, enable, disable and delete. Plaintext keys are shown only once on creation.
- Accept keys via `X-API-Key`, `Authorization: Bearer <key>` or `api_key` query parameter.
- Configure per-key visible tools, IP/CIDR allowlists and rate limits.
- Keep admin JWT authentication and public MCP API Key authentication on completely separate middleware chains.

### Security, Audit and Observability

- Security Center supports `off`, `monitor` and `enforce` modes for authentication failures, ACL denies and automatic blocking.
- Configure trusted proxy CIDRs, exempt CIDRs, block escalation windows and manual unblock flows.
- Statistics cover summaries, trends, upstream counts, tool rankings, API Key usage profiles, error rankings and call health.
- Call records can be paginated, inspected, exported and cleared for troubleshooting and cost analysis.
- Audit logs capture logins, resource changes and denied access attempts, with query and export support.
- Runtime system logs are searchable, exportable and clearable from the console; diagnostic bundles can be exported with one click.

### Admin Console and Operations

- Responsive Vue 3 console covering dashboard, upstreams, API Keys, rules, tool catalog, call records, statistics, audit, security, system logs, settings and profile pages.
- Built-in template market for common third-party MCP services with category browsing, search and one-click prefill.
- Import/export upstreams in MCP JSON format, and export/preview/import full gateway backups.
- Run the public MCP service on an independent port, keeping the admin console private while exposing only the service plane.
- XiaoZhi AI integration connects outbound over WebSocket and publishes the aggregated MCP capability to voice terminals.

## Tech Stack

| Layer | Choice |
| --- | --- |
| Backend | Go 1.25, Gin, GORM, pgx/PostgreSQL, go-redis/v9, robfig/cron/v3, golang-jwt/v5, MCP Go SDK |
| Frontend | Vue 3, Vite, TypeScript, Tailwind CSS, Pinia, Vue Router, ApexCharts |
| Storage | PostgreSQL for business data and partitioned statistics; Redis for tool cache, rate limits and stats buffers |
| Deployment | GitHub Actions builds linux/amd64 and linux/arm64 binaries, then packages them into a multi-arch Alpine Docker image |

## Quick Start

The gateway image contains both the backend and the admin console. Runtime requires PostgreSQL and Redis. Docker Compose is the easiest way to start everything.

### Docker Compose

Create `docker-compose.yml`:

```yaml
services:
  gateway:
    container_name: mcp-proxy-gateway
    image: xylplm/mcp-proxy-gateway:latest
    ports:
      - "8080:8080"
      # If you enable a dedicated public MCP port, for example :8081, expose it too.
      # - "8081:8081"
    environment:
      MPG_PG_DSN: "postgres://mpg:mpg_password@postgres:5432/mpg?sslmode=disable"
      MPG_REDIS_ADDR: "redis:6379"
      MPG_REDIS_PASSWORD: ""
      MPG_DATA_DIR: "/data"
    volumes:
      - mpg_data:/data
    depends_on:
      - postgres
      - redis
    restart: unless-stopped

  postgres:
    container_name: mpg-postgres
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: mpg
      POSTGRES_PASSWORD: mpg_password
      POSTGRES_DB: mpg
    volumes:
      - pg_data:/var/lib/postgresql/data
    restart: unless-stopped

  redis:
    container_name: mpg-redis
    image: redis:7-alpine
    restart: unless-stopped

volumes:
  mpg_data:
  pg_data:
```

Start it:

```bash
docker compose up -d
```

Open `http://localhost:8080`. The first launch redirects to the admin registration page.

### docker run

```bash
docker run -d --name mcp-proxy-gateway \
  -p 8080:8080 \
  -e MPG_PG_DSN="postgres://user:pass@host:5432/db?sslmode=disable" \
  -e MPG_REDIS_ADDR="host:6379" \
  -e MPG_REDIS_PASSWORD="" \
  -e MPG_DATA_DIR="/data" \
  -v mpg_data:/data \
  xylplm/mcp-proxy-gateway:latest
```

If you enable a dedicated MCP service port such as `:8081`, add `-p 8081:8081`.

### Image Tags

Images are published to Docker Hub: [`xylplm/mcp-proxy-gateway`](https://hub.docker.com/r/xylplm/mcp-proxy-gateway), with `linux/amd64` and `linux/arm64` support.

| Tag | Description |
| --- | --- |
| `latest` | Latest release |
| `1.0.YYYYMMDDHHmm` | Immutable Beijing-time date version for rollback and traceability |

```bash
docker pull xylplm/mcp-proxy-gateway:latest
docker pull xylplm/mcp-proxy-gateway:1.0.202606071302
```

> `stdio` upstreams launch subprocesses inside the gateway runtime. If a stdio MCP server depends on Node.js, Python, uvx or another runtime, use a custom image or preinstall the required runtime. Remote SSE, Streamable HTTP, WebSocket and OpenAPI upstreams do not have this constraint.

## Service Endpoints

By default, the gateway listens on `:8080` for the admin console, admin APIs, health checks and public MCP APIs. You can configure a dedicated `public_mcp_addr` from the settings page and move the MCP service plane to a separate port.

| Route | Auth | Purpose |
| --- | --- | --- |
| `/`, `/assets/*` | none | Embedded admin SPA |
| `/api/auth/*` | none | Initialization status, admin registration and login |
| `/api/admin/*` | Admin JWT | Management APIs for upstreams, rules, API Keys, tools, stats, audit, security, settings, backup and templates |
| `/mcp/sse` | API Key + security + ACL + rate limit | Full-mode SSE MCP endpoint |
| `/mcp/http` | API Key + security + ACL + rate limit | Full-mode Streamable HTTP MCP endpoint |
| `/mcp/ws` | API Key + security + ACL + rate limit | Full-mode WebSocket MCP endpoint |
| `/mcp/smart/sse` | API Key + security + ACL + rate limit | Smart-mode SSE MCP endpoint |
| `/mcp/smart/http` | API Key + security + ACL + rate limit | Smart-mode Streamable HTTP MCP endpoint |
| `/mcp/smart/ws` | API Key + security + ACL + rate limit | Smart-mode WebSocket MCP endpoint |
| `/healthz` | none | Liveness probe |
| `/api/admin/health` | Admin JWT | Detailed health including dependencies, upstreams and XiaoZhi state |

API Keys can be provided using either header:

```http
X-API-Key: <your-api-key>
Authorization: Bearer <your-api-key>
```

The query parameter `?api_key=<your-api-key>` is also supported.

## Configuration and Persistence

Database, Redis and data directory are configured via environment variables:

| Variable | Required | Default | Description |
| --- | :---: | --- | --- |
| `MPG_PG_DSN` | yes | none | PostgreSQL DSN, for example `postgres://user:pass@host:5432/db?sslmode=disable` |
| `MPG_REDIS_ADDR` | yes | none | Redis address, for example `host:6379` |
| `MPG_REDIS_PASSWORD` | no | empty | Redis password |
| `MPG_DATA_DIR` | no | `/data` | Directory for YAML config and local persistent data |

All other settings live in the YAML file under `MPG_DATA_DIR` and can be changed from the admin console:

- `server`: admin address, dedicated MCP address, MCP-on-admin-port switch and log level.
- `auth`: admin session timeout; JWT secret is generated automatically on first launch.
- `sync`: tool sync cron and timeout.
- `connection`: connect timeout, retry backoff and failure threshold.
- `aggregation`: upstream call timeout and same-name tool routing strategy.
- `mcp_api`: smart discovery limit and POST body size limit.
- `statistics` and `audit`: retention and default page sizes.
- `security`: monitor/enforce mode, failure windows, automatic blocking, trusted proxies and exempt CIDRs.
- `xiaozhi`: XiaoZhi integration switch, WebSocket endpoint and smart/full mode.

Persistence layout:

- `/data`: YAML config and local persistent files.
- PostgreSQL: upstreams, tool cache indexes, rules, API Key metadata, ACLs, rate-limit configs, call statistics, audit records and security events.
- Redis: tool cache, API Key rate-limit counters, statistics write buffers and runtime helper state.

Startup validates required env vars, reads YAML, generates a missing JWT secret, connects to PostgreSQL/Redis, and initializes the database schema before serving. Critical dependency failures are fail-fast.

## Suggested Flow

1. Add one or more upstream MCP servers from Upstreams, optionally using the template market.
2. Test connectivity and refresh the tool list.
3. Use Rules to normalize tool names, hide unwanted tools and attach tool policies.
4. Create API Keys with per-client visible tools, CIDR allowlists and rate limits.
5. Connect clients to `/mcp/smart/http` or `/mcp/smart/sse` first; use full-mode endpoints when the client can handle the entire catalog.
6. Monitor Call Records, Statistics, Audit and Security Center for quality and risk.

## Development

### Requirements

- Go 1.25+
- Node.js 24+, matching the CI frontend toolchain
- PostgreSQL 16+
- Redis 7+

### Start Local Dependencies

```bash
docker run -d --name mpg-pg -p 5432:5432 \
  -e POSTGRES_USER=mpg \
  -e POSTGRES_PASSWORD=mpg_password \
  -e POSTGRES_DB=mpg \
  postgres:16-alpine

docker run -d --name mpg-redis -p 6379:6379 redis:7-alpine
```

### Backend

```powershell
$env:MPG_PG_DSN = "postgres://mpg:mpg_password@localhost:5432/mpg?sslmode=disable"
$env:MPG_REDIS_ADDR = "localhost:6379"
$env:MPG_DATA_DIR = "./data"

go run ./cmd/gateway
```

Common checks:

```bash
go test ./...
go vet ./...
gofmt -l .
```

### Frontend

```bash
cd web
npm ci
npm run dev
```

Common checks:

```bash
npm run build
npm run lint
npm run format:check
npm run test:unit
```

For production builds, CI builds `web/dist`, copies it into `internal/static/dist`, and compiles the Go binary with embedded frontend assets.

## Project Structure

```text
mcp-proxy-gateway/
├── cmd/gateway/            # Gateway entrypoint
├── internal/
│   ├── app/                # Application wiring, route facets, lifecycle
│   ├── aggregation/        # Tool aggregation, routing, quota and cache policy
│   ├── apikey/             # API Keys, ACL, rate limiting and service-plane auth
│   ├── audit/              # Audit recording and query
│   ├── auth/               # Admin registration, login and JWT middleware
│   ├── backup/             # Backup import/export
│   ├── cache/              # Tool cache
│   ├── config/             # Environment and YAML config
│   ├── domain/             # Domain models, rule engine and error model
│   ├── health/             # Liveness and detailed health reporting
│   ├── httpapi/            # Admin REST API composition layer
│   ├── manager/            # Upstream lifecycle and retry backoff
│   ├── mcpapi/             # Public MCP multi-transport service
│   ├── security/           # Security Center and automatic blocking
│   ├── stats/              # Call statistics, trends and records
│   ├── store/              # PostgreSQL repositories and schema initialization
│   ├── sync/               # Tool sync and cron scheduling
│   ├── syslog/             # Runtime log buffer and export
│   ├── template/           # Template market
│   ├── transport/          # Upstream transport adapters
│   └── xiaozhi/            # XiaoZhi AI integration
├── web/                    # Vue 3 admin console
├── Dockerfile              # Alpine runtime image for CI-built binaries
└── .github/workflows/      # Release pipeline
```

## Release

The release workflow lives in `.github/workflows/release.yml` and is triggered manually:

- `quality`: install Node 24 and Go, run frontend dependency install, ESLint, `go vet`, `gofmt` warning and `go test ./...`.
- `version`: generate `1.0.<YYYYMMDDHHmm>` in Beijing time.
- `build-frontend`: write the frontend version, run `npm run build`, upload `web/dist`.
- `build-backend`: build static `linux/amd64` and `linux/arm64` binaries with embedded frontend assets.
- `docker`: package the binaries with the Alpine Dockerfile and push a multi-arch image.
- `release`: create the `v<version>` Git tag and GitHub Release.

Required repository secrets:

| Secret | Description |
| --- | --- |
| `DOCKERHUB_USERNAME` | Docker Hub username |
| `DOCKERHUB_TOKEN` | Docker Hub access token |

## License

Licensed under the [MIT](LICENSE) License.
