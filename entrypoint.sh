#!/bin/sh
set -e

# 确保 TZ 环境变量已设置（Alpine 镜像默认无时区配置）
export TZ="${TZ:-Asia/Shanghai}"

# 确保数据目录存在
mkdir -p "${MPG_DATA_DIR:-/data}"

exec "$@"
