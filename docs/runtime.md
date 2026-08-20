# 本地运行时（stdio）说明

## 适用范围

受管 Node、uv、npm、pip 的安装、卸载与依赖管理仅支持官方 Linux Docker/OCI 镜像：`linux/amd64` 与 `linux/arm64`。

- 原生 Windows/macOS 与 Windows 容器不支持这些受管操作；运行环境摘要会通过 `managementSupported` 与 `managementReason` 说明当前是否可用及原因。
- 在 Windows/macOS 上使用 Docker Desktop 时，只有切换到 **Linux containers** 引擎并运行官方镜像才受支持。
- 此限制只约束运行环境页面的受管变更操作；远程上游和已有本地工具的探测、策略校验不以此为前提。

## 安全模型（分层）

1. **策略层（全平台）**
   - `runtime.stdio_enabled`：一键禁用本地执行
   - 命令 denylist（shell/包装器不可放行）+ allowlist（默认模板命令）
   - 父进程敏感环境会被剥离；用户在 `connParams.env` 中显式配置的 MCP 凭证可按需要注入
2. **本地运行安全档位（每上游可覆盖）**
   - `standard`（默认）：全局命令白名单 + 环境清理 + 进程清理
   - `strict`：与 `strict_command_allowlist` 取交集（默认无 npm，避免严格档触发装包）、强制 cwd/文件允许路径、禁止脚本自装包意图、严格档仅 runtime 卷解析命令、环境继承收紧；**允许 npx/uvx，但目标包必须在 `strict_package_allowlist`（或上游 `packageAllowlist`）内**
   - `unrestricted`：denylist-only（仍禁 bash/cmd 等），管理台强红告警 + 二次确认
   - 配置键：全局 `runtime.default_stdio_security_mode`；每上游 `connParams.securityProfile`
   - **说明：** 策略本身不是内核沙箱。Linux 严格档在检测到 `bwrap` 时会尝试启用文件 bind 隔离，网络 `deny` 时会创建网络命名空间；在 Docker 内是否实际可用仍取决于容器权限与安全策略，存在 `bwrap` 二进制并不保证隔离能够启动。
3. **卷路径层**
   - 默认 `{MPG_DATA_DIR}/runtime`，可用 `MPG_RUNTIME_DIR` 覆盖
   - Doctor / Resolve / 子进程 PATH 优先卷内 `bin`、`node/bin`、`npm/node_modules/.bin`、`python/bin`、`uv/bin`
4. **受控预置安装与依赖管理**
   - 预置安装仅允许内置目录中、带官方 URL 与 SHA256 校验的固定 Node/uv 发行版；当前默认 Node 为 24 LTS
   - 禁止任意 URL；受管 npm/pip 依赖命令仅接收包名或版本规格，不接受路径、URL 或命令行参数
   - 状态位于 `runtime/state/installed.json`；npm 与 uv 缓存位于 `runtime/cache`
5. **进程加固（Linux）**
   - 独立进程组 + 父进程退出时 SIGTERM（`Pdeathsig`）
   - 会话 Close 后按进程组 SIGTERM/SIGKILL，清理 `npx`/`uvx` 等孙进程

远程 SSE / HTTP / WebSocket / OpenAPI **不经过**上述本地执行路径。

## 目录布局

```text
$MPG_DATA_DIR/runtime/
  bin/
  node/
  npm/
  python/
    .venv/
  uv/
  cache/
  state/installed.json
  README.txt
```

- `npm/` 是 npm 依赖的独立 prefix，保存共享 `node_modules` 与 CLI shim。`npm/node_modules/.bin` 会加入 stdio 子进程的 `PATH`，因此受管 CLI 可直接执行。
- 重装或卸载受管 Node 不会删除 `runtime/npm/` 中的依赖。`NODE_PATH` 仅用于 CommonJS 兼容查找；ESM 上游或项目仍应在自己的项目目录安装和维护依赖。
- pip 依赖由 uv 管理到 `runtime/python/.venv/`。创建该 venv 必须有可用 Python 解释器：`:full` 已包含 Python3，`:latest` slim 不包含；单独安装 uv 本身不能提供 Python。slim 镜像需要先自行提供 Python 后才可管理 pip 依赖。
- `cache/` 保存 npm、uv 的受管缓存，容器更新后随数据卷保留。

## 镜像策略

| 镜像 / 标签 | 说明 |
|---|---|
| **精简（默认）** `Dockerfile` → `:latest` / `:<version>` | Debian bookworm-slim/glibc + 静态二进制，**无** Node/Python；可受管安装 Node/uv，但 pip 依赖仍需要自行提供 Python |
| **完整** `Dockerfile.full` → `:full` / `:<version>-full` | Debian bookworm-slim/glibc，内置 **Node.js 24 LTS + npm/npx + Python3 + bwrap**，常见 stdio MCP 开箱即用 |
| 推荐路径 | 远程 MCP 用精简版；大量本地 stdio 用完整版；精简版也可在管理台预置装 Node/uv 或放入 `$MPG_DATA_DIR/runtime` |

