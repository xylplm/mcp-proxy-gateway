# Implementation Plan: MCP Proxy Gateway（实现计划）

## Overview

本实现计划将设计文档拆解为一系列循序渐进、可由编码代理执行的编码任务。整体顺序遵循「基础设施 → 数据层 → 纯函数领域核心（规则引擎、聚合管线）→ 出站适配 → 连接与同步 → 认证/对外服务 → 横切服务 → 管理 API → 扩展接入 → 前端 → 内嵌与装配 → 容器与 CI」的依赖链，确保每个任务都建立在前序产物之上，最终在主程序装配处把所有组件接线为可运行系统，不留孤儿代码。

技术栈：后端 Go 1.22+（`gin`、`pgx/v5`、`go-redis/v9`、`robfig/cron/v3`、`golang-jwt/v5`、`golang-migrate`、`bcrypt`、`log/slog`、MCP Go SDK），前端基于开源模板 TailAdmin Vue（vue-tailwind-admin-dashboard）：Vue3 + Vite + TypeScript + Tailwind CSS + Pinia + Vue Router（全部界面以 Tailwind 工具类构建并遵循 TailAdmin 视觉风格，统计图表用模板内置的 ApexCharts，不依赖任何组件式 UI 库）。属性测试统一使用 `pgregory.net/rapid`，每条属性一个测试、最少运行 100 次迭代，并在测试函数处添加注释：`// Feature: mcp-proxy-gateway, Property {编号}: {属性文本}`。

约定：
- 标记 `*` 的子任务为可选测试任务（单元/属性/集成测试），可在追求 MVP 时跳过；顶层任务不带 `*`。
- 每个任务标注其覆盖的需求条款编号（_Requirements: x.y_）。
- 属性测试任务显式引用设计文档「Correctness Properties」中的属性编号与其校验的需求条款。

## Tasks

- [x] 1. 搭建 Go 项目骨架与配置管理
  - [x] 1.1 用 Go 官方工具链初始化模块、搭建分层目录与核心领域类型/接口骨架
    - 用 `go mod init` 初始化 Go 模块（不手工拼凑 go.mod），再在模块下创建 `cmd/gateway`、`internal/{config,domain,transport,manager,sync,cache,crypto,aggregation,mcpapi,apikey,auth,stats,audit,template,xiaozhi,health,httpapi,static,store}` 目录
    - 定义核心数据类型：`ToolDef`、`ToolResult`、`UpstreamConfig`、`TransportType`、`AliasRule`、`FilterRule`、`ConnState`、`APIError`
    - 定义关键接口骨架（暂空实现）：`UpstreamSession`、`TransportFactory`、`Rule_Engine`、`Aggregation_Service`、`Tool_Cache`、`MCP_Manager`
    - 全部 Go 代码以 `gofmt`/`goimports` 格式化
    - _Requirements: 18.1, 18.2, 25.2, 25.6_

  - [x] 1.2 实现 Config_Manager（环境变量 + YAML 读取、默认值生成、启动校验）
    - 用 `caarlos0/env` 读取 `MPG_PG_DSN`、`MPG_REDIS_ADDR`、`MPG_REDIS_PASSWORD`、`MPG_DATA_DIR`
    - 用 `gopkg.in/yaml.v3` 读取/写回 `data/` 下 YAML 常规配置，定义 admin/auth/sync/connection/aggregation/mcp_api/statistics/audit/xiaozhi 配置结构与取值范围校验
    - YAML 不存在时以默认配置创建；必需环境变量缺失/无效或 YAML 非法时通过 `slog` 记录错误并终止启动
    - _Requirements: 18.1, 18.2, 18.3, 18.4, 18.5, 18.6_

  - [x] 1.3 编写配置加载单元测试
    - 测试默认配置创建、环境变量缺失终止、YAML 非法终止、配置回写持久化
    - _Requirements: 18.3, 18.5, 18.6_

- [x] 2. 实现数据层（迁移、连接、仓储）
  - [x] 2.1 编写数据库迁移脚本
    - 用 `golang-migrate` 版本化脚本创建 `upstream_mcp`、`alias_rule`、`filter_rule_mcp`、`api_key`、`filter_rule_apikey`、`api_key_acl`、`tool_cache`、`call_stat`（按 `called_at` 时间分区）、`audit_log`，含 `ON DELETE CASCADE` 与设计标注的关键索引
    - 将迁移脚本通过 `embed` 内嵌
    - _Requirements: 23.3, 16.10_

  - [x] 2.2 实现 PostgreSQL 连接池、Redis 客户端与启动迁移执行
    - 用 `pgxpool` 建立 PG 连接池、`go-redis/v9` 建立 Redis 客户端
    - 在连接 PG 成功后、对外服务前自动执行向上迁移，迁移失败记录错误并终止启动
    - _Requirements: 18.1, 23.3_

  - [x] 2.3 实现仓储层（Repository）
    - 为上游 MCP、别名规则、屏蔽规则（MCP 级与 API Key 级）、API Key 元数据、ACL、工具缓存持久副本、调用统计、审计日志实现类型安全的增删查改方法
    - _Requirements: 2.1, 8.1, 9.1, 12.1, 13.1, 16.1, 22.2, 23.3_

  - [x] 2.4 编写仓储层集成测试
    - 针对临时 PG 实例验证 CRUD 与级联删除行为
    - _Requirements: 2.1, 2.5, 23.3_

