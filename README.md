<div align="center">

<img src="web/public/images/logo/auth-logo.svg" alt="MCP Proxy Gateway" width="280" />

# MCP Proxy Gateway

**面向 AI 工程化落地的 MCP 聚合、治理与观测网关**

一次接入多类 MCP 服务，统一聚合工具能力，按 API Key 精细分发，并用管理台完成连接、规则、安全、审计、统计与运维闭环。

[![Release](https://img.shields.io/github/v/release/xylplm/mcp-proxy-gateway?sort=semver&label=release)](https://github.com/xylplm/mcp-proxy-gateway/releases)
[![Docker Image](https://img.shields.io/docker/v/xylplm/mcp-proxy-gateway?sort=semver&label=docker&logo=docker)](https://hub.docker.com/r/xylplm/mcp-proxy-gateway)
[![Docker Pulls](https://img.shields.io/docker/pulls/xylplm/mcp-proxy-gateway?logo=docker)](https://hub.docker.com/r/xylplm/mcp-proxy-gateway)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Vue](https://img.shields.io/badge/Vue-3-42b883?logo=vuedotjs&logoColor=white)](https://vuejs.org/)
[![License](https://img.shields.io/badge/license-AGPL--3.0%20%2B%20Commercial-blue.svg)](#许可证)

**中文** · [English](README.en.md)

[核心能力](#核心能力) · [快速开始](#快速开始) · [服务入口](#服务入口) · [配置与持久化](#配置与持久化) · [开发](#开发) · [发布](#发布)

</div>

---

## 项目定位

MCP Proxy Gateway 是一个自托管的 MCP 能力网关。它负责把分散在不同团队、不同运行时、不同传输协议里的 MCP Server 接入到同一个控制平面，再以稳定、可审计、可限流、可按租户裁剪的方式暴露给 Claude Code、Cursor、Cherry Studio、小智 AI 或其他兼容 MCP 的客户端。

项目后端使用 Go，管理台使用 Vue 3 + Vite + Tailwind CSS。前端构建产物通过 `go:embed` 内嵌进二进制，发布时随网关一起打包为 Docker 镜像；运行时依赖 PostgreSQL 保存业务数据，依赖 Redis 承载缓存、限流与统计缓冲。

```text
┌────────────────────────────────────────────────────────────────────────────┐
│                              管理控制平面                                  │
│  Vue 3 管理台 + REST API + JWT 登录 + 设置中心 + 备份 + 诊断 + 系统日志       │
└──────────────────────────────────┬─────────────────────────────────────────┘
                                   │
┌──────────────────────────────────▼─────────────────────────────────────────┐
│                              MCP 服务平面                                  │
│  /mcp/sse  /mcp/http  /mcp/ws  /mcp/smart/*                                 │
│  API Key 鉴权 · IP/CIDR 白名单 · 限流 · 安全中心 · 智能/全量模式              │
└──────────────────────────────────┬─────────────────────────────────────────┘
                                   │
┌──────────────────────────────────▼─────────────────────────────────────────┐
│                         聚合管线与治理引擎                                  │
│  工具同步 · 缓存优先 · 别名规则 · 屏蔽规则 · 工具策略 · 智能均衡/优先路由      │
└──────────────────────────────────┬─────────────────────────────────────────┘
                                   │
┌──────────────────────────────────▼─────────────────────────────────────────┐
│                              上游 MCP 集群                                 │
│  stdio · SSE · Streamable HTTP · WebSocket · OpenAPI/REST 虚拟 MCP 工具      │
└────────────────────────────────────────────────────────────────────────────┘
                                   │
                 PostgreSQL 业务数据 + Redis 缓存/限流/统计缓冲
```

## 核心能力

### 多协议接入与统一暴露

- 接入 `stdio`、`sse`、`streamable-http`、`websocket` 上游 MCP 服务。
- 支持把 OpenAPI/REST 服务虚拟成 MCP 工具，便于把传统 HTTP API 纳入 AI 工具体系。
- 对外同时提供 SSE、Streamable HTTP、WebSocket 三种 MCP 传输入口。
- 额外提供 `/mcp/smart/*` 智能模式端点，用少量网关工具完成按需发现与调用，降低客户端上下文占用。

### 工具聚合、路由与治理

- 多上游工具统一聚合为全局工具目录，支持排序、启停、手动刷新、自动同步与变化摘要。
- 同名工具多来源时支持智能均衡与优先级填充策略，并自动避开不可用或超额来源。
- 上游级别支持调用额度配置：每秒、每分钟、每小时、每天、每周、每月及指定时区。
- 别名规则可重命名工具或改写描述，屏蔽规则可按全局、上游或 API Key 视角裁剪工具集合。
- 工具策略支持按工具名匹配，覆盖路由策略、启用短 TTL 成功结果缓存、标注风险标签。
- 管理台内置工具目录、来源详情、Schema 冲突提示、风险提示与 Playground 试调用。

### API Key、租户隔离与访问控制

- API Key 生命周期管理：创建、启停、删除、明文创建后一次性展示。
- 支持 `X-API-Key`、`Authorization: Bearer <key>`、`api_key` 查询参数三种携带方式。
- 每个 API Key 可配置独立工具屏蔽规则、来源 IP/CIDR 白名单与限流策略。
- 管理员 JWT 鉴权链与对外 MCP API Key 鉴权链完全隔离，降低误暴露风险。

### 安全中心、审计与可观测性

- 安全中心支持 `off`、`monitor`、`enforce` 三种模式，记录鉴权失败、ACL 拒绝与自动封禁事件。
- 支持可信代理 CIDR、自动封禁豁免 CIDR、封禁升级窗口与手动解封。
- 调用统计覆盖总览、趋势、上游维度、工具排行、API Key 使用画像、错误排行与健康概览。
- 调用记录可分页查询、查看详情、导出与清理，便于排障和成本分析。
- 审计日志记录登录、资源增删改与拒绝访问事件，支持查询与导出。
- 系统日志在管理台可检索、导出、清空，诊断包可一键导出。

### 管理台与运维闭环

- 响应式 Vue 3 管理台，覆盖仪表盘、上游管理、API Key、规则、工具目录、调用记录、统计、审计、安全中心、系统日志、设置、关于等页面。
- 内置模板市场，按分类或关键字选择常见第三方 MCP 服务模板，一键预填上游配置。
- 支持 MCP JSON 导入/导出上游配置，支持全局配置备份导出、预览与导入。
- 支持独立对外 MCP 监听端口，可将管理端口留在内网，把服务端口单独暴露到公网。
- 支持小智 AI 接入，作为出站 WebSocket 客户端把聚合后的 MCP 能力提供给语音终端。

## 技术栈

| 层级 | 选型 |
| --- | --- |
| 后端 | Go 1.25、Gin、GORM、pgx/PostgreSQL、go-redis/v9、robfig/cron/v3、golang-jwt/v5、MCP Go SDK |
| 前端 | Vue 3、Vite、TypeScript、Tailwind CSS、Pinia、Vue Router、ApexCharts |
| 存储 | PostgreSQL 业务数据与分区统计表；Redis 工具缓存、限流计数、统计缓冲 |
| 部署 | GitHub Actions 预编译 linux/amd64 与 linux/arm64 二进制，Debian bookworm-slim/glibc 运行镜像，多架构 Docker Hub 发布 |

## 快速开始

网关镜像本身包含后端与管理台，但运行时需要 PostgreSQL 与 Redis。推荐使用 Docker Compose 一次拉起完整环境。

### Docker Compose

创建 `docker-compose.yml`：

```yaml
services:
  gateway:
    container_name: mcp-proxy-gateway
    image: xylplm/mcp-proxy-gateway:latest
    ports:
      - "8080:8080"
      # 如在系统设置中启用独立对外 MCP 端口，例如 :8081，请额外暴露该端口。
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

启动：

```bash
docker compose up -d
```

打开 `http://localhost:8080`。首次启动会进入管理员注册页，完成注册后即可进入管理台。

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

如果启用了独立 MCP 服务端口，例如 `:8081`，运行容器时还需要增加 `-p 8081:8081`。

### 镜像标签

镜像发布在 Docker Hub：[`xylplm/mcp-proxy-gateway`](https://hub.docker.com/r/xylplm/mcp-proxy-gateway)，支持 `linux/amd64` 与 `linux/arm64`。

| 标签 | 说明 |
| --- | --- |
| `latest` / `1.0.YYYYMMDDHHmm` | **精简（默认）**：无 Node/Python；可用管理台预置安装或数据卷放入工具 |
| `full` / `1.0.YYYYMMDDHHmm-full` | **完整**：内置 Node.js 24 LTS + npm/npx + Python3，本地 stdio 开箱即用 |

```bash
# 精简版（默认，体积小；远程 MCP / 自行装运行时）
docker pull xylplm/mcp-proxy-gateway:latest
docker pull xylplm/mcp-proxy-gateway:1.0.202606071302

# 完整版（内置 Node.js 24 LTS + Python3，本地 stdio 开箱即用）
docker pull xylplm/mcp-proxy-gateway:full
docker pull xylplm/mcp-proxy-gateway:1.0.202606071302-full
```

> `stdio` 上游会在网关进程旁启动本地子进程：
> - **默认 `:latest` 精简镜像**不含 Node / Python / uv；可通过管理台「运行环境」预置安装 Node/uv，或将工具放入数据卷 `$MPG_DATA_DIR/runtime`。
> - **`:full` 完整镜像**已内置 Node.js 24 LTS（含 npm/npx）与 Python3，适合模板与本地 MCP 拿来即用；仍可预置安装 uv。内置 Node 与受管预置安装使用同一份官方 tarball（同版本同校验和），不会出现两份版本不一致的 node。
> - 受管 Node、uv、npm、pip 的安装、卸载和依赖管理仅支持官方 `linux/amd64`、`linux/arm64` Docker/OCI 镜像。原生 Windows/macOS 与 Windows 容器不支持；Docker Desktop 仅在 Linux containers 引擎运行官方镜像时支持。
> 详见 [docs/runtime.md](docs/runtime.md)。远程 SSE、Streamable HTTP、WebSocket 与 OpenAPI 上游不依赖本地工具。

## 服务入口

默认情况下，网关在 `:8080` 同时提供管理台、管理 API、健康探针与对外 MCP API。你也可以在管理台「系统设置」中配置独立 `public_mcp_addr`，将 MCP 服务面迁移到单独端口。

| 路由 | 鉴权 | 用途 |
| --- | --- | --- |
| `/`、`/assets/*` | 无 | 内嵌管理台 SPA |
| `/api/auth/*` | 无 | 初始化状态、管理员注册、登录 |
| `/api/admin/*` | 管理员 JWT | 上游、规则、API Key、工具、统计、审计、安全、设置、备份、模板等管理 API |
| `/mcp/sse` | API Key + 安全中心 + ACL + 限流 | 全量模式 SSE MCP 入口 |
| `/mcp/http` | API Key + 安全中心 + ACL + 限流 | 全量模式 Streamable HTTP MCP 入口 |
| `/mcp/ws` | API Key + 安全中心 + ACL + 限流 | 全量模式 WebSocket MCP 入口 |
| `/mcp/smart/sse` | API Key + 安全中心 + ACL + 限流 | 智能模式 SSE MCP 入口 |
| `/mcp/smart/http` | API Key + 安全中心 + ACL + 限流 | 智能模式 Streamable HTTP MCP 入口 |
| `/mcp/smart/ws` | API Key + 安全中心 + ACL + 限流 | 智能模式 WebSocket MCP 入口 |
| `/healthz` | 无 | 存活探针，仅返回进程存活 |
| `/api/admin/health` | 管理员 JWT | 详细健康状态，包含依赖、上游、小智连接等信息 |

API Key 可通过以下任一方式传入：

```http
X-API-Key: <your-api-key>
Authorization: Bearer <your-api-key>
```

也支持查询参数：`?api_key=<your-api-key>`。

## 配置与持久化

数据库、Redis 与数据目录通过环境变量注入：

| 变量 | 必需 | 默认值 | 说明 |
| --- | :---: | --- | --- |
| `MPG_PG_DSN` | 是 | 无 | PostgreSQL 连接串，例如 `postgres://user:pass@host:5432/db?sslmode=disable` |
| `MPG_REDIS_ADDR` | 是 | 无 | Redis 地址，例如 `host:6379` |
| `MPG_REDIS_PASSWORD` | 否 | 空 | Redis 密码 |
| `MPG_DATA_DIR` | 否 | `/data` | YAML 配置与本地持久化目录 |
| `MPG_RUNTIME_DIR` | 否 | `{MPG_DATA_DIR}/runtime` | 本地 stdio 工具卷目录（`bin` / `node` / `npm` / `python` / `uv` / `cache` 等） |

其余配置保存在 `MPG_DATA_DIR` 下的 YAML 文件中，并可通过管理台修改：

- `server`：管理端口、独立 MCP 端口、是否在管理端口暴露 MCP、日志级别。
- `auth`：管理员会话超时时间，JWT 密钥首次启动自动生成。
- `sync`：工具自动同步 cron 与超时。
- `connection`：连接超时、重试退避、失败阈值。
- `aggregation`：上游调用超时、同名工具路由策略。
- `mcp_api`：智能发现返回数量、POST 请求体大小限制。
- `statistics` 与 `audit`：分页默认值与保留天数。
- `security`：监控/强制模式、失败窗口、自动封禁、可信代理、豁免 CIDR。
- `xiaozhi`：小智接入开关、WebSocket 地址与智能/全量模式。

持久化数据分布：

- `/data`：YAML 配置、本地持久化文件。
- PostgreSQL：上游配置、工具缓存索引、规则、API Key 元数据、ACL、限流配置、调用统计、审计、安全事件等业务数据。
- Redis：工具缓存、API Key 限流计数、统计写入缓冲和运行期状态辅助数据。

启动时会先校验必需环境变量、读取 YAML、生成缺失的 JWT 密钥、连接 PostgreSQL/Redis，并在对外服务前初始化数据库 schema。关键依赖失败会直接终止启动。

## 使用建议

1. 先在「上游管理」接入一个或多个 MCP Server，可使用模板市场快速预填。
2. 在上游详情中测试连接并刷新工具列表，确认工具目录可见。
3. 在「规则管理」配置别名、屏蔽和工具策略，把工具名整理成面向客户端的稳定接口。
4. 创建 API Key，为不同客户端配置独立可见工具、IP/CIDR 白名单与限流。
5. 客户端优先接入 `/mcp/smart/http` 或 `/mcp/smart/sse`，上下文充足时再使用全量入口。
6. 通过「调用记录」「统计分析」「审计日志」「安全中心」观察调用质量与访问风险。

## 开发

### 环境要求

- Go 1.25+
- Node.js 24+，项目当前前端工具链在 CI 中使用 Node 24
- PostgreSQL 16+
- Redis 7+

### 启动本地依赖

```bash
docker run -d --name mpg-pg -p 5432:5432 \
  -e POSTGRES_USER=mpg \
  -e POSTGRES_PASSWORD=mpg_password \
  -e POSTGRES_DB=mpg \
  postgres:16-alpine

docker run -d --name mpg-redis -p 6379:6379 redis:7-alpine
```

### 后端

```powershell
$env:MPG_PG_DSN = "postgres://mpg:mpg_password@localhost:5432/mpg?sslmode=disable"
$env:MPG_REDIS_ADDR = "localhost:6379"
$env:MPG_DATA_DIR = "./data"

go run ./cmd/gateway
```

常用检查：

```bash
go test ./...
go vet ./...
gofmt -l .
```

### 前端

```bash
cd web
npm ci
npm run dev
```

常用检查：

```bash
npm run build
npm run lint
npm run format:check
npm run test:unit
```

生产构建时，CI 会先执行 `web` 构建，再把 `web/dist` 复制到 `internal/static/dist`，最后编译 Go 二进制完成静态资源内嵌。

## 项目结构

```text
mcp-proxy-gateway/
├── cmd/gateway/            # 网关启动入口
├── internal/
│   ├── app/                # 应用装配、路由分面、启动与关闭
│   ├── aggregation/        # 工具聚合、调用路由、配额与缓存策略
│   ├── apikey/             # API Key、ACL、限流与服务面鉴权
│   ├── audit/              # 审计记录与查询
│   ├── auth/               # 管理员注册、登录、JWT 中间件
│   ├── backup/             # 配置备份导入导出
│   ├── cache/              # 工具缓存
│   ├── config/             # 环境变量与 YAML 配置
│   ├── domain/             # 领域模型、规则引擎与错误模型
│   ├── health/             # 存活探针与详细健康报告
│   ├── httpapi/            # 管理 REST API 组合层
│   ├── manager/            # 上游连接生命周期与重试退避
│   ├── mcpapi/             # 对外 MCP 多传输服务
│   ├── security/           # 安全中心、失败监控与自动封禁
│   ├── stats/              # 调用统计、趋势、记录查询
│   ├── store/              # PostgreSQL 仓储与 schema 初始化
│   ├── sync/               # 工具同步与 cron 调度
│   ├── syslog/             # 运行日志缓冲与导出
│   ├── template/           # 模板市场
│   ├── transport/          # 上游传输适配
│   └── xiaozhi/            # 小智 AI 接入
├── web/                    # Vue 3 管理台
├── Dockerfile              # 精简 Debian bookworm-slim/glibc 镜像（默认 :latest）
├── Dockerfile.full         # 完整镜像（:full，内置 Node/Python）
└── .github/workflows/      # 发布流水线
```

## 发布

发布流水线位于 `.github/workflows/release.yml`，只支持手动触发：

- `quality`：安装 Node 24 与 Go，执行前端依赖安装、ESLint、`go vet`、`gofmt` 提示、`go test ./...`。
- `version`：按北京时间生成 `1.0.<YYYYMMDDHHmm>` 版本号。
- `build-frontend`：写入前端版本号，执行 `npm run build`，上传 `web/dist`。
- `build-backend`：矩阵编译 `linux/amd64` 与 `linux/arm64` 静态二进制，并内嵌前端产物。
- `docker`：精简多架构镜像（`:latest` / `:<version>`）。
- `docker-full`：完整多架构镜像（`:full` / `:<version>-full`，内置 Node/Python）。
- `release`：创建 `v<version>` Git tag 与 GitHub Release。

仓库需要配置以下 Secrets：

| Secret | 说明 |
| --- | --- |
| `DOCKERHUB_USERNAME` | Docker Hub 用户名 |
| `DOCKERHUB_TOKEN` | Docker Hub Access Token |

## 许可证

本项目采用 [GNU Affero General Public License v3.0](LICENSE)（AGPLv3）与商业授权双授权模式。

- 在遵守 AGPLv3 条款的前提下，你可以免费使用、修改和分发本项目。
- 如需在闭源商业产品中集成、以 SaaS/托管服务形式提供，或不希望履行 AGPLv3 的源代码开放义务，请通过 GitHub 仓库联系作者获取单独商业授权。
