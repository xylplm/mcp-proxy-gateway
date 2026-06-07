# syntax=docker/dockerfile:1
#
# 多阶段构建：前端构建 → Go 内嵌前端并编译 → distroless 运行镜像。
#
# 交付物是一个自包含的静态二进制：前端 SPA 产物经 //go:embed 内嵌进二进制，
# 运行镜像无需任何外部静态资源目录，distroless 基础镜像无 shell / 包管理器，
# 体积小、攻击面最小、启动快。支持多架构（由 buildx 经 TARGETOS/TARGETARCH 驱动）。

# =============================================================================
# 阶段一：构建前端（产出 web/dist）
# =============================================================================
FROM node:20-alpine AS web
WORKDIR /web

# 先仅复制依赖清单以利用构建缓存（package.json 不变时跳过重复 npm ci）。
COPY web/package.json web/package-lock.json ./
RUN npm ci

# 复制前端源码并构建（vite 默认输出至 /web/dist）。
COPY web/ ./
RUN npm run build

# =============================================================================
# 阶段二：编译 Go（内嵌前端 dist 后静态编译，按目标架构交叉编译）
# =============================================================================
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src

# buildx 注入的目标平台信息，用于交叉编译出对应架构的二进制。
ARG TARGETOS
ARG TARGETARCH
# 由 CI 注入的版本号（如 1.0.202606071302），写入二进制版本变量。
ARG VERSION=dev

# 先下载依赖以利用缓存（go.mod/go.sum 不变时跳过重复下载）。
COPY go.mod go.sum ./
RUN go mod download

# 复制其余源码。
COPY . .

# 用阶段一构建好的前端产物覆盖 embed 占位目录，供 //go:embed dist/* 内嵌。
COPY --from=web /web/dist ./internal/static/dist

# 静态编译：禁用 CGO，按目标架构交叉编译，产出无外部依赖、可在 distroless 运行的二进制。
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X 'main.version=${VERSION}'" \
    -o /out/mpg ./cmd/gateway

# =============================================================================
# 阶段三：运行镜像（distroless static，非 root）
# 仅含二进制本身，声明 /data 卷与运行期环境变量。
# =============================================================================
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=build /out/mpg /mpg

# --- 运行期环境变量声明 ---
# 必需（无有效默认值，必须在运行时注入；缺失或无效将导致启动失败）：
#   MPG_PG_DSN          PostgreSQL 连接串，如 postgres://user:pass@host:5432/db?sslmode=disable
#   MPG_REDIS_ADDR      Redis 地址，如 host:6379
#   MPG_ENCRYPTION_KEY  AES-GCM 主密钥，解码后须为 16/24/32 字节（推荐 32 字节）
# 可选：
#   MPG_REDIS_PASSWORD  Redis 密码（无密码留空）
#   MPG_DATA_DIR        数据目录（默认 /data）
ENV MPG_PG_DSN="" \
    MPG_REDIS_ADDR="" \
    MPG_REDIS_PASSWORD="" \
    MPG_ENCRYPTION_KEY="" \
    MPG_DATA_DIR="/data"

# 持久化数据目录（YAML 配置与本地持久化数据）。
VOLUME ["/data"]

# 网关 HTTP/WS 监听端口（固定 :8080）。
EXPOSE 8080

ENTRYPOINT ["/mpg"]