- [x] 3. 实现规则引擎核心（纯函数，优先属性测试）
  - [x] 3.1 实现名称匹配 Match（正则 full match 与区分大小写精确相等）
    - 实现 `Match(pattern, isRegex, originalName)`：正则模式编译并做完整匹配（full match），非正则做区分大小写精确相等
    - 缓存编译后的正则，捕获非法正则错误
    - _Requirements: 8.7, 8.8, 9.5, 9.6, 13.5, 13.6_

  - [x] 3.2 编写名称匹配语义属性测试
    - **Property 4: 名称匹配语义一致**
    - **Validates: Requirements 8.7, 8.8, 9.5, 9.6, 13.5, 13.6**

  - [x] 3.3 实现规则校验 ValidateAlias / ValidateFilter
    - 校验：正则模式合法性、模式长度 1-200、别名目标名称(1-100)/目标描述(≤1024)至少一项、屏蔽规则数量上限 100
    - 校验失败返回字段级 `VALIDATION` 错误且不持久化
    - _Requirements: 8.9, 9.7, 9.8, 9.2, 9.9, 13.2, 13.3, 13.4_

  - [x] 3.4 编写规则校验属性测试
    - **Property 12: 规则校验拒绝非法规则**
    - **Validates: Requirements 8.9, 9.7, 9.8, 13.4**

  - [x] 3.5 编写规则数量上限属性测试
    - **Property 13: 规则数量上限**
    - **Validates: Requirements 9.2, 9.9, 13.2, 13.3**

  - [x] 3.6 实现 ApplyFilters（屏蔽规则应用，停用规则忽略）
    - 对每个工具的 `OriginalName` 应用启用的屏蔽规则，命中即排除；停用规则在匹配中忽略
    - _Requirements: 9.3, 9.4, 9.11, 13.7, 13.8_

  - [x] 3.7 编写屏蔽匹配与启停即时性属性测试
    - **Property 5: 屏蔽规则匹配与启停即时性**
    - **Validates: Requirements 9.3, 9.4, 9.11, 13.8**

  - [x] 3.8 实现 ApplyAliases（多规则按序仅应用首条匹配）
    - 对每个保留工具，按 `sort_order` 找第一条匹配的别名规则应用目标名称/描述，其余忽略；规则无匹配工具时保留规则不报错
    - _Requirements: 8.2, 8.3, 8.4, 8.5_

  - [x] 3.9 编写多别名规则仅应用首条属性测试
    - **Property 6: 多别名规则仅应用首条**
    - **Validates: Requirements 8.2, 8.3, 8.5**

- [x] 4. 实现聚合管线（确定性纯函数流水线）
  - [x] 4.1 实现聚合管线编排 BuildToolSet（读缓存→排序合并→MCP 屏蔽→别名重写→去重→API Key 过滤）
    - 仅取启用上游、按 `upstream.sort_order` 合并；固定执行顺序：先屏蔽后重写；同名去重时排序在后者追加可区分后缀，保证对外名称全局唯一
    - 维护 `Name → (UpstreamID, OriginalName)` 反向映射；当无启用上游或全部被屏蔽时返回空集合而非错误
    - _Requirements: 3.3, 3.4, 3.6, 8.6, 10.1, 10.2, 10.7, 13.7_

  - [x] 4.2 编写聚合名称全局唯一属性测试
    - **Property 1: 聚合工具名称全局唯一**
    - **Validates: Requirements 3.6, 8.6**

  - [x] 4.3 编写上游排序保持属性测试
    - **Property 2: 上游排序在聚合中保持**
    - **Validates: Requirements 3.4, 10.1**

  - [x] 4.4 编写管线顺序（屏蔽先于重写）属性测试
    - **Property 7: 管线顺序——屏蔽先于重写**
    - **Validates: Requirements 10.2**

  - [x] 4.5 编写 API Key 可见集合子集属性测试
    - **Property 8: API Key 级可见集合为全局集合子集**
    - **Validates: Requirements 13.7**

  - [x] 4.6 实现 InvokeTool 路由与别名反向映射（不含真实上游调用，先用接口占位）
    - 校验工具是否在可见集合内，否则返回 `TOOL_NOT_FOUND` 且不转发；命中则反向映射回 (上游, 原始名) 并以原始参数透传
    - _Requirements: 10.3, 10.4, 10.6, 11.7_

  - [x] 4.7 编写别名反向映射可逆属性测试
    - **Property 9: 别名反向映射可逆**
    - **Validates: Requirements 10.6**

  - [x] 4.8 编写不可见工具调用必被拒属性测试
    - **Property 10: 不可见工具调用必被拒**
    - **Validates: Requirements 10.4, 11.7**

- [x] 5. Checkpoint - 确保领域核心测试通过
  - 运行规则引擎与聚合管线的全部属性测试与单元测试，确保通过；如有疑问请询问用户。

