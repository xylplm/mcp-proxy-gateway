<div align="center">

# MCP Proxy Gateway

**通用的 MCP（Model Context Protocol）聚合与代理网关**

一处接入、统一聚合、按需分发 —— 把分散的 MCP 服务整合为一套可治理、可观测、可控权限的对外能力。

[![Release](https://img.shields.io/github/v/release/xylplm/mcp-proxy-gateway?sort=semver&label=release)](https://github.com/xylplm/mcp-proxy-gateway/releases)
[![Docker Image](https://img.shields.io/docker/v/xylplm/mcp-proxy-gateway?sort=semver&label=docker&logo=docker)](https://hub.docker.com/r/xylplm/mcp-proxy-gateway)
[![Docker Pulls](https://img.shields.io/docker/pulls/xylplm/mcp-proxy-gateway?logo=docker)](https://hub.docker.com/r/xylplm/mcp-proxy-gateway)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Vue](https://img.shields.io/badge/Vue-3-42b883?logo=vuedotjs&logoColor=white)](https://vuejs.org/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](#-许可证)

[English](README.md) · **中文文档**

[功能特性](#-功能特性) · [快速开始](#-快速开始docker) · [开发环境](#-开发环境) · [环境变量](#-环境变量) · [发版说明](#-发版与-cicd)

</div>

---

## 📖 项目简介

**MCP Proxy Gateway** 是一个用 Go 编写后端、Vue 3 编写管理界面的单进程 MCP 网关。它对接市面上各种连接方式（stdio / SSE / Streamable-HTTP / WebSocket）的上游 MCP 服务，将它们提供的工具统一聚合管理，并对外以多种 MCP 传输形式暴露聚合后的能力，供 Claude Code 等 AI 客户端及小智 AI 等终端调用。

整个系统打包为**单一 Docker 镜像**（前端静态资源经 `go:embed` 内嵌进二进制），无需 Nginx、无需独立静态服务器，挂载一个 `/data` 目录即可持久化配置，开箱即用。

```
                ┌─────────────────────────────────────────────┐
   管理员浏览器 ─┤  管理界面 (Vue3, 内嵌)  +  管理 REST API (JWT) │
                └─────────────────────────────────────────────┘
                                    │
   AI 客户端 ───►  对外 MCP API (API Key + 限流 + 来源白名单)
   小智 AI   ───►  /mcp/sse  /mcp/http  /mcp/ws
                                    │
                          ┌──────────────────┐
                          │   聚合服务 + 规则引擎  │   ← 缓存优先，确定性管线
                          └──────────────────┘
                                    │
   上游 MCP 集群 ◄──  传输适配层 (stdio / SSE / HTTP / WebSocket)
                                    │
                  PostgreSQL (业务数据)  +  Redis (缓存/限流/统计缓冲)
```

## ✨ 功能特性

- **多传输上游接入**：统一适配 stdio、SSE、Streamable-HTTP、WebSocket 四种上游 MCP 连接方式。
- **工具统一聚合**：多上游工具合并为全局唯一的工具集合，支持启停、排序、自动同步与缓存。
- **别名与屏蔽规则**：规则绑定在上游 MCP 或 API Key 上，支持精确匹配与正则完整匹配、多规则排序、单条启停；工具变化时规则保持稳定。
- **智能模式 / 全量模式**：智能模式仅暴露少量网关工具（`list_tools`/`search_tools`/`get_tool`/`call_tool`），按需发现，节省客户端上下文窗口；全量模式一次性暴露全部工具。
- **API Key 精细化管控**：生命周期管理、明文仅创建时返回一次、API Key 级屏蔽规则、来源 IP/CIDR 白名单、按 Key 限流。
- **快捷模板市场**：内置分类化的第三方 MCP 服务模板，按分类浏览/关键字检索，一键预填充快速接入。
- **多维统计与审计**：按上游 / 工具 / API Key 维度统计调用量与排行，关键管理操作与登录留痕审计，均支持保留期清理。
- **小智 AI 接入**：作为出站 WebSocket 客户端连接小智远程 MCP 接入点，将聚合能力提供给语音终端。
- **响应式管理界面**：基于 Tailwind CSS，覆盖手机 / 平板 / PC / 宽屏 / 4K 五档断点。
- **安全默认**：上游凭证 AES-GCM 加密存储、管理员密码 bcrypt 加盐哈希、两套鉴权中间件链（管理面 JWT 与对外面 API Key）完全隔离。

## 🧱 技术栈

| 层 | 选型 |
|----|------|
| 后端 | Go 1.25、gin、pgx/v5、go-redis/v9、robfig/cron/v3、golang-jwt/v5、golang-migrate、MCP Go SDK |
| 前端 | Vue 3 + Vite + TypeScript + Tailwind CSS（基于 TailAdmin 模板）+ Pinia + Vue Router + ApexCharts |
| 存储 | PostgreSQL（业务数据，按时间分区统计表）、Redis（工具缓存 / 限流计数 / 统计异步缓冲） |
| 部署 | 多阶段 Docker 构建（前端 → 内嵌 → distroless 运行镜像）、GitHub Actions |

## 🚀 快速开始（Docker）

### 前置依赖

网关本身打包为单一镜像，但运行时依赖外部 **PostgreSQL** 与 **Redis**。最简单的方式是用 Docker Compose 一并拉起。

### 方式一：Docker Compose（推荐）

在任意目录创建 `docker-compose.yml`：

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
      # 32 字节密钥用于 AES-256。可用 `openssl rand -hex 32` 生成（64 个十六进制字符）
      MPG_ENCRYPTION_KEY: "请替换为你自己的 32 字节随机密钥"
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

启动：

```bash
docker compose up -d
```

打开浏览器访问 **http://localhost:8080** —— 首次启动会进入管理员注册页，注册后即可登录使用。

### 方式二：docker run

```bash
docker run -d --name mcp-proxy-gateway \
  -p 8080:8080 \
  -e MPG_PG_DSN="postgres://用户:密码@主机:5432/库名?sslmode=disable" \
  -e MPG_REDIS_ADDR="主机:6379" \
  -e MPG_REDIS_PASSWORD="" \
  -e MPG_ENCRYPTION_KEY="$(openssl rand -hex 32)" \
  -v mpg_data:/data \
  xylplm/mcp-proxy-gateway:latest
```

### 镜像标签

镜像发布在 Docker Hub：[`xylplm/mcp-proxy-gateway`](https://hub.docker.com/r/xylplm/mcp-proxy-gateway)，提供多架构镜像（`linux/amd64`、`linux/arm64`）。

| 标签 | 说明 |
|------|------|
| `latest` | 最新发布版本（每次发版自动更新） |
| `1.0.YYYYMMDDHHmm` | 不可变的日期版本号，如 `1.0.202606071302`，便于回滚与追溯 |

```bash
docker pull xylplm/mcp-proxy-gateway:latest
# 或锁定具体版本
docker pull xylplm/mcp-proxy-gateway:1.0.202606071302
```

> **关于 stdio 上游的提示**：运行镜像基于 distroless，仅含网关二进制。若需接入 **stdio 类型**的上游 MCP（以子进程方式启动，依赖 node / python / uvx 等运行时），请改用自带相应运行时的镜像或预装依赖；SSE / Streamable-HTTP / WebSocket 为远程连接，无此约束。

## ⚙️ 环境变量

数据库、Redis 连接与加密主密钥通过**环境变量**注入（不写入 YAML），其余常规配置保存在 `/data` 下的 YAML 文件，并可在管理界面的「系统设置」中修改。

| 变量 | 必需 | 默认值 | 说明 |
|------|:----:|--------|------|
| `MPG_PG_DSN` | ✅ | — | PostgreSQL 连接串，如 `postgres://用户:密码@主机:5432/库名?sslmode=disable` |
| `MPG_REDIS_ADDR` | ✅ | — | Redis 地址，如 `主机:6379` |
| `MPG_REDIS_PASSWORD` | ❌ | 空 | Redis 密码，无密码留空 |
| `MPG_ENCRYPTION_KEY` | ✅ | — | AES-GCM 主密钥，解码后须为 **16 / 24 / 32 字节**（推荐 32 字节即 AES-256）。支持原始字节 / 十六进制 / base64 三种书写形式 |
| `MPG_DATA_DIR` | ❌ | `/data` | 数据目录路径，存放 YAML 配置与本地持久化数据 |

生成加密密钥的常用方式：

```bash
openssl rand -hex 32      # 64 个十六进制字符 → 解码为 32 字节
openssl rand -base64 32   # 44 个 base64 字符 → 解码为 32 字节
```

> 必需环境变量缺失或无效、YAML 非法、加密密钥无效时，进程会在启动日志中记录错误并**终止启动**（fail-fast）。

### 服务端口

网关固定监听 `:8080`，对外暴露以下路由面：

| 路由 | 鉴权 | 用途 |
|------|------|------|
| `/`、`/assets/*` | 无 | 内嵌的管理界面（SPA，支持客户端路由 fallback） |
| `/api/auth/*` | 无 | 管理员首次初始化、注册、登录 |
| `/api/admin/*` | 管理员 JWT | 上游 / 规则 / API Key / 设置 / 统计 / 审计 / 模板等管理接口 |
| `/mcp/sse`、`/mcp/http`、`/mcp/ws` | API Key + 限流 + 来源白名单 | 对外 MCP 聚合能力（多传输） |
| `/healthz` | 无 | 公开存活探针（仅返回自身存活，不泄露依赖明细） |
| `/api/admin/health` | 管理员 JWT | 详细健康（各依赖与上游 / 小智连接状态） |

## 📂 数据与持久化

- **YAML 常规配置**与本地持久化数据存于挂载的 `/data` 目录；容器重建并重新挂载同卷即可恢复。
- **业务数据**（上游 MCP、规则、API Key 元数据、调用统计）持久化到 PostgreSQL；统计表按时间分区，按保留期清理。
- 启动时在连接 PostgreSQL 成功后、对外服务前**自动执行数据库迁移**，迁移失败则终止启动。
- 管理界面支持**配置导出 / 导入**备份。

## 🛠 开发环境

### 环境要求

- **Go** 1.25+
- **Node.js** 20+（前端构建）
- **PostgreSQL** 16+ 与 **Redis** 7+（本地或容器）

### 克隆仓库

```bash
git clone git@github.com:xylplm/mcp-proxy-gateway.git
cd mcp-proxy-gateway
```

### 启动依赖（PostgreSQL + Redis）

可用 Docker 快速拉起本地依赖：

```bash
docker run -d --name mpg-pg -p 5432:5432 \
  -e POSTGRES_USER=mpg -e POSTGRES_PASSWORD=mpg_password -e POSTGRES_DB=mpg \
  postgres:16-alpine

docker run -d --name mpg-redis -p 6379:6379 redis:7-alpine
```

### 后端开发

```bash
# 配置必需环境变量（PowerShell 示例）
$env:MPG_PG_DSN         = "postgres://mpg:mpg_password@localhost:5432/mpg?sslmode=disable"
$env:MPG_REDIS_ADDR     = "localhost:6379"
$env:MPG_ENCRYPTION_KEY = "0123456789abcdef0123456789abcdef"  # 32 字节示例，请勿用于生产
$env:MPG_DATA_DIR       = "./data"

# 运行网关（开发态前端可单独热更，见下）
go run ./cmd/gateway

# 常用校验
go build ./...
go vet ./...
go test ./...        # 含单元 / 属性（pgregory.net/rapid）/ 集成测试
gofmt -l .           # 格式检查（应无输出）
```

### 前端开发

前端位于 `web/`，独立的 Vite 工程：

```bash
cd web
npm ci

npm run dev          # 启动开发服务器（默认 http://localhost:5173，热更新）
npm run lint         # ESLint 校验
npm run format       # Prettier 格式化（含 Tailwind 类名排序）
npm run type-check   # vue-tsc 类型检查
npm run build        # 生产构建，产出 web/dist
```

> 生产构建时，`web/dist` 会被拷入 `internal/static/dist` 并通过 `//go:embed dist/*` 内嵌进 Go 二进制，由网关进程直接提供，无需独立的静态服务器；Docker 多阶段构建已自动完成这一步。仓库内保留了一个最小占位 `index.html`，保证前端未构建时 `go build` / `go test` 也能编译通过。

### 本地构建镜像

```bash
docker build -t mcp-proxy-gateway:dev .
```

## 🗂 项目结构

```
mcp-proxy-gateway/
├── cmd/gateway/            # 可执行入口（精简，装配委托给 internal/app）
├── internal/
│   ├── app/                # 主程序装配：组件接线、路由分面、启停
│   ├── config/             # 配置管理（环境变量 + YAML）
│   ├── store/              # 仓储层、连接池、数据库迁移
│   ├── crypto/             # 加密服务（AES-GCM）
│   ├── domain/             # 领域核心：类型、规则引擎、统一错误模型
│   ├── aggregation/        # 聚合服务（确定性管线 + 调用路由）
│   ├── transport/          # 传输适配层（stdio/SSE/HTTP/WebSocket）
│   ├── manager/            # 连接管理器与重试退避状态机
│   ├── sync/               # 工具同步服务与 cron 调度
│   ├── cache/              # 工具缓存（Redis + PG）
│   ├── apikey/             # API Key 管理、鉴权、ACL、限流
│   ├── auth/               # 管理员认证与 JWT 中间件
│   ├── mcpapi/             # 对外 MCP API 服务（智能/全量模式、多传输）
│   ├── stats/ audit/       # 统计与审计服务
│   ├── template/           # 快捷模板市场
│   ├── xiaozhi/            # 小智接入服务
│   ├── health/             # 健康检查与启动连通性探测
│   ├── httpapi/            # 管理 REST API 路由与处理器
│   ├── backup/             # 配置导入导出
│   └── static/             # 内嵌 SPA 与 fallback 路由
└── web/                    # Vue 3 管理界面（Vite + TS + Tailwind）
```

## 🔖 发版与 CI/CD

发布流水线定义在 `.github/workflows/release.yml`，特点如下：

- **仅手动触发**：在 GitHub 仓库 **Actions → release → Run workflow** 手动发起，不会因 push/tag 自动发版。
- **自动日期版本号**：每次发版自动生成 `1.0.<YYYYMMDDHHmm>`（北京时间）形式的版本号，例如 `1.0.202606071302`，无需手动打 tag。
- **质量门禁前置**：先跑前端 ESLint、后端 `go vet` / `gofmt` 检查 / `go build` / `go test`，全部通过才进入发布；任一失败即中止，不会发布镜像。
- **多架构镜像构建并推送**：用 `buildx` 一步构建并推送多架构镜像（`linux/amd64`、`linux/arm64`）到 Docker Hub，打两个标签——具体版本号与 `latest`。编译在多阶段 Dockerfile 内完成（前端构建 → Go 内嵌前端并交叉编译 → distroless 运行镜像）。
- **自动打 tag 与 Release**：发布成功后自动创建 `v<版本号>` git tag 与对应的 GitHub Release（含自动生成的发布说明）。

### 需要配置的仓库 Secrets

| Secret | 说明 |
|--------|------|
| `DOCKERHUB_USERNAME` | Docker Hub 用户名 |
| `DOCKERHUB_TOKEN` | Docker Hub 访问令牌（Account Settings → Security → New Access Token） |

镜像仓库、镜像名（`xylplm/mcp-proxy-gateway`）等固定值已写死在流水线中，无需额外配置。

## 🤝 贡献

欢迎提交 Issue 与 Pull Request。提交前请确保：

- 后端通过 `go build ./...`、`go vet ./...`、`go test ./...`、`gofmt -l .`（无输出）；
- 前端通过 `npm run lint`、`npm run type-check`、`npm run build`。

## 📄 许可证

本项目采用 [MIT](LICENSE) 许可证。
