# 本地运行时（stdio）说明

## 安全模型（分层）

1. **策略层（全平台）**
   - `runtime.stdio_enabled`：一键禁用本地执行
   - 命令 denylist（shell/包装器不可放行）+ allowlist（默认模板命令）
   - 父进程敏感环境剥离；用户 `connParams.env` 可显式注入 MCP 凭证
2. **卷路径层**
   - 默认 `{MPG_DATA_DIR}/runtime`，可用 `MPG_RUNTIME_DIR` 覆盖
   - Doctor / Resolve / 子进程 PATH 优先卷内 `bin`、`node/bin`、`python/bin`、`uv/bin`
3. **受控预置安装（P1b）**
   - 仅内置目录：Node 22.14.0、uv 0.6.14（官方 URL + SHA256）
   - 禁止任意 URL / 任意 npm 包名
   - 落盘 `runtime/`，状态 `runtime/state/installed.json`
4. **进程加固（P2，Linux）**
   - 独立进程组 + 父进程退出时 SIGTERM
   - Windows/macOS 为策略层加固（no-op 进程属性），不阻断开发测试

远程 SSE / HTTP / WebSocket / OpenAPI **不经过**上述本地执行路径。

## 目录布局

```
$MPG_DATA_DIR/runtime/
  bin/
  node/
  python/
  uv/
  cache/
  state/installed.json
  README.txt
```

## 镜像策略

| 镜像 | 说明 |
|---|---|
| 默认 `Dockerfile` | Alpine + 静态二进制，无 Node/Python |
| 可选 `Dockerfile.stdio` | 叠加系统 nodejs/npm，体积更大 |
| 推荐 | 默认镜像 + 管理台预置安装 / 卷内手动放入工具 |

## 管理 API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/runtime/summary` | 探测 + 策略 + 目录 + catalog |
| GET | `/api/admin/runtime/catalog` | 预置包目录 |
| POST | `/api/admin/runtime/install/preview` | 安装预览 |
| POST | `/api/admin/runtime/install` | 安装（同步，最长约 10 分钟） |
| POST | `/api/admin/runtime/uninstall` | 卸载预置包 |

## 配置字段（YAML `runtime`）

- `stdio_enabled`（bool，默认 true）
- `command_allowlist`（string[]，空则回填默认）
- `extra_sensitive_env_prefixes`（string[]）
- `process_hardening`（bool，默认 true）
