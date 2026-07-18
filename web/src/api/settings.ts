/**
 * 系统设置与改密 API 封装（任务 26.4）
 *
 * 设计要点（对应 internal/httpapi/settings.go、auth.go 与 Req 7.3、15.1、17.5）：
 * - 系统设置端点挂载于管理前缀 `/api/admin/settings`，改密端点为受保护的
 *   `/api/admin/auth/change-password`；二者均复用全局 Axios 实例（`@/api/request`，
 *   baseURL=`/api/admin`），自动注入 JWT 并处理 401（Req 17.6）。
 * - 后端 GET /settings 返回 `{ settings: YAMLConfig }`，其中管理员凭证（用户名/哈希）
 *   与 JWT 签名密钥已被清空，仅保留 `admin.initialized` 标志；PUT /settings 接收完整
 *   YAMLConfig（沿用既有管理员凭证与 JWT 签名密钥，本端点不参与改密或密钥轮换）。请求/响应的 JSON 形状直接对应后端 YAMLConfig
 *   的 `yaml` 标签（snake_case 键名）。
 * - 改密走专用端点 POST /auth/change-password（相对 baseURL，即
 *   `/api/admin/auth/change-password`），请求体为 `{ currentPassword, newPassword }`。
 *
 * 后端真实路由（已实现，见 internal/httpapi/settings.go、auth.go）：
 *   GET  /settings                    读取常规配置快照（管理员凭证已清空）
 *   PUT  /settings                    校验并回写常规配置（cron 服务端专项校验）
 *   POST /auth/change-password        校验当前密码后改密（受 JWT 保护）
 */
import request, { ApiError } from '@/api/request'

/** 对外服务模式取值，与后端 config.ModeSmart / ModeFull 对齐。 */
export type MCPMode = 'smart' | 'full'

export type ToolRoutingStrategy = 'smart_balance' | 'priority_fill' | 'round_robin'

/** 日志级别取值，与后端 config.LogLevel* 对齐。 */
export type LogLevel = 'debug' | 'info' | 'warn' | 'error'

export type SecurityMode = 'off' | 'monitor' | 'enforce'

/** 服务监听配置（对应后端 ServerConfig）。 */
export interface ServerConfig {
  /** 管理台与管理 API 监听地址，默认 :8080。 */
  admin_addr: string
  /** 独立对外 MCP 服务监听地址；为空表示不单独监听。 */
  public_mcp_addr: string
  /** 是否仍在管理端口暴露 /mcp/*，默认 true 兼容旧部署。 */
  expose_mcp_on_admin_addr: boolean
  /** 进程日志级别：debug/info/warn/error，默认 info。保存设置后即时生效。 */
  log_level: LogLevel
}

/**
 * 管理员凭证配置（对应后端 AdminConfig）。
 *
 * 注意：GET /settings 返回时用户名与哈希被清空，仅 `initialized` 有意义；
 * PUT /settings 时本字段由后端沿用既有凭证覆盖，前端无需也不应提交真实凭证。
 */
export interface AdminConfig {
  /** 管理员用户名（读取时为空）。 */
  username: string
  /** 管理员密码 bcrypt 哈希（读取时为空）。 */
  password_hash: string
  /** 首次初始化标志。 */
  initialized: boolean
}

/** 认证会话配置（对应后端 AuthConfig）。 */
export interface AuthConfig {
  /** 会话超时秒数，范围 300-86400，默认 3600。 */
  session_timeout_s: number
}

/** 工具同步调度配置（对应后端 SyncConfig）。 */
export interface SyncConfig {
  /** 标准 cron 表达式，服务端校验通过后才持久化（Req 7.3）。 */
  cron: string
  /** 同步超时秒数，范围 5-300，默认 30。 */
  timeout_s: number
}

