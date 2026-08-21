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

# 数据卷内布局。
# 运行时（Node / Python / uv）由镜像提供，不再落在数据卷里；
# 卷内只保留"用户手放的可执行文件"与"依赖"，这样容器更新不会丢依赖。
mkdir -p "${DATA_DIR}" \
  "${RUNTIME_DIR}/bin" \
  "${RUNTIME_DIR}/npm" \
  "${RUNTIME_DIR}/pip" \
  "${RUNTIME_DIR}/cache"

# 仅在缺失时写入说明，避免覆盖用户修改
README_FILE="${RUNTIME_DIR}/README.txt"
if [ ! -f "${README_FILE}" ]; then
  cat > "${README_FILE}" <<EOF
MCP Proxy Gateway — 数据卷运行时目录

Node / Python / uv 由镜像提供，不在此目录，也不在运行期下载安装：
  完整镜像（:latest / :full）已内置 Node 24、Python 3.12、uv/uvx，开箱即用。
  精简镜像（:slim）不含任何本地运行时，只支持远程上游，管理台会隐藏 stdio 相关功能。

本目录只存两类东西，容器更新都不会丢失：

  bin/     用户手动放置的可执行文件（PATH 最高优先级，可用于覆盖镜像内置版本）
  npm/     npm 共享依赖（node_modules 与 CLI，所有 stdio 上游共享）
  pip/     pip 共享依赖（所有 stdio 上游共享）
  cache/   npm / uv 下载缓存

npm 共享依赖适用于 CLI 与 CommonJS 兼容查询；ESM 项目应在项目目录维护自身依赖。

当前路径：${RUNTIME_DIR}
放置文件到 bin/ 后请在管理台「运行环境」刷新探测。
也可设置 MPG_RUNTIME_DIR 覆盖本目录位置。
EOF
fi

# 旧版本曾把受管安装的 Node/uv 落在卷内，且排在 PATH 最前面。
# 现在运行时由镜像提供，这些残留目录已不再加入 PATH，否则会遮蔽镜像内置版本。
# 只提示不删除：那是用户数据卷里的内容，删除应由用户决定。
# python 也在列：旧版 pip 依赖装在 runtime/python/.venv，现已改为 runtime/pip。
for stale in node python uv state; do
  if [ -d "${RUNTIME_DIR}/${stale}" ] && [ -n "$(ls -A "${RUNTIME_DIR}/${stale}" 2>/dev/null)" ]; then
    echo "[mpg] 提示：检测到旧版受管运行时残留目录 ${RUNTIME_DIR}/${stale}，已不再使用。" >&2
    echo "[mpg]       运行时现由镜像提供，pip 依赖已迁至 ${RUNTIME_DIR}/pip。" >&2
    echo "[mpg]       确认无用后可自行删除以释放空间（需要的 pip 包请重新安装一次）。" >&2
  fi
done

# 将已存在的目录前置到 PATH（不存在则跳过）
prepend_path() {
  dir="$1"
  if [ -d "${dir}" ]; then
    case ":${PATH}:" in
      *":${dir}:"*) ;;
      *) PATH="${dir}${PATH:+:${PATH}}" ;;
    esac
  fi
}

# 逆序 prepend，最终顺序：bin → npm/.bin → pip/bin → 镜像内置运行时 → 原 PATH
prepend_path "${RUNTIME_DIR}/pip/bin"
prepend_path "${RUNTIME_DIR}/npm/node_modules/.bin"
prepend_path "${RUNTIME_DIR}/bin"
export PATH

exec "$@"
