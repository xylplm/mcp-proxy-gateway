# MCP Proxy Gateway 优化方案

## 定位与边界

本项目优先服务自部署、单管理员、可信管理者场景。优化目标是让网关更好接入、更好排障、更好备份恢复、更适合长期运行，而不是把它改造成多租户 SaaS 平台。

明确不纳入本阶段优化：

- 不调整 API Key 明文持久化与管理台查看/复制能力。管理员可二次查看密钥是当前产品体验的一部分。
- 不调整 release 流水线中 ESLint/gofmt 的阻断策略，也不新增强制 PR/Push CI。
- 不做公网 Base URL 自动推断或覆盖配置。API 服务页继续使用占位符，由用户按真实部署环境替换。
- 不引入复杂 RBAC、多租户、组织空间、审批流等重型能力。

## 总体判断

当前项目后端分层较清晰，管理 JWT 与外部 API Key 链路隔离，统计、审计、安全中心、模板市场和多传输 MCP 能力已经具备。下一阶段最值得做的是补齐产品化闭环：

- 配置出错前能发现。
- 调用失败后能定位。
- 数据和配置能可靠迁移恢复。
- 首页能告诉用户下一步做什么。
- 长期运行时日志、统计和安全事件更易消费。

## P0：近期最值得做

### 1. 备份导入导出接入管理台

现状：`internal/backup` 已有备份模型、编解码、服务和测试，但管理 API 与前端入口尚未接入。

价值：

- 用户迁移部署、升级前备份、误操作恢复会更安心。
- 已有内核，投入产出比高。
- 自部署场景下非常实用。

建议方案：

- 后端新增管理接口：
  - `GET /api/admin/backup/export`
  - `POST /api/admin/backup/import`
- 导出文件名包含版本和时间，例如 `mpg-backup-20260623-153000.json`。
- 导入前先解析校验，返回预览信息：
  - 上游数量
  - API Key 数量
  - 别名规则数量
  - 屏蔽规则数量
  - ACL 数量
  - 是否包含敏感明文
- 导入采用二次确认，文案明确“会覆盖当前业务配置”。
- 导入成功后写审计日志，并提示需要刷新页面或重启网关。

落地模块：

- `internal/httpapi/backup.go`
- `internal/httpapi/router.go`
- `internal/app/build.go`
- `web/src/api/backup.ts`
- `web/src/views/SettingsView.vue`

### 2. 上游连接测试与工具预览

现状：用户创建上游时，通常要保存后才能知道连接参数是否正确、能否拉取工具。

价值：

- 降低接入失败挫败感。
- 模板市场和手动接入都会受益。
- 能减少“连接失败但不知道哪里错”的排障成本。

建议方案：

- 在上游创建/编辑抽屉加入“测试连接”按钮。
- 后端提供临时探测接口，不落库：
  - 校验连接参数
  - 建立临时会话
  - 尝试 `list_tools`
  - 返回工具数量、耗时、失败阶段、错误摘要
- 成功时展示前 5-10 个工具预览。
- 失败时按阶段给出可执行提示：
  - 参数格式非法
  - 连接超时
  - 鉴权失败
  - 上游协议不兼容
  - 工具列表解析失败

落地模块：

- `internal/httpapi/upstream.go`
- `internal/transport`
- `web/src/components/upstreams/UpstreamFormDrawer.vue`
- `web/src/api/upstreams.ts`

### 3. 首页待办与健康摘要

现状：首页更多是指标卡与快速入口。新部署用户还需要自己判断下一步。

价值：

- 首次使用路径更清楚。
- 日常运维能第一眼看到问题。
- 不需要引入复杂引导系统。

建议方案：

首页增加“待处理事项”区域：

- 未创建上游：提示添加上游或打开模板市场。
- 存在启用但不可用上游：提示查看错误或重连。
- 已有上游但工具数为 0：提示刷新工具。
- 未创建 API Key：提示创建访问密钥。
- 没有任何调用记录：提示打开 API 服务页复制接入示例。
- MCP 仍复用管理端口：提示可按需启用独立端口，但不强制。
- 存在安全封禁或 24 小时异常访问：提示打开安全中心。

落地模块：