/** 上游连接与重试配置（对应后端 ConnectionConfig）。 */
export interface ConnectionConfig {
  /** 连接建立超时秒数，需为正整数，默认 30。 */
  connect_timeout_s: number
  /** 初始退避秒数，范围 1-60，默认 5。 */
  retry_initial_backoff_s: number
  /** 退避倍数，需大于等于 1，默认 5。 */
  retry_multiplier: number
  /** 退避上限秒数，范围 1-86400，默认 3600。 */
  retry_max_backoff_s: number
  /** 连续失败阈值，范围 1-100，默认 10。 */
  failure_threshold: number
}

/** 聚合调用配置（对应后端 AggregationConfig）。 */
export interface AggregationConfig {
  /** 上游调用超时秒数，范围 1-600，默认 30。 */
  upstream_call_timeout_s: number
  /** 同名工具有多个来源上游时的调用选择策略。 */
  tool_routing_strategy: ToolRoutingStrategy
}

/** 对外 MCP API 配置（对应后端 MCPAPIConfig）。 */
export interface MCPAPIConfig {
  /** 智能模式工具发现返回数，范围 1-200，默认 50。 */
  smart_discovery_limit: number
  /** 单次对外 MCP POST 请求体上限，单位 MiB，默认 8。 */
  request_body_limit_mib: number
}

/** 统计服务配置（对应后端 StatisticsConfig）。 */
export interface StatisticsConfig {
  /** 工具排行默认条数，范围 1-100，默认 10。 */
  top_limit_default: number
  /** 统计保留天数，范围 1-3650，默认 90。 */
  retention_days: number
}

/** 审计日志配置（对应后端 AuditConfig）。 */
export interface AuditConfig {
  /** 审计分页默认每页条数，范围 1-200，默认 20。 */
  page_size_default: number
  /** 审计保留天数，范围 1-3650，默认 180。 */
  retention_days: number
}

export interface SecurityConfig {
  mode: SecurityMode
  failure_window_s: number
  max_failures_per_ip: number
  max_failures_per_key_fingerprint: number
  max_acl_denies_per_key_ip: number
  first_block_duration_s: number
  max_block_duration_s: number
  escalation_window_s: number
  trusted_proxy_cidrs: string[]
  exempt_cidrs: string[]
}

/** 本地 stdio 运行时安全策略（对应后端 RuntimeConfig）。 */
export interface RuntimePolicyConfig {
  /** 是否允许创建/连接本地 stdio 上游，默认 true。 */
  stdio_enabled: boolean
  /** stdio 可执行文件白名单（基名）；空则服务端回填默认列表。 */
  command_allowlist: string[]
  /** 追加到内置敏感环境变量前缀（仅剥离父进程继承项）。 */
  extra_sensitive_env_prefixes: string[]
  /** 是否启用 stdio 进程加固（Linux 进程组/父亡杀子等），默认 true。 */
  process_hardening?: boolean
  /** 上游未声明时的默认本地运行安全档位。 */
  default_stdio_security_mode?: 'standard' | 'strict' | 'unrestricted' | string
  /** 严格档命令子集（与 command_allowlist 取交集）。 */
  strict_command_allowlist?: string[]
  /** 严格档允许 npx/uvx 执行的包/工具名（支持 @scope/*）。 */
  strict_package_allowlist?: string[]
  /** 全局默认文件允许路径。 */
  global_file_roots?: string[]
  /** 路径选择器额外可浏览根（仅扩大浏览，不改变 stdio 安全策略）。 */
  browse_extra_roots?: string[]
  /** 严格档是否仅从 runtime 卷解析命令。 */
  strict_path_only_runtime?: boolean
  /** 严格档默认网络策略。 */
  strict_network_default?: 'deny' | 'allowlist' | string
  /** 无内核隔离时是否允许严格档仅策略运行。 */
  strict_allow_policy_only?: boolean
}

/** 小智接入配置（对应后端 XiaoZhiConfig，Req 15）。 */
export interface XiaoZhiConfig {
  /** 是否启用小智接入。 */
  enabled: boolean
  /** 小智 MCP 接入点地址，启用时须为 ws:// 或 wss:// 合法 URL（Req 15.6）。 */
  endpoint: string
  /** 小智使用的 MCP 模式，smart 或 full，默认 full。 */
  mode: MCPMode
}

