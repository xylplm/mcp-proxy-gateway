# 本地运行时（stdio）说明

## 安全模型（分层）

1. **策略层（全平台）**
   - `runtime.stdio_enabled`：一键禁用本地执行
   - 命令 denylist（shell/包装器不可放行）+ allowlist（默认模板命令）
   - 父进程敏感环境剥离；用户 `connParams.env` 可显式注入 MCP 凭证
2. **本地运行安全档位（每上游可覆盖）**
   - `standard`（默认）：全局命令白名单 + 环境清理 + 进程清理
   - `strict`：与 `strict_command_allowlist` 取交集（默认无 docker/npm）、强制 cwd/文件允许路径、禁止脚本自装包意图、严格档仅 runtime 卷解析命令、环境继承收紧；**允许 npx/uvx，但目标包必须在 `strict_package_allowlist`（或上游 `packageAllowlist`）内**
   - `unrestricted`：denylist-only（仍禁 bash/cmd 等），管理台强红告警 + 二次确认
   - 配置键：全局 `runtime.default_stdio_security_mode`；每上游 `connParams.securityProfile`
   - **说明：** 当前为用户态策略约束，不是内核沙箱；Linux 若检测到 `bwrap` 仅作能力展示，真隔离后续启用
3. **卷路径层**
   - 默认 `{MPG_DATA_DIR}/runtime`，可用 `MPG_RUNTIME_DIR` 覆盖
   - Doctor / Resolve / 子进程 PATH 优先卷内 `bin`、`node/bin`、`python/bin`、`uv/bin`
4. **受控预置安装（P1b）**
   - 仅内置目录：Node 22.14.0、uv 0.6.14（官方 URL + SHA256）
   - 禁止任意 URL / 任意 npm 包名
   - 落盘 `runtime/`，状态 `runtime/state/installed.json`
5. **进程加固（P2，Linux）**
   - 独立进程组 + 父进程退出时 SIGTERM（`Pdeathsig`）
   - 会话 Close 后按进程组 SIGTERM/SIGKILL，清理 `npx`/`uvx` 等孙进程
   - Windows/macOS 为策略层加固 + `Process.Kill` 尽力清理，不阻断开发测试

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

| 镜像 / 标签 | 说明 |
|---|---|
| **精简（默认）** `Dockerfile` → `:latest` / `:<version>` | Alpine + 静态二进制，**无** Node/Python；体积最小 |
| **完整** `Dockerfile.full` → `:full` / `:<version>-full` | 内置 **Node.js + npm + Python3**，常见 stdio MCP 开箱即用 |
| 推荐路径 | 远程 MCP 用精简版；大量本地 stdio 用完整版；精简版也可在管理台预置装 Node/uv 或放入 `$MPG_DATA_DIR/runtime` |

完整版仍支持卷内 `runtime/` 与预置安装（例如 uv），用于补充或固定版本，不会与系统包冲突（PATH 优先卷内目录）。

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
- `default_stdio_security_mode`（`standard` | `strict` | `unrestricted`，默认 `standard`）
- `strict_command_allowlist`（string[]，默认 node/npx/python/python3/uv/uvx）
- `strict_package_allowlist`（string[]，默认含 `@modelcontextprotocol/*` 与内置模板常用包；支持 `@scope/*`）
- `global_file_roots`（string[]）
- `strict_path_only_runtime`（bool，默认 true）
- `strict_network_default`（`allowlist` | `deny`，默认 `allowlist`）
- `strict_allow_policy_only`（bool，默认 true；预留给真隔离不可用时的策略）

### 严格档 npx / uvx 包白名单

- **不是禁止** `npx`/`uvx`，而是限制它们能启动的包/工具名。
- 匹配规则：精确名，或 `@scope/*` 前缀（如 `@modelcontextprotocol/server-filesystem`）。
- 拒绝：本地路径（`./`、绝对路径）、`http(s)://` / `git+` URL、`npx -c` / shell 类参数、`uvx install` 等装包子命令。
- 生效白名单 = 全局 `strict_package_allowlist` ∪ 上游 `securityProfile.packageAllowlist`。

## 每上游 `connParams.securityProfile`（stdio）

```json
{
  "mode": "strict",
  "fileAccess": { "mode": "allowlist", "paths": ["/data/workspaces/demo"] },
  "network": { "mode": "deny", "hosts": [] },
  "dependencyPolicy": "declared_only",
  "packageAllowlist": ["@my-org/*", "my-custom-mcp"],
  "allowSelfInstall": false,
  "note": "可选备注"
}
```

## 管理 API（补充）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/admin/runtime/preflight` | 依赖 + 安全档位预检（可带 `securityProfile`/`cwd`/`args`） |
