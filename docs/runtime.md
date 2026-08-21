# 本地运行时（stdio）说明

## 运行时来源：镜像内置，不在运行期下载

Node、Python、uv / uvx 全部由**完整镜像**内置，网关不再提供运行期下载安装。数据卷只保存两类东西：用户手放的可执行文件，以及 npm / pip 共享依赖。

这样换来三点确定性：

- 版本不漂移。镜像 tag 唯一决定运行时版本，不存在「装过 / 没装过」两套行为。
- 没有下载失败面。不需要处理官方源不可达、镜像源回退、SHA256 校验、断点续传、状态文件损坏。
- PATH 语义单一。卷内 `bin/` 优先，其余全部来自镜像，不会出现受管目录与系统目录互相遮蔽。

| 镜像 / 标签 | 基础镜像 | 内容 | 适用 |
|---|---|---|---|
| **完整（默认）** `Dockerfile.full` → `:latest`、`:full`、`:<version>` | `python:3.12-slim`（glibc） | 网关 + Node 24 + npm/npx + Python 3.12 + uv/uvx + bubblewrap | 需要本地 stdio MCP、npm/pip 依赖、脚本中心 |
| **精简** `Dockerfile` → `:slim`、`:<version>-slim` | `alpine`（musl） | 仅网关静态二进制 | 纯网关聚合：远程 SSE / HTTP / WebSocket / OpenAPI |

`:full` 与 `:latest` 指向同一镜像，仅为兼容旧标签。老的 `:latest` 用户升级后只是镜像变大，功能不减。

### 为什么精简版用 Alpine，完整版用 python:3.12-slim

精简版不含任何运行时，musl 与 glibc 的差异无从体现，网关自身 `CGO_ENABLED=0` 静态编译，所以用 Alpine 取最小体积。

完整版必须是 glibc：Node 官方 tarball 链接 glibc，Alpine 上跑不起来。基础镜像选 `python:3.12-slim` 而不是 `debian:bookworm-slim` + apt python3，是因为 bookworm 仓库里的 Python 是已 EOL 的 3.11；`python:3.12-slim` 让 Python 版本由我们自己决定，同时仍是 Debian slim 底子，apt 装 bubblewrap 不需要额外仓库。

Node 装在 `/opt/node` 而非合并进 `/usr/local`，避免与基础镜像里的 Python 目录结构混淆；`PATH` 中前置 `/opt/node/bin`。

uv 通过 `COPY --from=ghcr.io/astral-sh/uv:<version>` 引入官方 distroless 镜像里的静态二进制，多架构自动匹配，不需要在 Dockerfile 里维护下载 URL 与校验和。

### uv / uvx 的必要性

- **uv 作为 pip 驱动**：把包装到我们自己的目录（`uv pip install --target`），不碰系统 Python，天然绕开 PEP 668 的 `externally-managed-environment` 限制。基础镜像有没有 `EXTERNALLY-MANAGED` 标记都不影响。
- **uvx 是 Python 生态的 npx**：官方 Python 系 MCP 服务（`mcp-server-fetch`、`mcp-server-git`、`mcp-server-sqlite`）只在 PyPI 发布，`uvx <pkg>` 是它们的规范调用方式，npm 上没有对应包。

## 精简镜像下被屏蔽的功能

管理台按 `GET /api/admin/runtime/summary` 的 `localRuntimeSupported` 门控（`imageFlavor` 仅用于展示与排查）：

- 侧边栏隐藏「脚本中心」（受管脚本要靠 Node / Python 执行）。
- 模板市场过滤掉 stdio 形态模板。
- 上游表单不提供 stdio 传输类型（编辑既有 stdio 上游时仍会显示，否则表单无法呈现当前值）。
- 运行环境页保留，但依赖管理区块替换为精简镜像说明；npm / pip 依赖接口返回 `ErrLocalRuntimeUnsupported`。

「运行环境」页面本身不隐藏：它是解释当前形态、查看 stdio 策略与沙箱能力的地方。

判定来源是环境变量 `MPG_IMAGE_FLAVOR`（`full` / `slim`），由 Dockerfile 写入。未声明时按 `full` 处理，因此源码构建与本地开发不会莫名少功能。

## 目录布局

```text
$MPG_DATA_DIR/runtime/
  bin/                      用户手放的可执行文件，PATH 优先级最高
  npm/
    node_modules/           npm 共享依赖
    node_modules/.bin/      npm 包提供的 CLI，自动加入 PATH
  pip/                      pip 共享依赖，顶层即 site-packages，自动加入 PYTHONPATH
    bin/                    pip 包提供的脚本，自动加入 PATH
  cache/                    npm / uv 下载缓存
  README.txt
```