- `web/src/views/DashboardView.vue`
- `web/src/api/health.ts`
- `web/src/api/security.ts`
- `web/src/api/stats.ts`

## P1：增强排障与运维体验

### 4. 调用链路诊断面板

建议在调用记录详情里补充更明确的诊断信息：

- 使用的 API Key
- 模式：full / smart
- 来源：api / xiaozhi
- 命中的上游
- 原始工具名与暴露工具名
- 路由策略结果
- 上游耗时
- 错误分类
- 请求参数和响应结果的折叠查看

这不需要改变统计模型太多，可以先基于已有 Redis 最近调用记录扩展展示。

落地模块：

- `internal/stats/recent.go`
- `internal/aggregation`
- `web/src/views/CallRecordsView.vue`
- `web/src/components/call-records/CallRecordDetailModal.vue`

### 5. 上游详情页

当前上游列表卡片已经能完成多数操作，但当上游变多、工具变多后，单卡片承载能力有限。

建议新增上游详情视图：

- 基本配置
- 连接状态历史
- 最近错误
- 工具列表与搜索
- 该上游相关别名/屏蔽规则
- 最近调用统计
- 手动同步记录

可以先用抽屉或详情 modal 做轻量版本，不必新增复杂路由。

落地模块：

- `web/src/views/UpstreamsView.vue`
- `web/src/components/upstreams`
- `internal/httpapi/upstream.go`
- `internal/httpapi/stats.go`

### 6. 模板市场增强

模板市场已经存在，后续可以做轻量增强：

- 最近使用模板
- 收藏模板
- 模板按使用场景分组，例如搜索、浏览器、代码仓库、数据库、知识库
- 模板字段加入更具体的校验提示
- 支持自定义本地模板导入导出

注意不建议一开始做远程模板市场，维护成本和安全审查成本都较高。

落地模块：

- `internal/template`
- `web/src/components/upstreams/TemplateMarketModal.vue`
- `web/src/api/templates.ts`

### 7. 规则调试器

别名和屏蔽规则会影响最终暴露工具，用户需要知道规则是否按预期生效。

建议新增“规则调试”能力：

- 输入工具名或选择已有工具。
- 展示经过哪些规则。
- 展示过滤前、过滤后、别名后的结果。
- 支持指定 API Key 视角。

价值：

- 排查“为什么客户端看不到某工具”。
- 排查“为什么工具名被改成这样”。

落地模块：

- `internal/domain/rule_engine_impl.go`
- `internal/aggregation/pipeline.go`
- `internal/httpapi/rules.go`
- `web/src/views/RulesView.vue`

## P1：长期运行体验

### 8. 日志与事件下载

现有系统日志、安全事件、审计日志都能查看，但导出能力可以补齐：

- 导出当前筛选结果为 JSON/CSV。
- 系统日志支持按级别、关键字、时间范围过滤。
- 安全事件支持按 IP/API Key/事件类型过滤后导出。

价值：

- 用户反馈问题时可以直接带上诊断包。
- 自部署场景下方便存档。

落地模块：

- `internal/httpapi/systemlog.go`
- `internal/httpapi/audit.go`
- `internal/httpapi/security.go`
- `web/src/views/SystemLogsView.vue`
- `web/src/views/AuditView.vue`
- `web/src/views/SecurityCenterView.vue`

### 9. 轻量诊断包

新增“下载诊断包”功能，包含：

- 当前版本
- 运行配置脱敏摘要
- 健康检查结果
- 上游状态
- 最近系统日志
- 最近安全事件
- 最近失败调用

不包含：

- API Key 明文
- 上游凭证
- 请求/响应明细中的敏感字段

这比让用户手动截图、复制日志更高效。

落地模块：

- `internal/httpapi/diagnostics.go`
- `internal/health`
- `internal/syslog`
- `web/src/views/AboutView.vue` 或 `SettingsView.vue`

### 10. 配置变更影响提示

当前系统设置保存会重启。建议对不同字段给出影响提示：

- 立即生效：日志级别、智能模式返回数量、小智配置等。
- 需要重启：监听端口、管理端口 MCP 暴露策略。
- 会影响调用：上游调用超时、路由策略、同步周期。

前端可以在保存确认框中列出本次改动摘要，而不是统一文案。