- [x] 6. 实现凭证存储与 JWT 密钥管理
  - [x] 6.1 实现上游凭证明文存储与编辑回显
    - 上游 MCP 凭证作为用户自部署配置明文存储，编辑时由管理 API 回显，保存时随配置整体覆盖
    - _Requirements: 19.1, 19.2, 19.4_

  - [x] 6.2 实现 JWT 签名密钥首次生成并写回配置
    - 首启时若 `config.yaml` 中 `jwt_secret` 为空，则生成随机密钥并写回，避免所有部署共享写死密钥
    - **Validates: Requirements 19.1, 19.2**

- [x] 7. 实现工具缓存（Tool_Cache，Redis 热路径 + PG 持久层）
  - [x] 7.1 实现 Get / Replace / Delete（整列表替换语义）
    - Redis 键 `mpg:tools:{upstreamID}` 存 JSON 工具列表 + updatedAt，PG `tool_cache` 持久兜底；Replace 为整列表替换，Delete 级联清理
    - _Requirements: 6.1, 6.2, 6.6_

  - [x] 7.2 编写工具缓存整列表替换往返属性测试
    - **Property 16: 工具缓存整列表替换往返**
    - **Validates: Requirements 6.1, 6.2**

- [x] 8. 实现传输适配层（Transport_Adapter）
  - [x] 8.1 实现 TransportFactory 与连接参数校验
    - 按传输类型（stdio/SSE/Streamable-HTTP/WebSocket）校验必填连接参数齐备与格式合法；不支持类型或参数缺失/非法返回字段级校验错误且不建立连接
    - _Requirements: 4.5, 4.6, 4.8_

  - [x] 8.2 实现统一 UpstreamSession 抽象（Connect/ListTools/CallTool/Close）
    - 定义会话生命周期语义：initialize 握手受 `connect_timeout_s` 约束、`ListTools`、`CallTool`（原始参数透传）、`Close`；携带鉴权凭证
    - _Requirements: 4.7, 4.9_

  - [x] 8.3 实现 stdio 传输会话
    - 以子进程方式启动上游 MCP，基于 MCP Go SDK 完成 initialize、tools/list、tools/call
    - _Requirements: 4.1_

  - [x] 8.4 实现 SSE 传输会话
    - 基于 MCP Go SDK SSE client transport 完成连接、初始化与工具收发
    - _Requirements: 4.2_

  - [x] 8.5 实现 Streamable-HTTP 传输会话
    - 基于 MCP Go SDK Streamable-HTTP client transport 完成连接、初始化与工具收发
    - _Requirements: 4.3_

  - [x] 8.6 实现 WebSocket 传输会话
    - 基于 WS client transport 完成连接、初始化与工具收发
    - _Requirements: 4.4_

  - [x] 8.7 编写传输连接参数校验属性测试
    - **Property 30: 传输连接参数校验**
    - **Validates: Requirements 4.5, 4.6, 4.8**

  - [x] 8.8 编写四种传输类型连接与初始化集成测试
    - 针对 mock/本地上游各 1-3 例，验证连接建立、初始化、tools/list 与 tools/call、连接超时标记失败
    - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.9_

- [x] 9. 实现连接管理器（MCP_Manager）与重试退避状态机
  - [x] 9.1 实现上游 MCP 的 CRUD、名称唯一与字段校验
    - Create/Update/Delete/List：名称长度 1-100、必填字段与格式校验、名称唯一冲突返回 `CONFLICT`、不存在返回 `NOT_FOUND`；删除时关闭连接并级联清理工具缓存与规则；列表返回各服务及当前连接状态，无数据返回空列表
    - 上游 MCP 凭证明文写库并在响应中回显，编辑保存时随配置整体覆盖
    - _Requirements: 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7, 2.8, 19.1, 19.3_

  - [x] 9.2 编写上游 MCP CRUD 校验与冲突单元测试
    - 验证名称越界、缺字段、名称冲突、资源不存在、空列表等具体场景
    - _Requirements: 2.2, 2.6, 2.7, 2.8_

  - [x] 9.3 实现启用/停用与排序（Reorder 完整性校验）
    - SetEnabled 标记启停；Reorder 校验提交排序是否为已注册标识的恰好一次排列（拒绝未注册/缺失/重复），合法才持久化，非法保持原排序并返回错误
    - _Requirements: 3.1, 3.2, 3.4, 3.5_

  - [x] 9.4 编写排序请求完整性校验属性测试
    - **Property 3: 排序请求完整性校验**
    - **Validates: Requirements 3.5**

  - [x] 9.5 实现连接生命周期状态机与指数退避重试
    - 状态：connecting/available/unavailable/suspended；退避 `min(initial × 2^n, max)`；运行期断开/建立失败计失败数并退避重试；连续失败达阈值转 suspended 并记录告警暂停自动重试；重连成功重置失败计数恢复可用；GetState 返回状态、最近失败原因与当前生效退避上限；Reconnect 支持管理员手动重连
    - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.5, 5.6_

  - [x] 9.6 编写指数退避不超过上限且非递减属性测试
    - **Property 14: 指数退避不超过上限且非递减**
    - **Validates: Requirements 5.1, 5.2, 5.3**

  - [x] 9.7 编写重连成功重置失败计数属性测试
    - **Property 15: 重连成功重置失败计数**
    - **Validates: Requirements 5.5**