子进程 PATH 前缀顺序：`bin` → `npm/node_modules/.bin` → `pip/bin`，之后才是镜像自带目录。不存在的目录会被跳过。

严格档要求可执行文件落在运行时卷内。允许范围由 `runtimeRootOfPrefix` 从每个前缀反推：剥掉上面那组相对后缀，多个后缀同时匹配时取最窄的根。这样 npm `.bin` 里指向 `../<pkg>/bin/*.js` 的符号链接能解析通过，而运行时目录本身叫 `pip`/`npm` 时也不会把父目录放进白名单。

### npm 依赖

安装到独立 prefix `runtime/npm`（`npm install --prefix`），与 Node 发行版目录完全分离。`npm/node_modules/.bin` 进 PATH，所以 CLI 可直接作为 stdio 命令启动。`NODE_PATH` 指向 `runtime/npm/node_modules`，仅用于 CommonJS 兼容查找 —— ESM 上游或有自己 `package.json` 的项目仍应在项目目录维护依赖。

### pip 依赖：不使用 venv

pip 依赖用 `uv pip install --target runtime/pip` 平铺安装，通过 `PYTHONPATH` 接入子进程，`runtime/pip/bin` 进 PATH。`uv pip list` / `uv pip uninstall` 同样带 `--target`，三个操作对称。

**为什么不用 venv：** venv 不可重定位。`pyvenv.cfg` 与脚本 shebang 写死解释器绝对路径，`lib/pythonX.Y/site-packages` 还带 Python 小版本号。镜像把 Python 从 3.12 升到 3.13 后，卷里的旧 venv 会静默失效 —— 包还在，但解释器找不到、shebang 指向已删除的路径，用户只会看到 `ModuleNotFoundError`。`--target` 目录与解释器路径和小版本都解耦，升级镜像后仍然可用。

代价是 C 扩展包跨 Python 小版本可能需要重装。这是显式、可诊断的失败（import 时报 ABI 不匹配），比 venv 那种「整个依赖区一起消失」更容易处理。

`uv pip --target` 仍需要一个 Python 解释器来确定目标 ABI，交给 uv 按 PATH 发现即可 —— 依赖命令的子进程 PATH 已前置 `runtime/bin`，用户放入的解释器同样优先生效。命令统一带 `--no-python-downloads`，完整镜像另设 `UV_NO_MANAGED_PYTHON=1`、`UV_PYTHON_DOWNLOADS=never` 兜底，避免 uv 在找不到解释器时静默下载数十 MB。执行前网关自己也查一次解释器，缺失时给出「请改用完整镜像或放入 runtime/bin」的引导，而不是把 uv 的原始报错抛给用户。

### 覆盖镜像自带版本

把可执行文件放入 `runtime/bin/` 并赋可执行权限即可，该目录在 PATH 最前。这是唯一的逃生口，也适用于精简镜像下补个别自带运行时的二进制。

## 安全模型（分层）

1. **策略层（全平台）**
   - `runtime.stdio_enabled`：一键禁用本地执行
   - 命令 denylist（shell/包装器不可放行）+ allowlist（默认模板命令）
   - 父进程敏感环境会被剥离；用户在 `connParams.env` 中显式配置的 MCP 凭证可按需要注入
2. **本地运行安全档位（每上游可覆盖）**
   - `standard`（默认）：全局命令白名单 + 环境清理 + 进程清理
   - `strict`：与 `strict_command_allowlist` 取交集（默认无 npm，避免严格档触发装包）、强制 cwd/文件允许路径、禁止脚本自装包意图、仅在 runtime 卷内解析命令、环境继承收紧；**允许 npx/uvx，但目标包必须在 `strict_package_allowlist`（或上游 `packageAllowlist`）内**
   - `unrestricted`：denylist-only（仍禁 bash/cmd 等），管理台强红告警 + 二次确认
   - 配置键：全局 `runtime.default_stdio_security_mode`；每上游 `connParams.securityProfile`
   - **说明：** 策略本身不是内核沙箱。Linux 严格档在检测到 `bwrap` 时会尝试启用文件 bind 隔离，网络 `deny` 时会创建网络命名空间；在 Docker 内是否实际可用仍取决于容器权限与安全策略，存在 `bwrap` 二进制并不保证隔离能够启动。
3. **卷路径层**
   - 默认 `{MPG_DATA_DIR}/runtime`，可用 `MPG_RUNTIME_DIR` 覆盖
   - Doctor / Resolve / 子进程 PATH 优先卷内 `bin`、`npm/node_modules/.bin`、`pip/bin`