/**
 * 常规 YAML 配置（对应后端 config.YAMLConfig）。
 *
 * 键名与后端 `yaml` 标签一致（snake_case），便于 PUT 时原样回传。
 */
export interface YAMLConfig {
  server: ServerConfig
  admin: AdminConfig
  /** JWT 签名密钥；GET 时为空，PUT 时后端会强制沿用已保存值，前端不应修改。 */
  jwt_secret?: string
  auth: AuthConfig
  sync: SyncConfig
  connection: ConnectionConfig
  aggregation: AggregationConfig
  mcp_api: MCPAPIConfig
  statistics: StatisticsConfig
  audit: AuditConfig
  security: SecurityConfig
  /** 本地运行时策略；旧配置可能缺省，前端读写前需兜底。 */
  runtime?: RuntimePolicyConfig
  xiaozhi: XiaoZhiConfig
}

/** GET/PUT /settings 的响应体：{ settings: YAMLConfig }。 */
export interface SettingsRuntime {
  server: ServerConfig
}

export interface SettingsSnapshot {
  settings: YAMLConfig
  runtime: SettingsRuntime
}

export interface UpdateSettingsOptions {
  restart?: boolean
}

interface SettingsResponse {
  settings: YAMLConfig
  runtime?: SettingsRuntime
}

/**
 * 后端统一校验错误（VALIDATION）的形状。
 *
 * `fields` 的键为后端字段路径（如 `sync.cron`、`auth.session_timeout_s`），
 * 值为该字段的中文错误说明，便于前端按字段定位并展示（Req 7.3、18.6）。
 */
export interface APIErrorBody {
  code?: number
  message?: string
  fields?: Record<string, string>
}

/**
 * 从任意错误中提取后端统一错误体（含字段级说明）。
 *
 * 响应拦截器已把请求失败统一规整为 ApiError（含 code/message/fields），此处据此还原
 * 为 { code, message, fields } 视图，供设置页将服务端校验错误映射回对应表单字段；
 * 非 ApiError 时返回 null。
 */
export function extractAPIError(err: unknown): APIErrorBody | null {
  if (err instanceof ApiError) {
    return { code: err.code, message: err.message, fields: err.fields }
  }
  return null
}

/** 读取当前常规配置快照（Req 17.5、18.4）。 */
function normalizeSettingsSnapshot(data: SettingsResponse): SettingsSnapshot {
  return {
    settings: data.settings,
    runtime: data.runtime ?? { server: data.settings.server },
  }
}

export async function getSettingsSnapshot(): Promise<SettingsSnapshot> {
  const res = await request.get<SettingsResponse>('/settings')
  return normalizeSettingsSnapshot(res.data)
}

export async function getSettings(): Promise<YAMLConfig> {
  const res = await request.get<SettingsResponse>('/settings')
  return res.data.settings
}

/**
 * 校验并回写常规配置（Req 7.3、18.4）。
 *
 * cron 非法或字段越界时后端返回 VALIDATION（HTTP 400，含 fields），由调用方按字段展示；
 * 成功时返回回写后的配置快照（管理员凭证已清空）。
 */
export async function updateSettings(
  payload: YAMLConfig,
  options?: UpdateSettingsOptions,
): Promise<YAMLConfig> {
  const res = await request.put<SettingsResponse>('/settings', payload, {
    params: options?.restart ? { restart: 'true' } : undefined,
  })
  return res.data.settings
}

/** 改密请求体（对应后端 changePasswordRequest，Req 1.8、1.10）。 */
export interface ChangePasswordRequest {
  /** 当前密码，校验匹配后方可改密。 */
  currentPassword: string
  /** 新密码，长度需在 6 至 128 个字符之间。 */
  newPassword: string
}

/**
 * 校验当前密码后更新管理员密码（Req 1.8、1.10）。
 *
 * 当前密码不匹配返回 UNAUTHORIZED（401）；新密码长度越界返回 VALIDATION（400）。
 */
export async function changePassword(payload: ChangePasswordRequest): Promise<void> {
  await request.post('/auth/change-password', payload)
}
