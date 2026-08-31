# AI 工具风险治理

本文记录 MCP Proxy Gateway 第一版 AI 工具风险治理的架构不变量、迁移方式和运维边界。

## 安全模型

工具风险目录以 `(upstream_id, original_name)` 为稳定身份。对外别名和同名聚合结果不是授权真源，因为不同上游提供的同名工具可能具有不同副作用。

风险等级按 `low < medium < high < blocked` 排序：

- `low`：只读查询，不产生持久状态变化，也不向第三方发送内容。
- `medium`：可控、通常可验证或可恢复的业务写入。
- `high`：删除、权限或凭据变更、外部发送、任意执行、基础设施调整及高影响批量操作。
- `blocked`：不应长期暴露的能力。任何受治理档案都不能发现或调用。

API Key 档案固定为：

- `legacy_unrestricted`：迁移兼容，不应用风险目录，仍受全局过滤、Key 过滤、ACL 和限流约束。
- `readonly`：仅允许 `low`。
- `standard`：允许 `low + medium`，是新建 Key 的默认值。
- `privileged`：允许 `low + medium + high`。

现有 Key 通过数据库字段默认值迁移为 `legacy_unrestricted`，避免升级后立即中断。管理员应完成影响评估后逐个迁移，必要时可将单个 Key 回滚为兼容档。

## 有效风险

所有调用方必须复用 `internal/risk.EffectiveLevel`，不得在 HTTP、聚合或前端复制判定逻辑：

1. 已确认的人工覆盖优先。
2. 否则取 AI 建议与确定性下限中的较高等级。
3. `pending`、`stale`、`error`、缺记录或无有效 AI 结果按 `high` 处理。
4. `blocked` 对所有受治理档案拒绝。
5. 人工降到确定性下限以下，必须同时提交 `force=true` 和非空复核理由。

AI 仅提供建议，不是唯一安全边界。确定性规则以版本 `rules-v1` 提供风险下限；提示词以 `risk-prompt-v2` 标识。工具名称、描述和 Schema 均作为不可信数据编码，不能作为模型指令执行。

## 工具变化与对账

定义指纹为以下规范化 JSON 的 SHA-256：上游 ID、原始名、描述、Input Schema。JSON 对象键顺序不影响指纹，Schema 实质变化会使记录进入 `stale`。

工具缓存成功替换后，后台观察者异步对账风险目录：新增为 `pending`，变化为 `stale`，移除为 `removed` 并保留历史。对账或 AI 故障不得回滚工具缓存，也不得阻断同步主流程。人工字段与 AI 字段独立，重新评级不能覆盖人工结论。

## 鉴权位置

风险过滤发生在 MCP 全局过滤、别名和同名来源聚合之前。被拒绝的来源不会进入反向映射或路由候选；若同名工具仍有允许来源，工具继续可见且只能路由到允许来源。

真实调用在选定来源后、配额预占和上游调用前再次执行实时授权，以覆盖评级或 Key 档案在发现与调用之间变化的情况。拒绝返回稳定错误码 `TOOL_RISK_FORBIDDEN`，不包含工具参数正文或敏感详情。

Smart 与 Full 模式均复用聚合服务，因此工具发现和真实调用共享相同安全视角。管理员使用空 `apiKeyID` 查看全局目录时不应用 Key 风险过滤。

## Provider 密钥与网络

Provider API Key 以明文保存到数据库，管理 API 和管理页面会返回完整 `apiKey`，便于随时查看、复制和修改。创建或更新时，`apiKey` 是完整值；更新为空字符串会清除已保存密钥，空密钥的 Provider 不会发送 Authorization 请求头。

备份同样包含明文 Provider API Key。数据库、备份文件和管理账号都应按敏感凭据保护；不要将导出的备份文件提交到版本库或发送到不受控位置。

Base URL 只接受无 userinfo 的 HTTP/HTTPS URL。客户端限制总超时、响应体大小和跨主机重定向，并支持访问公网、本机或局域网中的 OpenAI-compatible 服务。

## 上线与回滚

建议先部署目录、Provider 和评级能力，完成全量对账与人工复核，再迁移 Key。优先迁移只读任务，其次日常客户端，特权 Key 只用于明确的管理工作流。Provider 可独立停用；单个 Key 可切回 `legacy_unrestricted`。受治理 Key 的风险服务异常必须 fail closed，不能静默扩大权限。
