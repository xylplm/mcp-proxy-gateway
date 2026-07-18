#!/bin/sh
set -e

# 确保 TZ 环境变量已设置（Alpine 镜像默认无时区配置）
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
  "${RUNTIME_DIR}/python" \
  "${RUNTIME_DIR}/uv" \
  "${RUNTIME_DIR}/cache" \
  "${RUNTIME_DIR}/state"

# 仅在缺失时写入说明，避免覆盖用户修改
README_FILE="${RUNTIME_DIR}/README.txt"
if [ ! -f "${README_FILE}" ]; then
  cat > "${README_FILE}" <<EOF
MCP Proxy Gateway — 本地运行时目录

将 Node / Python / uv 等可执行文件放入本目录后，stdio 上游即可被探测与启动。
默认镜像不含这些工具；本目录位于数据卷内，容器更新不会丢失。

推荐布局：
  bin/           直接放置 node、npx、uv、uvx 等（优先加入 PATH）
  node/bin/      Node 发行版
  python/bin/    Python 发行版
  uv/bin/        uv 发行版
  cache/         包管理器缓存（预留）
  state/         安装状态（预留）

当前路径：${RUNTIME_DIR}
放置文件后请重启网关进程，并在管理台「运行环境」刷新探测。
也可设置环境变量 MPG_RUNTIME_DIR 覆盖本目录位置。
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

# 逆序 prepend 以使最终顺序为 bin → node/bin → python/bin → uv/bin → 原 PATH
prepend_path "${RUNTIME_DIR}/uv/bin"
prepend_path "${RUNTIME_DIR}/python/bin"
prepend_path "${RUNTIME_DIR}/node/bin"
prepend_path "${RUNTIME_DIR}/bin"
export PATH

exec "$@"