4. **依赖管理输入约束**
   - npm/pip 依赖命令只接收包名或版本规格，拒绝路径、URL、以 `-` 开头的项（防 flag 注入）与控制字符
   - 缓存固定在 `runtime/cache`，不写容器临时层
5. **进程加固（Linux）**
   - 独立进程组 + 父进程退出时 SIGTERM（`Pdeathsig`）
   - 会话 Close 后按进程组 SIGTERM/SIGKILL，清理 `npx`/`uvx` 等孙进程

远程 SSE / HTTP / WebSocket / OpenAPI **不经过**上述本地执行路径。

## 为什么不支持 docker 作为 stdio 命令

`docker` 不在默认命令白名单与探测工具列表中，模板市场也不提供 `docker run` 形态的模板。

网关自身运行在容器内，镜像不提供 `docker` CLI，也不挂载宿主 `/var/run/docker.sock`，因此 `docker run ...` 这类上游永远无法启动，只会得到一个信息量很低的 `initialize: EOF`。而挂载宿主 socket 等同于把宿主 root 权限交给 stdio 子进程，属于容器逃逸原语，不适合作为默认能力。

以容器镜像分发的 MCP 服务请优先使用其远程端点（例如 GitHub 官方托管的 Streamable HTTP 服务）。确有本地需求的自建部署，可自行在 `runtime.command_allowlist` 中加回 `docker` 并自行承担 socket 挂载的风险。

## 包仓库镜像

- `runtime.npm_registry`、`runtime.pip_index_url`、`runtime.uv_index_url` 可为包管理器设置公共或无认证镜像 registry，对依赖管理命令与 stdio 子进程同时生效。
- 请仅配置公共/镜像 registry。为避免本地凭证泄露，父进程继承的敏感环境变量会被清理（常见 token、密码与云密钥），系统不提供私有 registry credential 支持。需要凭证的 MCP 上游应使用受控、显式的上游 `env` 配置，并自行评估风险。

## 管理 API

所有接口位于管理员 JWT 保护的 `/api/admin` 下。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/runtime/summary` | 探测、策略、目录、沙箱能力，及 `imageFlavor` / `localRuntimeSupported` |
| GET | `/api/admin/runtime/tools` | 可探测的宿主工具元数据 |
| POST | `/api/admin/runtime/preflight` | 依赖与安全档位预检（可带 `securityProfile` / `cwd` / `args`） |
| POST | `/api/admin/runtime/directory/inspect` | 目录启动方式推断（受允许根约束） |
| GET | `/api/admin/runtime/deps?kind=npm` | 列出 npm 共享依赖 |
| GET | `/api/admin/runtime/deps?kind=pip` | 列出 pip 共享依赖及 Python 可用性提示 |
| POST | `/api/admin/runtime/deps/install` | 安装或升级依赖，JSON：`{ "kind": "npm" \| "pip", "spec": "包名[@版本]" }` |
| POST | `/api/admin/runtime/deps/uninstall` | 卸载依赖，JSON：`{ "kind": "npm" \| "pip", "name": "包名" }` |

精简镜像下依赖接口返回校验错误，错误链上带 `ErrLocalRuntimeUnsupported`，文案引导切换到完整镜像。

## 配置字段（YAML `runtime`）

- `stdio_enabled`（bool，默认 true）
- `command_allowlist`（string[]，空则回填默认）
- `extra_sensitive_env_prefixes`（string[]）
- `process_hardening`（bool，默认 true）
- `default_stdio_security_mode`（`standard` | `strict` | `unrestricted`，默认 `standard`）
- `strict_command_allowlist`（string[]，默认 node/npx/python/python3/uv/uvx）
- `strict_package_allowlist`（string[]，默认含 `@modelcontextprotocol/*`、内置模板常用 npm 包与官方 `mcp-server-*` Python 包；支持 `@scope/*`）
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

## 从旧版本升级

旧版镜像会在数据卷里留下 `runtime/node`、`runtime/python`、`runtime/uv`、`runtime/state` 四个受管目录。新版不再读写它们，容器启动时 entrypoint 只提示存在残留，不会自动删除 —— 确认无用后手动删即可，删除不影响 `runtime/npm` 与 `runtime/pip` 里的依赖。

`runtime/python/.venv` 里的旧 pip 依赖不会自动迁移。需要的包在「运行环境 → 依赖管理 → pip」重新安装一次即可，新位置是 `runtime/pip`。
