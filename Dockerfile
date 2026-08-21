# 完整镜像（默认）：网关 + 本地 stdio 运行时全部内置。
#
# 内置 Node.js 24 LTS（node/npm/npx）、Python 3.12（python/python3/pip）、
# uv/uvx、bubblewrap。stdio 上游、脚本中心、npm/pip 依赖管理开箱即用。
#
# 设计取舍：运行时由镜像提供，不在运行期下载安装。
#   - 版本由镜像 tag 决定，可复现；不再是数据卷里的可变状态。
#   - 免去下载、校验、解压、原子替换、回滚、安装状态等一整套运行期机制。
#   - 数据卷只存"依赖"（runtime/npm、runtime/pip），容器更新不丢。
# 纯网关用户请用精简镜像（Dockerfile.slim → :slim），体积小且自动隐藏 stdio 相关功能。
#
# 基础镜像用官方 python:3.12-slim 而非 debian + apt python3：
#   - Python 版本由我们决定，不跟随发行版（bookworm 只有已 EOL 的 3.11）。
#   - 仍是 Debian glibc 底子，Node 官方 tarball 可直接用。
#
# 标签约定（CI）：
#   xylplm/mcp-proxy-gateway:latest / :full
#   xylplm/mcp-proxy-gateway:<version> / :<version>-full
#
# 本地构建示例（二进制需先编译到 bin/mpg-linux-<arch>）：
#   docker build --build-arg TARGETARCH=amd64 -t xylplm/mcp-proxy-gateway:latest .

FROM python:3.12-slim

ARG TARGETARCH

# Node 官方 tarball 固定版本 + 校验和。这里是 Node 版本的唯一来源。
ARG NODE_VERSION=24.19.0
ARG NODE_SHA256_AMD64=f625d97cd707df4ff96254916fbc5ff014f09c09effe5a1e0ca8f6d41a8789d4
ARG NODE_SHA256_ARM64=d28c8a5bf0a808f0ed434a1dce8c54ae98f0371c0bd86ac58abc613f73e6643f

# uv/uvx 取自官方 distroless 镜像：多架构自动匹配，无需下载与校验和维护。
COPY --from=ghcr.io/astral-sh/uv:0.6.14 /uv /uvx /usr/local/bin/

# PATH 必须在安装 Node 的 RUN 之前声明。
# npm / npx 是以 #!/usr/bin/env node 为 shebang 的脚本，解析自己的解释器要靠 PATH；
# 若 PATH 放到 RUN 之后，构建期校验会以 env: 'node': No such file or directory 失败，
# 运行期同理。放在这里让构建校验与运行期走完全相同的解析路径。
ENV PATH="/opt/node/bin:${PATH}"

# bubblewrap：严格档 stdio 的文件 bind 隔离与网络 deny 命名空间断网；
# 实际可用性仍取决于容器权限，存在二进制不代表隔离一定能启动。
#
# Node 装到 /opt/node 而不是合并进 /usr/local：与 Python 各自独立，
# 升级或排查时边界清晰，也不会覆盖 Python 的同名目录。
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        tzdata \
        bubblewrap; \
    apt-get install -y --no-install-recommends curl; \
    case "${TARGETARCH}" in \
        amd64) nodeArch=x64;   nodeSha="${NODE_SHA256_AMD64}" ;; \
        arm64) nodeArch=arm64; nodeSha="${NODE_SHA256_ARM64}" ;; \
        *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL --retry 3 --retry-delay 2 -o /tmp/node.tar.gz \
        "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${nodeArch}.tar.gz"; \
    echo "${nodeSha}  /tmp/node.tar.gz" | sha256sum -c -; \
    mkdir -p /opt/node; \
    tar -xzf /tmp/node.tar.gz -C /opt/node --strip-components=1 --no-same-owner \
        --exclude='*/CHANGELOG.md' \
        --exclude='*/README.md' \
        --exclude='*/LICENSE'; \
    rm -f /tmp/node.tar.gz; \
    apt-get purge -y --auto-remove curl; \
    rm -rf /var/lib/apt/lists/*; \
    node -v; \
    npm -v; \
    npx --version; \
    python3 --version; \
    python --version; \
    uv --version; \
    uvx --version; \
    bwrap --version

COPY bin/mpg-linux-${TARGETARCH} /mpg
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /mpg /entrypoint.sh

# MPG_IMAGE_FLAVOR=full 让管理台暴露全部本地 stdio 功能。
# UV_NO_MANAGED_PYTHON / UV_PYTHON_DOWNLOADS：Python 由镜像提供，
# 禁止 uv 自行下载解释器（否则 uv pip --target 找不到解释器时会静默拉数十 MB）。
ENV MPG_PG_DSN="" \
    MPG_REDIS_ADDR="" \
    MPG_DATA_DIR="/data" \
    MPG_IMAGE_FLAVOR="full" \
    UV_NO_MANAGED_PYTHON=1 \
    UV_PYTHON_DOWNLOADS=never

VOLUME ["/data"]

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
CMD ["/mpg"]
