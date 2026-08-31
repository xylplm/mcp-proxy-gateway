# AI 工具风险治理开发交接单

生成时间：2026-08-23（Asia/Shanghai）

代码分支：`feature/ai-tool-risk-governance`

核心功能提交：`22e5270 feat ✨: 风险治理增加 AI 工具评级与 API Key 来源授权`

Draft PR：[xylplm/mcp-proxy-gateway#2](https://github.com/xylplm/mcp-proxy-gateway/pull/2)

## 阶段状态

| 阶段 | 状态 | 说明 |
| --- | --- | --- |
| 本地测试通过 | 部分通过 | 功能相关 Go 测试、前端构建、ESLint 和 137 项前端单测通过；全量 Go 测试仍有 2 个非本功能失败；全仓库 Prettier 检查仍有历史文件未格式化。 |
| GitHub CI 通过 | 未完成 | PR 创建后暂未返回任何 GitHub 状态检查，不能视为 CI 已通过。 |
| PR 已提交 | 已完成 | 已创建 Draft PR #2，状态为 Open，当前 GitHub 判定可合并，但仍需 Review 和 CI。 |
| PR 已合并 | 未完成 | Draft PR 尚未转为 Ready，也尚未合并。 |
| 家庭生产环境已部署 | 未完成 | 尚未构建并部署 NAS 生产镜像，也未进行家庭生产环境验证。 |

## 1. 实际完成的功能

### AI Provider 管理

- 新增 OpenAI-compatible Provider 的创建、编辑、删除、启用、激活和连通性测试。
- 支持 Chat Completions 与 Responses 两种 API 协议。
- 支持模型、请求超时、单批工具数、最大并发数和同步后自动评级配置。
- 单批工具数限制为 1～50，最大并发数限制为 1～3。
- Provider API Key 明文保存，管理 API 与管理页面均可直接查看、复制和编辑完整值。
- Base URL 仅允许无 userinfo 的 HTTP/HTTPS URL，并限制响应体、总超时和跨主机重定向。

### 工具风险目录与评级

- 以 `(upstream_id, original_name)` 作为工具风险档案的稳定身份。
- 新增 `low / medium / high / blocked` 四级风险等级。
- 使用 `rules-v1` 确定性规则提供不可被普通 AI 结果降低的风险下限。
- 使用 `risk-prompt-v2` 请求 AI 返回风险等级、标签、置信度、理由、中文功能描述和复核建议。
- 工具新增、描述或 Schema 变化、移除时自动将风险目录更新为 `pending / stale / removed` 等状态。
- 支持手动刷新风险目录、启动批量评级、单工具重新评级、失败任务重试和任务取消。
- 评级任务支持批处理、有限并发、可重试错误分类和失败批次拆分；界面会区分 429、503、网络错误、响应格式错误等原因。
- 最近评级任务默认突出最新任务，历史记录通过弹窗查看。

### 人工复核

- 人工复核可查看原始/中文工具描述、Input Schema、确定性规则下限、AI 等级、置信度、理由和建议标签。
- 支持单项和批量人工覆盖，并保留人工理由、复核时间和强制降级标记。
- 人工等级低于确定性规则下限时，必须显式确认并填写理由，操作进入审计。
- 清除人工覆盖后，根据已有 AI 结果、复核原因和错误状态恢复正确状态。

### API Key 风险档案与来源权限

- 新增 `legacy_unrestricted / readonly / standard / privileged` 四类 API Key 风险档案。
- 现有 Key 默认迁移为 `legacy_unrestricted`，新建 Key 默认使用 `standard`。
- 风险授权同时应用于工具发现和真实调用，避免评级或权限在两阶段之间变化时被绕过。
- `pending / stale / error`、缺失记录或无有效 AI 结果，对受治理档案按 `high` 处理。
- `blocked` 对所有受治理档案拒绝；拒绝使用稳定业务错误码 `TOOL_RISK_FORBIDDEN`。
- API Key 可选择访问全部上游，或只允许指定上游；来源权限只会缩小范围，不会绕过风险档案、屏蔽规则、ACL、配额或限流。
- API Key 配置弹窗将“风险档案”作为独立标签页，并在其中并列展示上游来源权限。

### 同步、备份与管理界面

- 工具缓存成功更新后异步对账风险目录；风险对账或 AI 故障不会回滚工具缓存或阻断同步主流程。
- Provider、明文 Provider Key、工具风险目录、人工复核结果、API Key 风险档案和来源权限已纳入备份与恢复；备份文件需按敏感凭据保护。
- 新增“AI 风险治理”管理页面、导航和 API 封装。
- 风险目录默认每页 20 条，可切换 20/50/100，并处理快速翻页请求覆盖和页码越界。
- 原有工具“风险提示标签”继续用于提醒调用 Agent，不作为权限授权真源，可与风险评级配合使用。

## 2. 与原方案相比的调整或未实现项

- 已取消“禁止 Provider 访问私有网络”的配置和校验。最终实现允许访问公网、本机或局域网中的 OpenAI-compatible 服务，以适配 NAS 和家庭网络部署；启动时会删除遗留的 `allow_private_network` 字段。
- “重新评级待复核”不再作为主要界面按钮展示。待复核项保留 AI 结果等待人工确认；后端兼容接口仍保留，但当前前端没有调用入口。
- 根据实际误报情况，将待复核判定收敛为：AI 明确要求复核、置信度低于 0.80、或 AI 结果低于确定性下限等有效原因，并升级提示词为 `risk-prompt-v2`。
- API Key 权限本期实现到“按上游来源选择”，逐工具允许列表暂未实现。
- 未实现本地 Docker 镜像构建和 NAS 部署验证；当前仅完成本地源码运行与测试。
- 未自动迁移现有 API Key 到更严格档案，保留 `legacy_unrestricted` 以避免升级后中断，需管理员分批迁移。

## 3. 主要修改文件和模块

- `internal/risk/`：风险等级、有效等级算法、确定性规则、指纹、提示词、Provider 校验、OpenAI-compatible 客户端、评级任务和授权器。
- `internal/httpapi/ai_risk.go`：AI Provider、风险目录、评级任务、人工复核等管理 API。
- `internal/httpapi/apikey.go`：API Key 风险档案、影响预览和上游来源权限接口。
- `internal/aggregation/aggregation.go`：工具发现与真实调用阶段的风险授权和上游来源授权。
- `internal/apikey/upstream_access.go`：API Key 上游来源权限应用服务。
- `internal/store/models.go`、`internal/store/store.go`：新增字段、表、索引和约束的 schema 初始化。
- `internal/store/repo_ai_provider.go`：AI Provider 仓储。
- `internal/store/repo_tool_risk.go`：工具风险目录与人工覆盖仓储。
- `internal/store/repo_risk_job.go`：评级任务仓储。
- `internal/store/repo_apikey_upstream_access.go`：API Key 来源权限仓储。
- `internal/sync/periodic.go`、`internal/sync/refresh.go`：工具同步后的异步风险目录对账。
- `internal/backup/`：风险治理数据和来源权限的备份、校验与恢复。
- `web/src/views/AIRiskGovernanceView.vue`：AI 风险治理主页面。
- `web/src/components/ai-risk/RiskJobCard.vue`：评级任务状态和错误分类展示。
- `web/src/components/apikeys/APIKeyConfigModal.vue`：风险档案和来源权限配置。
- `web/src/api/aiRisk.ts`、`web/src/api/apikeys.ts`：前端 API 类型与请求封装。
- `docs/ai-tool-risk-governance.md`：安全模型、授权位置和上线回滚说明。

## 4. 数据库迁移与配置变化

数据库由启动阶段的 GORM AutoMigrate 和幂等补充 SQL 自动处理。

新增表：

- `ai_provider`：Provider 协议、模型、明文 API Key、超时、批大小、并发、自动评级和激活状态。
- `tool_risk_assessment`：工具快照、指纹、确定性下限、AI 结论、人工复核和当前状态。
- `risk_assessment_job`：评级任务范围、进度、重试、拆分、错误分类和时间信息。
- `api_key_upstream_access`：API Key 与允许访问上游的多对多关系。

现有 `api_key` 表新增：

- `risk_profile`，非空，默认 `legacy_unrestricted`。
- `upstream_access_mode`，非空，默认 `all`。

额外 schema 操作：

- 为来源权限、单一激活 Provider、风险状态/等级和任务状态增加索引。
- 增加 API Key、上游、Provider 相关外键及级联策略。
- 删除历史字段 `ai_provider.allow_private_network`。
- 将 Provider 密钥字段统一为 `ai_provider.api_key`，并删除历史密文和 nonce 字段；旧 Provider 密钥不会迁移。
- 启动时将“已禁用但仍为 active”的 Provider 纠正为非 active。

没有新增 YAML 配置项。Provider 的运行参数由管理页面保存到数据库。

## 5. Provider 密钥与备份

- Provider API Key 以明文保存，管理台可直接查看、复制和编辑；更新时留空会清除当前密钥。
- 备份文件包含明文 Provider API Key，数据库、备份文件和管理账号都应按敏感凭据保护。
- 备份格式已升级为 `mpg-backup/v2`，旧版加密备份不再支持导入。

## 6. 实际执行的测试命令和结果

### 通过

- `go test ./internal/risk ./internal/store ./internal/httpapi ./internal/aggregation ./internal/apikey ./internal/backup ./internal/sync`
  - 结果：通过。
- `cd web && npm run build`
  - 结果：通过；`vue-tsc --build` 与 Vite 生产构建均成功。
- `cd web && npm run lint`
  - 结果：通过。
- `cd web && npm run test:unit`
  - 结果：通过，137 项测试全部成功。
- 针对本次变更的前端文件执行 `npx prettier --check`
  - 结果：通过。
- `git diff --check`
  - 结果：通过。
- 高置信度密钥、私钥和连接串扫描
  - 结果：未发现真实敏感信息进入提交；`web/dist`、`web/node_modules` 保持忽略。

### 未完全通过

- `go test ./...`
  - `internal/runtime/TestBrowseParentDoesNotEscape` 在当前 macOS 本机环境失败，输出 `parent=""`。
  - `internal/transport/TestResolveDirectoryLaunch` 因当前 Python 环境缺少 `mcp` 模块失败：`ModuleNotFoundError: No module named 'mcp'`。
  - 上述目录没有本功能代码改动；除这两个包外，其余 Go 包通过。
- `cd web && npm run format:check`
  - 全仓库检查未通过。修复本次变更文件后，仍有 53 个历史前端文件不符合当前 Prettier 输出；为避免把大规模无关格式化混入本 PR，没有批量改写。

## 7. 安全与权限边界

- AI 结论只是建议；确定性规则、人工复核和运行时授权共同构成安全边界。
- 受治理 API Key 在风险记录缺失、待评级、过期或评级错误时按高风险处理，采用 fail closed，不静默扩大权限。
- 人工覆盖优先于 AI，但降低到确定性下限以下必须显式强制确认、填写理由并记录审计。
- 风险目录按真实来源工具授权，不以别名或同名聚合结果作为授权真源。
- 工具发现阶段先过滤来源，真实调用选定来源后、配额预占和上游调用前再次授权。
- `legacy_unrestricted` 仅用于升级兼容，仍然受全局过滤、API Key 屏蔽规则、上游来源权限、ACL、配额和限流约束。
- 上游来源权限只能缩小 API Key 可见和可调用范围，不能提高风险档案允许的等级。
- Provider API Key 通过管理 API 明文返回，访问管理端、数据库和备份文件的权限都应受控。
- Provider 允许访问本机和局域网地址，这是为了支持家庭网络服务；管理员应只配置可信 Base URL，并通过网络隔离控制其访问范围。

## 8. 已知问题

- 全量 Go 测试在当前本机环境仍有上述 2 项失败，需要分别核对 macOS 路径测试预期和 Python `mcp` 测试依赖。
- GitHub PR 当前没有返回状态检查，不能确认 GitHub CI 是否配置或是否通过。
- 全仓库仍有 53 个历史前端文件未通过 Prettier 检查，本 PR 只保证本次修改文件格式正确。
- 逐工具 API Key 允许列表尚未实现；当前粒度为风险档案加上游来源。
- Provider 允许私有网络访问，没有内置 SSRF 私网阻断；生产安全依赖管理员配置、容器网络和 NAS 防火墙边界。
- 尚未执行 Docker 镜像构建、NAS 部署、真实升级、备份恢复演练和生产回滚演练。

## 9. 部署、升级和回滚步骤

### 部署前

1. 备份 PostgreSQL、现有应用配置和当前可用镜像标签。
2. 确认生产数据库账号拥有创建/修改表、索引和外键的权限。
3. 先在测试容器或 NAS 临时实例上使用生产数据库副本验证启动迁移；旧 Provider 密文会被清除，需按需重新填写密钥。

### 升级

1. 基于 PR 合并后的确定提交构建带版本标签的镜像，不要使用不可追踪的 `latest` 作为唯一回滚点。
2. 启动新版本并观察 schema 初始化、工具同步和风险目录对账日志。
3. 登录管理台，确认原有 API Key 均为 `legacy_unrestricted`，原有调用没有因升级立即中断。
4. 重新填写并测试需要鉴权的 Provider，执行风险目录刷新和小批量评级；确认 429/503 重试、任务统计和人工复核可用。
5. 完成目录评级和必要复核后，再按“只读客户端 → 日常客户端 → 特权客户端”的顺序分批切换 API Key 风险档案。
6. 根据业务来源逐步设置上游权限，每次修改后验证工具发现和真实调用。

### 回滚

1. 若风险授权影响调用，优先将受影响 API Key 切回 `legacy_unrestricted`，并将上游权限切回 `all`。
2. 停用 Provider 和同步后自动评级，避免产生新的评级任务。
3. 将 NAS 服务切回升级前的已知可用镜像标签。
4. 本次 schema 主要为新增表和字段，旧版本通常会忽略它们；但 `allow_private_network` 字段会被删除，如需精确恢复旧 schema，应恢复部署前数据库备份。
5. 回滚后验证登录、工具同步、API Key 调用、配额、ACL 和上游连接；若回滚前已清除历史 Provider 密钥，需要重新配置。

## 10. 建议更新到《冒险手册》的内容

- 新增“AI 工具风险治理”章节，说明风险等级、状态、确定性下限、AI 建议和人工复核的关系。
- 新增 API Key 四类风险档案的适用场景、迁移顺序和紧急回退到 `legacy_unrestricted` 的方法。
- 新增上游来源权限说明，强调其与风险档案、屏蔽规则、ACL、限流和配额是叠加收敛关系。
- 新增 Provider 配置指南，解释 API 协议、单批工具数、并发数、超时、自动评级及本地/局域网 Base URL 风险。
- 新增 Provider 明文密钥、数据库/备份保护和管理账号权限要求。
- 新增评级任务故障排查表，覆盖 HTTP 429、HTTP 503、网络错误、模型输出格式错误、部分成功和批次拆分。
- 新增“待复核”处理流程，以及强制降低到规则下限以下时的审批和审计要求。
- 新增 NAS 灰度部署、数据库迁移检查、生产验证和回滚清单。
- 记录当前全量测试的两个本机依赖问题与全仓库 Prettier 历史债务，避免后续误判为本功能回归。