- [x] 10. 实现工具同步服务（Sync_Service）与 cron 调度
  - [x] 10.1 实现 cron 表达式校验与配置持久化、动态重载
    - 用 `robfig/cron/v3` 校验标准 cron 格式与字段范围，校验通过才持久化；更新后取消旧调度未触发任务并不重启即按新周期生效
    - _Requirements: 7.3, 7.4, 7.6, 7.7_

  - [x] 10.2 编写 cron 表达式校验属性测试
    - **Property 18: cron 表达式校验**
    - **Validates: Requirements 7.3, 7.4**

  - [x] 10.3 实现周期同步、并发去重、超时与失败降级
    - 仅对启用且开启自动同步的上游按 cron 触发拉取；用进程内 `sync.Map` 去重，上次未完成则跳过本次触发；同步超时（默认 30s，5-300s）；失败/超时保留旧缓存并记录失败事件；缓存缺失触发一次拉取；成功则整列表替换缓存
    - _Requirements: 6.3, 7.1, 7.2, 7.5, 7.8_

  - [x] 10.4 编写同步并发去重属性测试
    - **Property 19: 同步并发去重**
    - **Validates: Requirements 7.8**

  - [x] 10.5 实现手动刷新工具列表
    - 走相同拉取逻辑：成功立即整列表替换缓存、不等下一周期；失败保留旧缓存并返回刷新失败错误
    - _Requirements: 6.4, 6.5_

- [x] 11. 实现聚合调用与上游路由接线
  - [x] 11.1 将 InvokeTool 接入真实上游会话与超时控制
    - 连接不可用返回 `UPSTREAM_UNAVAILABLE` 且不转发；上游调用超时（默认 30s，1-600s）中止且不返回部分结果返回 `UPSTREAM_TIMEOUT`；成功或上游错误结果原样透传
    - _Requirements: 10.3, 10.5, 10.8_

  - [x] 11.2 编写聚合调用路由集成测试
    - 验证不可用上游、超时、原样透传成功/错误结果
    - _Requirements: 10.3, 10.5, 10.8_

- [x] 12. Checkpoint - 确保连接、同步与聚合调用测试通过
  - 运行传输、缓存、加密、连接管理、同步与聚合调用相关测试，确保通过；如有疑问请询问用户。

- [x] 13. 实现对外 MCP API 服务（MCP_API_Service）
  - [x] 13.1 实现全量工具模式
    - 以 MCP 协议一次性暴露全部聚合工具定义
    - _Requirements: 11.1, 11.2_

  - [x] 13.2 实现智能模式网关工具 list_tools / search_tools / get_tool / call_tool
    - 智能模式仅暴露四个网关工具；`list_tools` 分页返回名称+简述；`search_tools` 按关键字过滤（默认 50，范围 1-200），无匹配返回空列表；`get_tool` 返回单工具完整定义（含 inputSchema），不可见工具返回工具不存在；`call_tool` 路由到具体聚合工具，不可见返回工具不存在且不发起调用
    - 均基于当前可见聚合工具集合（已过完整管线，含 API Key 级过滤）
    - _Requirements: 11.3, 11.4, 11.5, 11.6, 11.7_

  - [x] 13.3 编写智能模式工具发现与获取属性测试
    - **Property 11: 智能模式工具发现与获取结果正确**
    - **Validates: Requirements 11.4, 11.5, 11.7**

  - [x] 13.4 实现多传输对外端点（SSE / Streamable-HTTP / WebSocket）
    - 在 `/mcp/sse`、`/mcp/http`、`/mcp/ws` 复用 MCP SDK server transport 暴露同一聚合能力；调用前先经 API Key 校验
    - _Requirements: 11.8, 11.9_

  - [x] 13.5 编写对外三种传输端到端集成测试
    - SSE/HTTP/WS 各 1-2 例验证工具列表与调用
    - _Requirements: 11.8_

