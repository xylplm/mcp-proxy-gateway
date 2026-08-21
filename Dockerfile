# 精简镜像（slim）：只有网关静态二进制，不含任何本地运行时。
#
# 面向纯网关用户：只接远程 SSE / Streamable-HTTP / WebSocket / OpenAPI 上游。
# 本地 stdio 相关功能（运行环境、脚本中心、stdio 上游、依赖管理）在本镜像中默认关闭，
# 管理台会隐藏对应入口 —— 由 MPG_IMAGE_FLAVOR=slim 决定，不是用户可切换的开关。
# 需要本地 stdio 请使用完整镜像（Dockerfile.full → :latest / :full）。
#
# 基于 Alpine 取最小体积：网关以 CGO_ENABLED=0 静态编译，不依赖 glibc。
# （完整镜像必须用 glibc，因为 Node 官方 tarball 是 glibc 构建；精简镜像没有运行时，
#   所以没有这个约束。）
#
# 标签约定（CI）：
#   xylplm/mcp-proxy-gateway:slim
#   xylplm/mcp-proxy-gateway:<version>-slim
#
# 构建参数：
#   TARGETARCH — buildx 注入的目标架构（amd64 / arm64）
# 注意：Docker build context 为仓库根目录，二进制位于 bin/。

FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata

ARG TARGETARCH
COPY bin/mpg-linux-${TARGETARCH} /mpg
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /mpg /entrypoint.sh

# --- 运行期环境变量 ---
# 必需：
#   MPG_PG_DSN          PostgreSQL 连接串
#   MPG_REDIS_ADDR      Redis 地址
# 可选：
#   MPG_REDIS_PASSWORD  Redis 密码
#   MPG_DATA_DIR        数据目录（默认 /data）
#   MPG_RUNTIME_DIR     本地 stdio 工具卷（默认 $MPG_DATA_DIR/runtime）
# MPG_IMAGE_FLAVOR 决定管理台是否暴露本地 stdio 相关功能，非用户可切换开关。
ENV MPG_PG_DSN="" \
    MPG_REDIS_ADDR="" \
    MPG_DATA_DIR="/data" \
    MPG_IMAGE_FLAVOR="slim"

VOLUME ["/data"]

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
CMD ["/mpg"]
