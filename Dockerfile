# 默认精简镜像（slim）：仅网关静态二进制 + CA/时区。
# 不含 Node / Python / uv；stdio 工具请走管理台「运行环境」预置安装或数据卷 $MPG_DATA_DIR/runtime。
#
# 标签约定（CI）：
#   xylplm/mcp-proxy-gateway:latest
#   xylplm/mcp-proxy-gateway:<version>
#
# 完整版见 Dockerfile.full（:full / :<version>-full）。
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
ENV MPG_PG_DSN="" \
    MPG_REDIS_ADDR="" \
    MPG_DATA_DIR="/data"

VOLUME ["/data"]

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
CMD ["/mpg"]