落地模块：

- `web/src/views/SettingsView.vue`
- `web/src/api/settings.ts`

## P2：性能与可维护性

### 11. 聚合工具集短缓存

一些页面和调用路径会频繁构建聚合工具集。建议增加进程内短缓存：

- key：`apiKeyID + mode + revision`
- 失效时机：上游增删改、规则变更、API Key 规则变更、工具同步完成
- TTL：5-30 秒即可

不要先做复杂 Redis 分布式缓存，当前单进程架构用内存缓存更简单。

落地模块：

- `internal/aggregation`
- `internal/cache`
- `internal/httpapi/tools.go`

### 12. 大列表服务端分页和搜索

优先对象：

- 安全事件
- 审计日志
- API Key
- 工具列表
- 调用记录

策略：

- 已有分页的保持。
- 还在前端分页的，规模上来后改服务端分页。
- 搜索字段尽量少而准：名称、前缀、IP、工具名、上游名。

落地模块：

- `internal/store/repo_*`
- `internal/httpapi/*`
- `web/src/api/*`

### 13. 前端大文件拆分

当前部分 Vue 文件偏大：

- `web/src/components/upstreams/UpstreamFormDrawer.vue`
- `web/src/views/APIServiceView.vue`
- `web/src/views/UpstreamsView.vue`
- `web/src/views/SettingsView.vue`

建议按业务块拆分：

- 表单字段组件
- 接入引导组件
- 状态摘要组件
- 工具详情组件
- 设置分区组件

目标不是抽象一套大框架，而是让单文件更容易维护。

### 14. 迁移机制显式化

当前数据库初始化主要依赖 AutoMigrate 加补充 DDL。短期够用，但后续如果字段变更多，建议引入轻量迁移记录表：

- 记录已执行迁移版本。
- 保留幂等 SQL。
- 启动时按顺序执行未应用迁移。

不要一次性引入重型 migration 框架，先满足可追踪和可回滚说明即可。

## 新功能候选池

以下功能可以按用户反馈选择性推进：

### A. API Key 使用画像

在 API Key 详情里展示：

- 最近调用时间
- 7 天调用量
- 失败率
- 常用工具 Top 10
- 最近来源 IP
- 是否命中过 ACL 拒绝

价值：让用户知道某个 Key 是否还在用，是否可以停用。

### B. 上游配额看板

如果上游配置了限流/配额，展示：

- 当前窗口剩余额度
- 最近超额次数
- 哪些工具最消耗额度
- 路由策略下各上游分摊情况

价值：帮助决定上游排序和路由策略。

### C. 工具目录页

新增一个全局工具目录：

- 搜索工具名/描述
- 按上游、标签、是否冲突筛选
- 查看来源上游
- 查看入参 schema
- 查看最近调用情况

价值：当工具数量变多时，工具目录比散落在上游详情里更好用。

### D. 规则影响预览

创建/编辑规则时实时展示会影响哪些工具：

- 将被屏蔽的工具数量
- 将被改名的工具列表
- 是否产生重名冲突

价值：降低规则误伤。

### E. 小智接入专项页

如果小智集成是重点，可以从 API 服务页拆出专项卡片：

- 连接状态
- 最近重连次数
- 最近错误
- 当前模式
- 发送给小智的工具数量

价值：语音终端场景排障更直观。

### F. 配置快照历史

每次保存系统设置、上游、规则时记录一份轻量快照：

- 只保留最近 N 份。
- 支持查看差异。
- 支持手动恢复。

价值：自部署场景下防误操作，但实现要控制范围，避免做成复杂版本管理。

## 推荐实施顺序

1. 备份导入导出 API/UI。
2. 上游连接测试与工具预览。
3. 首页待办与健康摘要。
4. 调用记录详情增强。
5. 规则调试器或工具目录页，二选一看用户反馈。
6. 日志/事件导出与诊断包。
7. 聚合工具集短缓存。
8. 大文件拆分与轻量迁移机制。

## 验收标准

每个功能尽量用小闭环验收：

- 有明确入口。
- 有成功、失败、空态。
- 有审计或日志记录。
- 有后端单测，涉及前端则至少通过 `npm run build`。
- 不改变当前自部署可信管理员体验。
