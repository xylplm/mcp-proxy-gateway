# 极简运行镜像：仅 COPY 已编译好的静态二进制到 Alpine。
# 编译在 GitHub Actions 中完成，本文件不做任何构建。
#
# 构建参数：
#   TARGETARCH — buildx 多架构构建时自动注入的目标架构（amd64 / arm64），
#                 用于自动选择对应架构的二进制文件。
#
# 注意：Docker build context 为仓库根目录（.），二进制位于 bin/ 子目录。

FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata

ARG TARGETARCH
COPY bin/mpg-linux-${TARGETARCH} /mpg
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /mpg /entrypoint.sh

# --- 运行期环境变量 ---
# 必需（无有效默认值，必须在运行时通过 -e 注入；缺失或无效将导致启动失败）：
#   MPG_PG_DSN          PostgreSQL 连接串，如 postgres://user:pass@host:5432/db?sslmode=disable
#   MPG_REDIS_ADDR      Redis 地址，如 host:6379
#   MPG_ENCRYPTION_KEY  AES-GCM 主密钥，解码后须为 16/24/32 字节（推荐 32 字节）
# 可选（敏感字段通过 -e 注入，不在此声明）：
#   MPG_REDIS_PASSWORD  Redis 密码
#   MPG_DATA_DIR        数据目录（默认 /data）
ENV MPG_PG_DSN="" \
    MPG_REDIS_ADDR="" \
    MPG_DATA_DIR="/data"

# 持久化数据目录（YAML 配置与本地持久化数据）。
VOLUME ["/data"]

# 网关 HTTP/WS 监听端口（固定 :8080）。
EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
CMD ["/mpg"]
