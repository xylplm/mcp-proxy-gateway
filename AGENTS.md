# Agent Instructions

## 项目概况

本仓库是 MCP Proxy Gateway，后端使用 Go，前端管理台位于 `web/`，使用 Vue 3 + Vite + Tailwind CSS。

主要目录：
- `cmd/gateway`：网关启动入口。
- `internal/domain`：领域模型、规则引擎与业务校验，优先保持纯函数和小接口。
- `internal/httpapi`：管理 REST API 组合层，负责路由、请求解析、统一响应和依赖接线，避免沉入复杂业务逻辑。
- `internal/store`：PostgreSQL/pgx 仓储与迁移，SQL schema 位于 `internal/store/migrations`。
- `internal/manager`、`internal/sync`、`internal/apikey` 等：应用服务与后台流程。
- `web/src/api`：前端 API 封装，统一复用 `requestData`。
- `web/src/components`：可复用 Vue 组件，通用组件放 `web/src/components/common`。
- `web/src/views`：页面级组件。
- `web/src/directives`：全局 Vue 指令，例如 `v-tooltip`。

## 后端约定

- Go 版本以 `go.mod` 为准，当前为 Go 1.25。
- HTTP 框架使用 Gin；管理端点统一挂在 `/api/admin`，由 `internal/httpapi/router.go` 装配。
- 管理 API 统一响应信封，前端依赖 `{ code, message, data }`，成功业务码为 `20000`。
- `httpapi` 是组合层：解析请求、调用应用服务或仓储、复用领域校验、返回统一错误；不要把复杂业务流程直接堆在 handler 里。
- 领域规则、业务校验优先放 `internal/domain` 或对应应用服务中，仓储只负责持久化和数据映射。
- 仓储实现保持类型安全，错误通过领域错误模型归一化；涉及 schema 变化时同步更新迁移、仓储和测试。
- 新增接口时同步检查前端 `web/src/api` 的类型和路径，保持请求/响应字段一致。
- Go 代码提交前优先运行 `go test ./...`。若沙箱无法写 Go build cache，需要请求提升权限执行同一命令。

## 前端约定

- 前端使用 Vue 3 `<script setup lang="ts">`、Pinia、Vue Router、Axios、Tailwind CSS。
- API 请求统一走 `web/src/api/request.ts` 的 `requestData<T>`；业务侧捕获的错误默认是 `ApiError`。
- 路由页面放 `web/src/views`，页面内可拆分的业务区块放 `web/src/components/<domain>`。
- 通用组件放 `web/src/components/common`，全局指令放 `web/src/directives`，并在 `web/src/main.ts` 注册。
- Tooltip 使用公共能力：
  - 简单元素使用 `v-tooltip="'说明'"` 或 `v-tooltip:bottom-end="'说明'"`。
  - 复杂包裹使用全局 `<Tooltip content="说明" placement="bottom-end">...</Tooltip>`。
  - 不要为图标按钮使用原生 `title` 充当主要提示。
- 图标按钮必须保留 `aria-label`；纯视觉 SVG 使用 `aria-hidden="true"`。
- UI 文案面向用户表达业务含义，不要展示实现说明，例如“响应式卡片”“PC/2K/4K 布局”等。
- 布局保持管理后台风格：信息密度适中、控件清晰、避免营销式 hero 和无意义装饰。
- 颜色定义和新增色值尽量遵从项目常用颜色与 Tailwind theme token，优先使用 `brand`、`gray`、`success`、`error`、`warning` 等既有色阶，避免随意新增孤立色值。
- 尽量不使用表格作为主要信息布局，优先使用卡片、网格、列表和可折叠区块，保证手机等小屏幕用户也能顺畅查看和操作；确需表格时必须提供小屏可用的响应式方案。
- 所有页面都需要响应式设计，覆盖 4K、2K、PC、平板、手机等不同分辨率设备，确保布局、信息密度、操作控件和文本在各断点下都有最优 UI 交互体验。
- Tailwind 类优先复用现有风格；动态创建的 DOM 不要依赖运行时拼接的 Tailwind 类名，公共样式应放到 `web/src/assets/main.css`。
- 前端提交前优先运行 `npm run build`；必要时再运行 `npm run lint` 或 `npm run format:check`。

## 变更流程

- 修改前先查看相关文件和现有模式，保持改动范围紧凑。
- 不要回滚或覆盖用户已有改动；遇到无关变更时忽略，遇到冲突时先确认上下文。
- 后端行为变更需要同步测试；接口字段或路由变更需要同步前端 API 类型和调用点。
- 前端 UI 变更至少运行 `npm run build`，后端/跨端变更至少运行 `go test ./...` 与 `npm run build`。
- 提交前执行 `git diff --check`，确认无空白错误。
- 提交前检查 `git status --short` 和 staged 文件列表，避免把无关文件带入提交。

## 提交信息规范

生成中文提交信息，格式：`<type> <emoji>: <subject>`，总长度不超过 200 字。

类型与对应 emoji：
- `feat` ✨：新功能
- `add` ➕：新增文件或内容
- `fix` 🐛：最终修复；进行中或部分修复用 `to:` 标明
- `docs` 📚：文档变更
- `merge` 🔀：分支合并
- `deps` 📦：依赖变更
- `build` 🏗️：构建系统变更
- `perf` ⚡：性能优化
- `refactor` 🔁：仅重构，无行为变更
- `chore` 🔧：仅工具或配置变更

提交摘要必须：
1. 使用中文。
2. 包含对应 emoji。
3. 采用语义化祈使语气。
4. 包含模块名称。

示例：`feat ✨: 首页增加豆瓣电影正在热映小部件`