- [x] 14. 实现 API Key 管理、访问控制与限流
  - [x] 14.1 实现 API Key 生命周期管理
    - 创建（名称 1-100、全局唯一、初始启用、明文仅创建响应返回一次、仅存哈希+前缀）、删除、停用、过期失效、列表（仅元数据不含明文）；不存在返回错误、名称越界返回校验错误、空列表
    - _Requirements: 12.1, 12.2, 12.3, 12.4, 12.6, 12.7, 12.8, 12.9_

  - [x] 14.2 编写 API Key 生命周期单元测试
    - 验证唯一性、停用/过期后拒绝、名称越界、列表不含明文
    - _Requirements: 12.1, 12.3, 12.8_

  - [x] 14.3 实现 API Key 级屏蔽规则管理与上限校验
    - 创建/启停 API Key 屏蔽规则，模式 1-200、正则合法性、单 Key 上限 100；接入聚合管线第 6 阶段在 API Key 视角过滤
    - _Requirements: 13.1, 13.2, 13.3, 13.4, 13.8_

  - [x] 14.4 实现 API Key 鉴权中间件（存在/启用/未过期）
    - 提取 API Key 校验有效性，任一不通过返回鉴权失败且不路由到聚合能力
    - _Requirements: 11.9, 12.5_

  - [x] 14.5 实现来源白名单（IP/CIDR）访问控制
    - 配置 ACL 后仅允许来源地址命中白名单的请求使用该 Key，否则返回 `FORBIDDEN`
    - _Requirements: 13.9, 13.10_

  - [x] 14.6 编写来源白名单匹配属性测试
    - **Property 21: 来源白名单匹配**
    - **Validates: Requirements 13.9, 13.10**

  - [x] 14.7 实现限流中间件（Redis 固定窗口计数）
    - 配置速率上限的 Key 在每窗口内受理数不超上限、超额返回 `RATE_LIMITED`、下一窗口恢复；未配置上限不限流
    - _Requirements: 21.1, 21.2, 21.3, 21.4_

  - [x] 14.8 编写限流不超额且窗口恢复属性测试
    - **Property 20: 限流不超额且窗口恢复**
    - **Validates: Requirements 21.1, 21.2, 21.3, 21.4**

- [x] 15. 实现认证服务（Auth_Service）
  - [x] 15.1 实现首次初始化、注册、登录、会话与改密
    - 首次无管理员进入初始化提供注册入口；注册校验用户名 3-32、密码 6-128，bcrypt 加盐哈希写入 YAML，已存在则拒绝保持单用户；登录匹配则签发有效期=会话超时（默认 3600s，300-86400s）的 JWT，不匹配拒绝；改密校验当前密码与新密码长度后更新哈希
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 1.8, 1.9, 1.10_

  - [x] 15.2 编写管理员凭证哈希往返校验属性测试
    - **Property 28: 管理员凭证哈希往返校验**
    - **Validates: Requirements 1.2, 1.5, 1.9**

  - [x] 15.3 实现管理员 JWT 鉴权中间件与令牌过期
    - 校验 `Authorization: Bearer` 签名与过期；缺失/无效/过期返回 `UNAUTHORIZED`；超过会话超时使令牌失效
    - _Requirements: 1.6, 1.7_

- [x] 16. Checkpoint - 确保对外服务、鉴权与限流测试通过
  - 运行 MCP API、API Key、限流、访问控制与认证相关测试，确保通过；如有疑问请询问用户。

- [x] 17. 实现统计服务（Statistics_Service）
  - [x] 17.1 实现异步统计写入与降级
    - 工具调用完成 `LPUSH mpg:stats:buffer`（主流程附加耗时 <50ms），后台 worker 批量落 `call_stat`；写入失败静默丢弃不影响主流程；接入聚合调用路径
    - _Requirements: 16.1, 16.8, 16.9_

  - [x] 17.2 实现多维度统计与排行查询
    - 按上游 MCP / API Key 维度统计区间内调用条数（含成功失败）；按工具维度降序排行（默认 10，范围 1-100，基于稳定标识 `(upstream_id, original_name)`）；闭区间时间过滤；开始晚于结束返回范围无效错误；无记录返回空结果
    - _Requirements: 16.2, 16.3, 16.4, 16.5, 16.6, 16.7_

  - [x] 17.3 实现统计保留期清理
    - 按保留期（默认 90 天，1-3650）定时清理超期时间分区
    - _Requirements: 16.10_

  - [x] 17.4 编写统计时间范围闭区间过滤属性测试
    - **Property 22: 统计时间范围闭区间过滤**
    - **Validates: Requirements 16.5, 16.7**

  - [x] 17.5 编写工具排行降序且条数受限属性测试
    - **Property 23: 工具排行降序且条数受限**
    - **Validates: Requirements 16.3**

- [x] 18. 实现审计日志服务（Audit_Service）
  - [x] 18.1 实现审计事件记录与保留期清理
    - 记录登录（含结果）、上游/规则/API Key 的增改删（类型+目标+时间戳）、鉴权失败被拒访问；按保留期（默认 180 天，1-3650）清理；接入认证与管理操作路径
    - _Requirements: 22.1, 22.2, 22.3, 22.5_

  - [x] 18.2 实现审计日志分页查询（倒序）
    - 按发生时间倒序分页返回，默认每页 20、范围 1-200
    - _Requirements: 22.4_

  - [x] 18.3 编写审计日志倒序分页属性测试
    - **Property 24: 审计日志倒序分页**
    - **Validates: Requirements 22.4**