完整版仍支持卷内 `runtime/` 与预置安装（例如 uv），用于补充或固定版本，不会与系统包冲突（PATH 优先卷内目录）。`bwrap` 是否能在实际容器中提供隔离还取决于容器权限，不能只根据二进制存在判断。

### 完整版的 Node 来源

`:full` 内置的 Node 不使用发行版 `nodejs`/`npm` 包，而是落地与受管预置安装**完全相同**的官方 tarball（同版本、同 SHA256），解压到 `/usr/local`。这样做有两个原因：

- Debian bookworm 仓库的 `nodejs` 是已 EOL 的 Node 18，不适合作为开箱即用的运行时。
- `runtime/node/bin` 在 PATH 上先于 `/usr/local/bin`。若内置版本与受管版本不一致，实际生效的 node 会取决于用户是否执行过受管安装，产生难以排查的行为差异。

版本与校验和的唯一真源是 `internal/runtime/catalog.go` 中 `DefaultNodePackageID` 对应的资产；`Dockerfile.full` 以构建参数固定同一组值，并由 `TestDockerfileFullNodePinMatchesCatalog` 校验两处一致。升级预置 Node 时两处都要改，漏改会直接测试失败。

镜像内也不安装 `python3-pip`：pip 依赖全程由受管 uv 在 `runtime/python/.venv/` 内完成，不经过系统 `pip`，且 `pip` 不在 stdio 命令白名单内，无法作为上游命令启动。

## 为什么不支持 docker 作为 stdio 命令

`docker` 不在默认命令白名单与探测工具列表中，模板市场也不再提供 `docker run` 形态的模板。

网关自身运行在容器内，镜像不提供 `docker` CLI，也不挂载宿主 `/var/run/docker.sock`，因此 `docker run ...` 这类上游永远无法启动，只会得到一个信息量很低的 `initialize: EOF`。而挂载宿主 socket 等同于把宿主 root 权限交给 stdio 子进程，属于容器逃逸原语，不适合作为默认能力。

以容器镜像分发的 MCP 服务请优先使用其远程端点（例如 GitHub 官方托管的 Streamable HTTP 服务）。确有本地需求的自建部署，可自行在 `runtime.command_allowlist` 中加回 `docker` 并自行承担 socket 挂载的风险；此时标准档风险等级会上报为 `high`，严格档仍然拒绝 `docker`。

## 受管依赖与包仓库

- npm 依赖通过 `runtime/npm/` 的独立 prefix 管理；安装、卸载和列表操作不会修改 Node 发行版目录。
- pip 依赖通过 uv 与 `runtime/python/.venv/` 管理；需同时具备 uv 与一个 Python 解释器（不需要系统 `pip`，venv 由 `uv venv` 直接创建）。
- `runtime.npm_registry`、`runtime.pip_index_url` 与 `runtime.uv_index_url` 可为包管理器设置公共或无认证镜像 registry。请仅配置公共/镜像 registry。
- 为避免本地凭证泄露，父进程继承的敏感环境变量会被清理，包括常见 token、密码与云密钥；系统不会默认引入私有 registry credential 支持。需要凭证的 MCP 上游应使用受控、显式的上游配置，并自行评估风险。

## 管理 API

所有接口位于管理员 JWT 保护的 `/api/admin` 下。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/runtime/summary` | 探测、策略、目录、catalog、沙箱能力及 `managementSupported` / `managementReason` |
| GET | `/api/admin/runtime/catalog` | 预置包目录 |
| POST | `/api/admin/runtime/install/preview` | 安装预览 |
| POST | `/api/admin/runtime/install` | 安装预置 Node/uv（同步，最长约 10 分钟） |
| POST | `/api/admin/runtime/uninstall` | 卸载预置 Node/uv |
| GET | `/api/admin/runtime/deps?kind=npm` | 列出 npm 受管依赖 |
| GET | `/api/admin/runtime/deps?kind=pip` | 列出 pip 受管依赖及 Python 可用性提示 |
| POST | `/api/admin/runtime/deps/install` | 安装或升级依赖，JSON：`{ "kind": "npm" | "pip", "spec": "包名[@版本]" }` |
| POST | `/api/admin/runtime/deps/uninstall` | 卸载依赖，JSON：`{ "kind": "npm" | "pip", "name": "包名" }` |
| POST | `/api/admin/runtime/preflight` | 依赖与安全档位预检（可带 `securityProfile` / `cwd` / `args`） |

当 `managementSupported` 为 `false` 时，受管安装、卸载和依赖接口会拒绝变更；请根据 `managementReason` 切换至受支持的官方 Linux Docker/OCI 镜像环境。

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
- `strict_allow_policy_only`（bool，默认 true；无内核隔离时是否允许仅靠策略运行）
- `npm_registry`、`pip_index_url`、`uv_index_url`（可选的公共/镜像包仓库 URL；空表示不覆盖默认源）

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
