<div align="center">

# MCP Proxy Gateway

**A universal aggregation & proxy gateway for MCP (Model Context Protocol)**

Connect once, aggregate uniformly, distribute on demand — turn scattered MCP services into a single governable, observable, permission-controlled capability.

[![Release](https://img.shields.io/github/v/release/xylplm/mcp-proxy-gateway?sort=semver&label=release)](https://github.com/xylplm/mcp-proxy-gateway/releases)
[![Docker Image](https://img.shields.io/docker/v/xylplm/mcp-proxy-gateway?sort=semver&label=docker&logo=docker)](https://hub.docker.com/r/xylplm/mcp-proxy-gateway)
[![Docker Pulls](https://img.shields.io/docker/pulls/xylplm/mcp-proxy-gateway?logo=docker)](https://hub.docker.com/r/xylplm/mcp-proxy-gateway)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Vue](https://img.shields.io/badge/Vue-3-42b883?logo=vuedotjs&logoColor=white)](https://vuejs.org/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](#license)

**English** · [中文文档](README.zh.md)

[Features](#-features) · [Quick Start](#-quick-start-docker) · [Development](#-development) · [Environment Variables](#-environment-variables) · [Release](#-release--cicd)

</div>

---

## 📖 Overview

**MCP Proxy Gateway** is a single-process MCP gateway with a Go backend and a Vue 3 admin UI. It connects to upstream MCP services over every common transport (stdio / SSE / Streamable-HTTP / WebSocket), aggregates the tools they expose into one unified set, and re-exposes that aggregated capability over multiple MCP transports for AI clients such as Claude Code and voice terminals such as XiaoZhi AI.

The whole system ships as a **single Docker image** — the frontend assets are embedded into the binary via `go:embed`, so there is no Nginx, no separate static server. Mount a single `/data` directory to persist configuration, and you are ready to go.

```
                ┌─────────────────────────────────────────────┐
   Admin browser ┤  Admin UI (Vue3, embedded) + Admin REST API (JWT) │
                └─────────────────────────────────────────────┘
                                    │
   AI clients  ───►  External MCP API (API Key + rate limit + IP allowlist)
   XiaoZhi AI  ───►  /mcp/sse  /mcp/http  /mcp/ws
                                    │
                          ┌──────────────────────┐
                          │  Aggregation + Rule Engine │  ← cache-first, deterministic pipeline
                          └──────────────────────┘
                                    │
   Upstream MCP cluster ◄──  Transport adapters (stdio / SSE / HTTP / WebSocket)
                                    │
                  PostgreSQL (business data)  +  Redis (cache / rate limit / stats buffer)
```

## ✨ Features

- **Multi-transport upstream connectivity** — uniformly adapts stdio, SSE, Streamable-HTTP and WebSocket upstream MCP connections.
- **Unified tool aggregation** — merges multi-upstream tools into a globally unique set with enable/disable, ordering, auto-sync and caching.
- **Alias & filter rules** — rules are bound to an upstream MCP or an API Key, supporting exact and full-match regex matching, ordered multi-rule application and per-rule enable/disable; rules stay stable as tools change.
- **Smart mode / Full mode** — smart mode exposes only a few gateway tools (`list_tools`/`search_tools`/`get_tool`/`call_tool`) for on-demand discovery to save client context window; full mode exposes all tools at once.
- **Fine-grained API Key control** — lifecycle management, plaintext returned only once at creation, per-API-Key filter rules, source IP/CIDR allowlist, per-key rate limiting.
- **Quick template market** — built-in categorized third-party MCP service templates; browse by category or keyword and prefill in one click.
- **Multi-dimension statistics & audit** — call counts and rankings by upstream / tool / API Key; audit trail for key admin operations and logins; both with retention cleanup.
- **XiaoZhi AI integration** — acts as an outbound WebSocket client to a XiaoZhi remote MCP endpoint, providing the aggregated capability to voice terminals.
- **Responsive admin UI** — built on Tailwind CSS, covering five breakpoints: mobile / tablet / PC / wide / 4K.
- **Secure by default** — upstream credentials encrypted at rest with AES-GCM, admin password salted-hashed with bcrypt, two fully isolated middleware chains (admin JWT vs. external API Key).

## 🧱 Tech Stack

| Layer | Choice |
|-------|--------|
| Backend | Go 1.25, gin, pgx/v5, go-redis/v9, robfig/cron/v3, golang-jwt/v5, golang-migrate, MCP Go SDK |
| Frontend | Vue 3 + Vite + TypeScript + Tailwind CSS (based on the TailAdmin template) + Pinia + Vue Router + ApexCharts |
| Storage | PostgreSQL (business data, time-partitioned stats table), Redis (tool cache / rate-limit counter / async stats buffer) |
| Deployment | Multi-stage Docker build (frontend → embed → distroless runtime image), GitHub Actions |

## 🚀 Quick Start (Docker)

### Prerequisites

The gateway ships as a single image but depends on external **PostgreSQL** and **Redis** at runtime. The easiest way is to bring everything up with Docker Compose.

### Option A: Docker Compose (recommended)

Create a `docker-compose.yml`:

```yaml
services:
  gateway:
    image: xylplm/mcp-proxy-gateway:latest
    ports:
      - "8080:8080"
    environment:
      MPG_PG_DSN: "postgres://mpg:mpg_password@postgres:5432/mpg?sslmode=disable"
      MPG_REDIS_ADDR: "redis:6379"
      MPG_REDIS_PASSWORD: ""
      # 32-byte key for AES-256. Generate with `openssl rand -hex 32` (64 hex chars).
      MPG_ENCRYPTION_KEY: "replace-with-your-own-32-byte-random-key"
      MPG_DATA_DIR: "/data"
    volumes:
      - mpg_data:/data
    depends_on:
      - postgres
      - redis
    restart: unless-stopped

  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: mpg
      POSTGRES_PASSWORD: mpg_password
      POSTGRES_DB: mpg
    volumes:
      - pg_data:/var/lib/postgresql/data
    restart: unless-stopped

  redis:
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

Open **http://localhost:8080** — the first launch shows the admin registration page; register and log in.

### Option B: docker run

```bash
docker run -d --name mcp-proxy-gateway \
  -p 8080:8080 \
  -e MPG_PG_DSN="postgres://user:pass@host:5432/db?sslmode=disable" \
  -e MPG_REDIS_ADDR="host:6379" \
  -e MPG_REDIS_PASSWORD="" \
  -e MPG_ENCRYPTION_KEY="$(openssl rand -hex 32)" \
  -v mpg_data:/data \
  xylplm/mcp-proxy-gateway:latest
```

### Image Tags

Published on Docker Hub: [`xylplm/mcp-proxy-gateway`](https://hub.docker.com/r/xylplm/mcp-proxy-gateway). Multi-arch images (`linux/amd64`, `linux/arm64`).

| Tag | Description |
|-----|-------------|
| `latest` | Latest released version (updated on every release) |
| `1.0.YYYYMMDDHHmm` | Immutable date-based version, e.g. `1.0.202606071302`, for easy rollback and traceability |

```bash
docker pull xylplm/mcp-proxy-gateway:latest
# or pin a specific version
docker pull xylplm/mcp-proxy-gateway:1.0.202606071302
```

> **Note on stdio upstreams**: the runtime image is distroless and contains only the gateway binary. To connect **stdio-type** upstream MCPs (launched as subprocesses, requiring node / python / uvx, etc.), use an image bundling the needed runtimes or pre-install the dependencies. SSE / Streamable-HTTP / WebSocket are remote connections with no such constraint.

## ⚙️ Environment Variables

Database/Redis connections and the encryption master key are injected via **environment variables** (not stored in YAML). Other general settings live in a YAML file under `/data` and can be edited from the admin UI's **System Settings**.

| Variable | Required | Default | Description |
|----------|:--------:|---------|-------------|
| `MPG_PG_DSN` | ✅ | — | PostgreSQL DSN, e.g. `postgres://user:pass@host:5432/db?sslmode=disable` |
| `MPG_REDIS_ADDR` | ✅ | — | Redis address, e.g. `host:6379` |
| `MPG_REDIS_PASSWORD` | ❌ | empty | Redis password; leave empty if none |
| `MPG_ENCRYPTION_KEY` | ✅ | — | AES-GCM master key; must decode to **16 / 24 / 32 bytes** (32 recommended for AES-256). Accepts raw bytes / hex / base64 |
| `MPG_DATA_DIR` | ❌ | `/data` | Data directory for the YAML config and local persistent data |

Generate an encryption key:

```bash
openssl rand -hex 32      # 64 hex chars  → decodes to 32 bytes
openssl rand -base64 32   # 44 base64 chars → decodes to 32 bytes
```

> If a required env var is missing/invalid, the YAML is malformed, or the encryption key is invalid, the process logs the error at startup and **fails fast** (refuses to start).

### Service Routes

The gateway listens on a fixed port `:8080` and exposes the following route facets:

| Route | Auth | Purpose |
|-------|------|---------|
| `/`, `/assets/*` | none | Embedded admin UI (SPA with client-side routing fallback) |
| `/api/auth/*` | none | Admin first-time init, registration, login |
| `/api/admin/*` | Admin JWT | Management APIs: upstreams / rules / API Keys / settings / stats / audit / templates |
| `/mcp/sse`, `/mcp/http`, `/mcp/ws` | API Key + rate limit + IP allowlist | External aggregated MCP capability (multi-transport) |
| `/healthz` | none | Public liveness probe (self status only, no dependency details) |
| `/api/admin/health` | Admin JWT | Detailed health (dependencies, upstreams, XiaoZhi connection states) |

## 📂 Data & Persistence

- **YAML config** and local persistent data live in the mounted `/data` directory; remounting the same volume after a container rebuild restores them.
- **Business data** (upstream MCPs, rules, API Key metadata, call statistics) is persisted to PostgreSQL; the stats table is time-partitioned and cleaned up by retention.
- Database migrations run **automatically after connecting to PostgreSQL and before serving**; migration failure aborts startup.
- The admin UI supports configuration **export / import** backup.

## 🛠 Development

### Requirements

- **Go** 1.25+
- **Node.js** 20+ (frontend build)
- **PostgreSQL** 16+ and **Redis** 7+ (local or containerized)

### Clone

```bash
git clone git@github.com:xylplm/mcp-proxy-gateway.git
cd mcp-proxy-gateway
```

### Start dependencies (PostgreSQL + Redis)

```bash
docker run -d --name mpg-pg -p 5432:5432 \
  -e POSTGRES_USER=mpg -e POSTGRES_PASSWORD=mpg_password -e POSTGRES_DB=mpg \
  postgres:16-alpine

docker run -d --name mpg-redis -p 6379:6379 redis:7-alpine
```

### Backend

```bash
# Set required env vars (PowerShell example)
$env:MPG_PG_DSN         = "postgres://mpg:mpg_password@localhost:5432/mpg?sslmode=disable"
$env:MPG_REDIS_ADDR     = "localhost:6379"
$env:MPG_ENCRYPTION_KEY = "0123456789abcdef0123456789abcdef"  # 32-byte example, do NOT use in production
$env:MPG_DATA_DIR       = "./data"

# Run the gateway (frontend can be hot-reloaded separately, see below)
go run ./cmd/gateway

# Common checks
go build ./...
go vet ./...
go test ./...        # unit / property (pgregory.net/rapid) / integration tests
gofmt -l .           # format check (should print nothing)
```

### Frontend

The frontend lives in `web/` as a standalone Vite project:

```bash
cd web
npm ci

npm run dev          # dev server (http://localhost:5173, HMR)
npm run lint         # ESLint
npm run format       # Prettier (includes Tailwind class sorting)
npm run type-check   # vue-tsc
npm run build        # production build → web/dist
```

> On a production build, `web/dist` is copied into `internal/static/dist` and embedded into the Go binary via `//go:embed dist/*`, served directly by the gateway process — no separate static server. The Docker multi-stage build does this automatically. A minimal placeholder `index.html` is kept in the repo so `go build`/`go test` always compile before the frontend is built.

### Build the image locally

```bash
docker build -t mcp-proxy-gateway:dev .
```

## 🗂 Project Structure

```
mcp-proxy-gateway/
├── cmd/gateway/            # Executable entrypoint (thin; wiring delegated to internal/app)
├── internal/
│   ├── app/                # Main assembly: component wiring, route facets, startup/shutdown
│   ├── config/             # Configuration (env vars + YAML)
│   ├── store/              # Repositories, connection pools, DB migrations
│   ├── crypto/             # Encryption service (AES-GCM)
│   ├── domain/             # Domain core: types, rule engine, unified error model
│   ├── aggregation/        # Aggregation service (deterministic pipeline + call routing)
│   ├── transport/          # Transport adapters (stdio/SSE/HTTP/WebSocket)
│   ├── manager/            # Connection manager & retry-backoff state machine
│   ├── sync/               # Tool sync service & cron scheduling
│   ├── cache/              # Tool cache (Redis + PG)
│   ├── apikey/             # API Key management, auth, ACL, rate limiting
│   ├── auth/               # Admin authentication & JWT middleware
│   ├── mcpapi/             # External MCP API service (smart/full mode, multi-transport)
│   ├── stats/ audit/       # Statistics & audit services
│   ├── template/           # Quick template market
│   ├── xiaozhi/            # XiaoZhi integration service
│   ├── health/             # Health checks & startup connectivity probing
│   ├── httpapi/            # Admin REST API routes & handlers
│   ├── backup/             # Config import/export
│   └── static/             # Embedded SPA & fallback routing
└── web/                    # Vue 3 admin UI (Vite + TS + Tailwind)
```

## 🔖 Release & CI/CD

The release pipeline is defined in `.github/workflows/release.yml`:

- **Manual trigger only** — run from **Actions → release → Run workflow**; it never publishes on push/tag.
- **Automatic date version** — each release generates a version like `1.0.<YYYYMMDDHHmm>` (Beijing time), e.g. `1.0.202606071302`; no manual tagging needed.
- **Quality gate first** — frontend ESLint plus backend `go vet` / `gofmt` check / `go build` / `go test` must all pass before building; any failure aborts the pipeline and nothing is published.
- **Multi-arch image build & push** — `buildx` builds and pushes a multi-arch image (`linux/amd64`, `linux/arm64`) to Docker Hub with two tags: the version and `latest`. Compilation happens inside the multi-stage Dockerfile (frontend build → Go embed & cross-compile → distroless runtime).
- **Automatic tag & Release** — on success, a `v<version>` git tag and a matching GitHub Release (with auto-generated notes) are created.

### Required repository Secrets

| Secret | Description |
|--------|-------------|
| `DOCKERHUB_USERNAME` | Docker Hub username |
| `DOCKERHUB_TOKEN` | Docker Hub access token (Account Settings → Security → New Access Token) |

Fixed values such as the registry and image name (`xylplm/mcp-proxy-gateway`) are hard-coded in the pipeline; nothing else to configure.

## 🤝 Contributing

Issues and pull requests are welcome. Before submitting, please ensure:

- Backend passes `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l .` (no output);
- Frontend passes `npm run lint`, `npm run type-check`, `npm run build`.

## 📄 License

Licensed under the [MIT](LICENSE) License.
