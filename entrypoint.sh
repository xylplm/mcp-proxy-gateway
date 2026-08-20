#!/bin/sh
set -e

# 确保 TZ 环境变量已设置
export TZ="${TZ:-Asia/Shanghai}"

DATA_DIR="${MPG_DATA_DIR:-/data}"
if [ -n "${MPG_RUNTIME_DIR:-}" ]; then
  RUNTIME_DIR="${MPG_RUNTIME_DIR}"
else
  RUNTIME_DIR="${DATA_DIR}/runtime"
fi

# 数据目录与卷内运行时布局（青龙式：工具放 volume，镜像保持极简）
mkdir -p "${DATA_DIR}" \
  "${RUNTIME_DIR}/bin" \
  "${RUNTIME_DIR}/node" \
  "${RUNTIME_DIR}/npm" \
  "${RUNTIME_DIR}/python" \
  "${RUNTIME_DIR}/uv" \
  "${RUNTIME_DIR}/cache" \
  "${RUNTIME_DIR}/state"

# 仅在缺失时写入说明，避免覆盖用户修改
README_FILE="${RUNTIME_DIR}/README.txt"
if [ ! -f "${README_FILE}" ]; then
  cat > "${README_FILE}" <<EOF
MCP Proxy Gateway — Linux 容器受管运行时目录

本目录仅由官方 Linux Docker/OCI 镜像管理。原生 Windows/macOS 和 Windows 容器不支持
运行环境页的安装、卸载和依赖管理；Windows/macOS Docker Desktop 请使用 Linux 容器引擎。

将 Node / Python / uv 等可执行文件放入本目录后，stdio 上游即可被探测与启动。
本目录位于数据卷内，容器更新不会丢失。

镜像说明：
  默认 slim（:latest）不含系统 Node/Python；Node/uv 可使用管理台受管安装。
  完整版 full（:full）已内置 Node/npm 与 Python3；pip 依赖管理需要 Python。

推荐布局：
  bin/           用户手动放置的直接可执行文件（优先加入 PATH）
  node/bin/      受管 Node 发行版
  npm/           受管 npm 共享依赖、node_modules 与 CLI shim
  python/bin/    用户手动放置的 Python 发行版
  uv/bin/        受管 uv 发行版
  cache/         npm / uv 受管缓存
  state/         受管安装状态

npm 共享依赖适用于受管 CLI 与 CommonJS 兼容查询；ESM 项目应维护自己的本地依赖。

当前路径：${RUNTIME_DIR}
放置文件后请刷新运行环境探测。也可设置 MPG_RUNTIME_DIR 覆盖本目录位置。
EOF
fi

# 将已存在的工具目录前置到 PATH（不存在则跳过）
prepend_path() {
  dir="$1"
  if [ -d "${dir}" ]; then
    case ":${PATH}:" in
      *":${dir}:"*) ;;
      *) PATH="${dir}${PATH:+:${PATH}}" ;;
    esac
  fi
}

# 逆序 prepend 以使最终顺序为 bin → node/bin → npm/.bin → python/bin → uv/bin → 原 PATH
prepend_path "${RUNTIME_DIR}/uv/bin"
prepend_path "${RUNTIME_DIR}/python/bin"
prepend_path "${RUNTIME_DIR}/npm/node_modules/.bin"
prepend_path "${RUNTIME_DIR}/node/bin"
prepend_path "${RUNTIME_DIR}/bin"
export PATH

exec "$@"