- [x] 19. 实现管理 REST API（/api/admin/*）
  - [x] 19.1 实现上游 MCP、规则、API Key 管理路由
    - 在 JWT 中间件下暴露上游 MCP CRUD/启停/排序/手动刷新、别名与屏蔽规则、API Key 与其规则/ACL/限流配置的管理端点，串接对应应用服务
    - _Requirements: 2.1, 3.1, 8.1, 9.1, 12.1, 13.1, 17.5_

  - [x] 19.2 实现系统设置、统计与审计查询路由
    - 暴露认证（注册/登录/改密）、系统设置（同步 cron、各超时、模式等配置读写）、统计排行查询、审计查询端点
    - _Requirements: 7.3, 16.2, 17.5, 22.4_

  - [x] 19.3 实现统一错误模型与校验错误响应
    - 用 `APIError`（code/message/fields）统一返回，校验错误标识每个无效字段
    - _Requirements: 2.2, 4.8, 14.10_

- [x] 20. 实现快捷模板市场（Template_Market）
  - [x] 20.1 实现内置模板集合、分类组织与检索
    - 内置模板含标识/名称/分类/简介/文档链接/传输类型/预设连接参数/占位参数定义；按分类组织（搜索/开发工具/数据库与存储/文件与系统/AI 与模型/办公与协作/自动化/其他）；按分类筛选与关键字检索（名称或简介命中），无匹配返回空列表；模板详情返回；无模板返回空列表
    - _Requirements: 14.1, 14.2, 14.3, 14.4, 14.5, 14.6, 14.13_

  - [x] 20.2 编写模板关键字检索命中属性测试
    - **Property 25: 模板关键字检索命中**
    - **Validates: Requirements 14.4, 14.5**

  - [x] 20.3 实现基于模板的上游创建（预填充与占位参数校验）
    - 选择模板预填充表单、标记待输入占位参数；要求服务地址+API Key 的模板在填写前禁止创建；服务地址非法 URL 拒绝并保留其他参数；缺失必填占位参数拒绝并指明参数名；完整参数按需求 2/4 字段校验后生成上游配置并持久化
    - _Requirements: 14.7, 14.8, 14.9, 14.10, 14.11, 14.12_

  - [x] 20.4 编写模板必填占位参数校验属性测试
    - **Property 26: 模板必填占位参数校验**
    - **Validates: Requirements 14.10**

- [x] 21. 实现小智接入服务（XiaoZhi_Connector）
  - [x] 21.1 实现小智 WS 接入点连接、工具暴露与调用路由
    - 配置接入点地址并启用时 WS 连接；接入点请求工具列表时返回聚合服务输出的当前可见集合；调用时路由到聚合服务并原样返回成功/错误结果；停用时关闭连接并停止重连
    - _Requirements: 15.1, 15.2, 15.3, 15.5_

  - [x] 21.2 实现小智地址校验与指数退避重连
    - 地址校验仅接受 ws:// 或 wss:// 合法 WebSocket URL，非法拒绝保持原配置；连接失败/断开按指数退避重连（初始 1s/1-60s、倍数 2、上限默认 60s/1-3600s）
    - _Requirements: 15.4, 15.6_

  - [x] 21.3 编写小智接入点地址协议校验属性测试
    - **Property 29: 小智接入点地址协议校验**
    - **Validates: Requirements 15.6**

  - [x] 21.4 编写小智 WS 连接与重连集成测试
    - 针对 mock 接入点验证连接、工具列表、调用与断线重连
    - _Requirements: 15.1, 15.3, 15.4_

- [x] 22. 实现健康检查服务（Health_Service）与启动连通性日志
  - [x] 22.1 实现启动连通性探测与结构化日志
    - 启动按序探测并用 `slog` 记录 PG、Redis、各启用上游 MCP、（若启用）小智接入点的连通性结果与失败原因
    - _Requirements: 20.1, 20.2, 20.3, 20.4, 20.5_

  - [x] 22.2 实现公开存活探针与鉴权详细健康端点
    - `/healthz` 仅返回自身存活；`/api/admin/health` 经管理员鉴权返回各依赖与上游/小智连接明细，未鉴权返回 401
    - _Requirements: 20.6, 20.7, 20.8_

  - [x] 22.3 编写健康端点鉴权与公开探针集成测试
    - 验证公开探针不泄露明细、详细端点鉴权与 401
    - _Requirements: 20.6, 20.7, 20.8_

- [x] 23. Checkpoint - 确保管理 API、统计审计、模板与小智测试通过
  - 运行管理 REST API、统计、审计、模板市场、小智接入与健康检查相关测试，确保通过；如有疑问请询问用户。

- [x] 24. 实现配置导入导出与备份
  - [x] 24.1 实现配置导出与导入校验应用
    - 导出当前配置（YAML 常规配置 + PG 业务配置：上游/规则/API Key 元数据）为可导入备份文件；导入校验格式与内容后应用；格式非法或校验失败拒绝并返回备份无效错误
    - _Requirements: 23.4, 23.5, 23.6_

  - [x] 24.2 编写配置导入导出往返属性测试
    - **Property 27: 配置导入导出往返**
    - **Validates: Requirements 23.4, 23.5, 23.6**

- [x] 25. 迁移前端至 TailAdmin 模板并搭建响应式基础
  - [x] 25.1 将前端迁移到 TailAdmin Vue 模板
    - 将现有 `web/` 目录改名留存为备份（如 `web-old/` 或 `web.bak/`）；克隆 https://github.com/TailAdmin/vue-tailwind-admin-dashboard 作为新的 `web/`
    - 清理模板自带的演示/示例页面与无关内容（demo 路由、示例视图、占位文案等），仅保留基础布局与所需基础设施
    - 克隆后、实现页面前立即将模板核心依赖升级到最新且兼容的稳定版本——尤其是 Vue 3.x、Vite、TypeScript，以及 vue-router、pinia、@vitejs/plugin-vue、vue-tsc、ESLint、Prettier（含 prettier-plugin-tailwindcss）、Tailwind CSS 等核心工具链；锁定升级后的版本到 `package.json`；通过 `npm install`、类型检查、lint 与生产构建验证升级无破坏性问题（趁业务代码尚未铺开尽早发现并修复）
    - 验证升级后的模板可正常通过类型检查、lint 与生产构建（`npm install && npm run build`，含 type-check 与 `npm run lint` 均通过）
    - 彻底移除 Ant Design Vue 及其相关依赖与引用；样式统一以 Tailwind CSS 工具类编写并遵循 TailAdmin 视觉风格
    - 集成 Prettier（启用 `prettier-plugin-tailwindcss` 对工具类一致排序）与 ESLint，确认两者规则互不冲突，在 `package.json` 提供 `lint`（校验，发现问题非零退出码）与 `format`（格式化）脚本
    - _Requirements: 17.1, 17.8, 25.1, 25.3, 25.4, 25.5, 25.7_

  - [x] 25.2 重建五档响应式基础与大屏最大宽度约束
    - 基于 Tailwind 响应式工具类与 TailAdmin 布局组件重建五档断点响应式基础（手机<768 / 平板 768-1023 / PC 1024-1439 / 宽屏 1440-2559 / 4K≥2560）
    - 移植/重建断点常量与「当前断点」composable，与 Tailwind 配置中的断点（`sm`/`md`/`lg`/`xl`/`2xl` 及自定义超大屏断点）对齐，避免各页面硬编码
    - 主内容容器在 ≥1440 用 Tailwind `max-w-*` + `mx-auto` 设最大宽度并居中；侧边栏/表格列/表单列数/分页条数随断点切换，保证无内容溢出
    - _Requirements: 17.3, 17.7_

  - [x] 25.3 在 TailAdmin 模板上重新实现登录页、路由守卫与 401 拦截
    - 将既有会话 store 与 Axios 拦截器逻辑移植到模板结构中，以 Tailwind/TailAdmin 风格重写登录页
    - 未认证访问受保护页面重定向登录；Axios 拦截器注入 JWT，遇鉴权失败/令牌失效清本地会话并重定向登录
    - _Requirements: 17.4, 17.6_

- [x] 26. 实现前端各管理页面
  - [x] 26.1 实现上游 MCP 管理页（含模板市场接入向导）
    - 以 Tailwind CSS 工具类、遵循 TailAdmin 组件与风格（表格、表单、抽屉、徽章、分页）实现：列表/启停/排序/增删改、连接状态展示（状态徽章）、手动刷新；模板市场分类浏览/检索/详情、选择模板预填充表单与占位参数填写
    - _Requirements: 14.3, 14.6, 14.7, 17.5_

  - [x] 26.2 实现规则管理页（别名/描述重写、屏蔽过滤）
    - 以 Tailwind CSS 工具类、遵循 TailAdmin 组件与风格（卡片、表单、模态框、开关）实现独立别名规则与 MCP 级屏蔽规则的增删改、启停、排序，以及全部上游/多上游作用范围配置；小屏单列，平板/PC/2K/4K 通过响应式卡片网格提升信息密度，避免使用表格
    - _Requirements: 8.1, 9.1, 17.5_

  - [x] 26.3 实现 API Key 管理页
    - 以 Tailwind CSS 工具类、遵循 TailAdmin 组件与风格（表格、表单、模态框、徽章）实现：API Key 增删/启停、明文创建时一次性展示、屏蔽规则/ACL/限流/有效期配置
    - _Requirements: 12.1, 13.1, 17.5_

  - [x] 26.4 实现系统设置页
    - 以 Tailwind CSS 工具类、遵循 TailAdmin 表单组件风格实现：同步 cron、各超时、对外模式（smart/full）、保留期、小智接入、改密等配置编辑
    - _Requirements: 7.3, 15.1, 17.5_

  - [x] 26.5 实现统计排行页与审计日志页
    - 以 Tailwind CSS 工具类、遵循 TailAdmin 风格实现：统计多维度查询与排行展示（图表复用模板内置 ApexCharts，大屏多卡片网格并排）、审计日志倒序分页查询（表格 + 分页）
    - _Requirements: 16.2, 16.3, 22.4, 17.5_

- [x] 27. 实现静态资源服务与前端内嵌装配
  - [x] 27.1 实现 Static_Server 内嵌 SPA 与 fallback 路由
    - 通过 `//go:embed dist/*` 内嵌前端构建产物，由 Go 进程提供静态资源；非 API/非文件路径 fallback 到 `index.html` 支持客户端路由
    - _Requirements: 17.1, 17.2_

  - [x] 27.2 装配主程序与路由分面（cmd/gateway 接线所有组件）
    - 在 `cmd/gateway` 装配 Config_Manager→DB/Redis/迁移→各应用服务→领域核心→入站路由（静态/管理 API/对外 MCP/healthz），分离 JWT 与 API Key 两套中间件链；启动时先连通性探测再对外服务
    - _Requirements: 11.8, 17.1, 18.1, 20.1_

  - [x] 27.3 编写 SPA fallback 与路由分面集成测试
    - 验证客户端路由 fallback、两 API 面中间件隔离
    - _Requirements: 17.2_

- [x] 28. 实现容器化构建与 CI/CD
  - [x] 28.1 编写多阶段 Dockerfile
    - 阶段一 node 构建前端、阶段二 golang embed dist 编译、阶段三 distroless 运行镜像，声明 `/data` 卷与环境变量
    - _Requirements: 24.1, 24.2, 23.1_

  - [x] 28.2 编写 GitHub Action 发布流水线
    - tag 推送触发构建单一镜像并推送远程仓库，任一步骤失败标记工作流失败并记录原因
    - 在构建前加入前端 ESLint 校验（`npm run lint`）与后端 `go vet`/格式检查步骤，校验失败即中止流水线
    - _Requirements: 24.3, 24.4, 24.5, 25.5_

- [x] 29. 最终 Checkpoint - 确保全部测试通过
  - 运行后端全部属性/单元/集成测试与前端构建，确保通过；如有疑问请询问用户。

## Notes

- 标记 `*` 的子任务为可选测试任务（单元/属性/集成测试），可在追求 MVP 时跳过；顶层任务不带 `*`。
- 每个任务引用具体需求条款以保证可追溯性。
- 设计「Correctness Properties」中的 30 条属性均已映射到属性测试子任务（Property 1-30），每条属性一个测试、最少 100 次迭代，测试处添加注释 `// Feature: mcp-proxy-gateway, Property {编号}: {属性文本}`，并就近放置在对应实现任务之后以尽早发现错误。
- 不适合 PBT 的外部行为（四种上游传输、对外三种传输、小智 WS、健康端点、SPA fallback、容器与 CI）以集成/冒烟/示例测试覆盖。
- Checkpoint 任务用于增量验证，确保每个阶段产物在进入下一阶段前可用。
- 主程序装配（任务 27.2）把所有组件接线为可运行系统，确保无孤儿代码。

## Task Dependency Graph

下图按依赖关系将叶子子任务划分为可并行执行的「波次」（wave）。同一波次内的任务相互独立、可并行；波次 N 的任务必须在波次 0..N-1 全部完成后才能开始。写入同一文件的任务被刻意分到不同波次以避免冲突。Checkpoint 与顶层父任务不纳入图中。

要点：
- W0 搭建脚手架与核心类型（所有任务的根依赖）。
- 纯函数领域核心（规则引擎、聚合管线）及其属性测试集中在 W2-W7，便于先行验证不变量。
- 前端迁移至 TailAdmin 模板（25.x）从 W1 起与后端并行推进，页面（26.x）在 W11。
- 主程序装配（27.2，W13）在所有组件之后接线，确保无孤儿代码；CI（28.2）依赖 Dockerfile（28.1）。

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["1.2", "2.1", "25.1"] },
    { "id": 2, "tasks": ["1.3", "2.2", "3.1", "6.1", "8.1", "25.2", "25.3"] },
    { "id": 3, "tasks": ["2.3", "3.2", "3.3", "3.6", "3.8", "6.2", "8.2"] },
    { "id": 4, "tasks": ["2.4", "3.4", "3.5", "3.7", "3.9", "7.1", "8.3", "8.4", "8.5", "8.6", "8.7"] },
    { "id": 5, "tasks": ["4.1", "7.2", "8.8", "9.1"] },
    { "id": 6, "tasks": ["4.2", "4.3", "4.4", "4.5", "4.6", "9.2", "9.3", "9.5", "10.1", "14.1", "15.1"] },
    { "id": 7, "tasks": ["4.7", "4.8", "9.4", "9.6", "9.7", "10.2", "10.3", "10.5", "14.2", "14.3", "14.4", "14.5", "14.7", "15.2", "15.3"] },
    { "id": 8, "tasks": ["10.4", "11.1", "13.1", "13.2", "14.6", "14.8", "18.1", "20.1", "21.1", "22.1"] },
    { "id": 9, "tasks": ["11.2", "13.3", "13.4", "17.1", "18.2", "20.2", "20.3", "21.2", "22.2"] },
    { "id": 10, "tasks": ["13.5", "17.2", "17.3", "18.3", "19.1", "20.4", "21.3", "21.4", "22.3"] },
    { "id": 11, "tasks": ["17.4", "17.5", "19.2", "24.1", "26.1", "26.2", "26.3", "26.4", "26.5"] },
    { "id": 12, "tasks": ["19.3", "24.2", "27.1"] },
    { "id": 13, "tasks": ["27.2"] },
    { "id": 14, "tasks": ["27.3", "28.1"] },
    { "id": 15, "tasks": ["28.2"] }
  ]
}
```
