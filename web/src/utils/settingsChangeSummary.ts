import type { YAMLConfig } from '@/api/settings'

export interface SettingsChange {
  label: string
  before: string
  after: string
  impact: string
  requiresRestart: boolean
}

export interface SettingsDraftInput {
  adminAddr: string
  publicMCPAddr: string
  trustedProxyCIDRs: string[]
  exemptCIDRs: string[]
  commandAllowlist: string[]
  strictCommandAllowlist?: string[]
  strictPackageAllowlist?: string[]
  globalFileRoots?: string[]
  browseExtraRoots?: string[]
  extraSensitiveEnvPrefixes: string[]
  npmRegistry?: string
  pipIndexURL?: string
  uvIndexURL?: string
}

function ensureRuntime(config: YAMLConfig): void {
  if (config.runtime == null) {
    config.runtime = {
      stdio_enabled: true,
      command_allowlist: ['node', 'npx', 'npm', 'python', 'python3', 'uv', 'uvx', 'docker'],
      extra_sensitive_env_prefixes: [],
      process_hardening: true,
      default_stdio_security_mode: 'standard',
      strict_command_allowlist: ['node', 'npx', 'python', 'python3', 'uv', 'uvx'],
      strict_package_allowlist: [
        '@modelcontextprotocol/*',
        '@playwright/mcp',
        '@notionhq/notion-mcp-server',
        'firecrawl-mcp',
        'exa-mcp-server',
      ],
      global_file_roots: [],
      browse_extra_roots: [],
      strict_path_only_runtime: true,
      strict_network_default: 'allowlist',
      strict_allow_policy_only: true,
    }
  }
  if (!Array.isArray(config.runtime.command_allowlist)) {
    config.runtime.command_allowlist = []
  }
  if (!Array.isArray(config.runtime.extra_sensitive_env_prefixes)) {
    config.runtime.extra_sensitive_env_prefixes = []
  }
  if (config.runtime.process_hardening == null) {
    config.runtime.process_hardening = true
  }
  if (!config.runtime.default_stdio_security_mode) {
    config.runtime.default_stdio_security_mode = 'standard'
  }
  if (!Array.isArray(config.runtime.strict_command_allowlist)) {
    config.runtime.strict_command_allowlist = []
  }
  if (!Array.isArray(config.runtime.strict_package_allowlist)) {
    config.runtime.strict_package_allowlist = []
  }
  if (!Array.isArray(config.runtime.global_file_roots)) {
    config.runtime.global_file_roots = []
  }
  if (!Array.isArray(config.runtime.browse_extra_roots)) {
    config.runtime.browse_extra_roots = []
  }
  if (config.runtime.strict_path_only_runtime == null) {
    config.runtime.strict_path_only_runtime = true
  }
  if (!config.runtime.strict_network_default) {
    config.runtime.strict_network_default = 'allowlist'
  }
  if (config.runtime.strict_allow_policy_only == null) {
    config.runtime.strict_allow_policy_only = true
  }
  // 包仓库镜像字段缺省视为空（不覆盖子进程默认源）。
  if (config.runtime.npm_registry == null) {
    config.runtime.npm_registry = ''
  }
  if (config.runtime.pip_index_url == null) {
    config.runtime.pip_index_url = ''
  }
  if (config.runtime.uv_index_url == null) {
    config.runtime.uv_index_url = ''
  }
}

type ValueFormatter = (value: unknown) => string

const routingStrategyLabels: Record<string, string> = {
  smart_balance: '智能均衡',
  round_robin: '智能均衡',
  priority_fill: '稳定优先',
}

const securityModeLabels: Record<string, string> = {
  monitor: '仅记录',
  enforce: '自动封禁',
  off: '关闭',
}

export function cloneSettingsConfig(value: YAMLConfig): YAMLConfig {
  return JSON.parse(JSON.stringify(value)) as YAMLConfig
}

export function buildSettingsDraft(config: YAMLConfig, input: SettingsDraftInput): YAMLConfig {
  const draft = cloneSettingsConfig(config)
  ensureRuntime(draft)
  draft.server.admin_addr = input.adminAddr
  draft.server.public_mcp_addr = input.publicMCPAddr
  draft.security.trusted_proxy_cidrs = input.trustedProxyCIDRs
  draft.security.exempt_cidrs = input.exemptCIDRs
  draft.runtime!.command_allowlist = input.commandAllowlist
  draft.runtime!.extra_sensitive_env_prefixes = input.extraSensitiveEnvPrefixes
  if (input.strictCommandAllowlist) {
    draft.runtime!.strict_command_allowlist = input.strictCommandAllowlist
  }
  if (input.strictPackageAllowlist) {
    draft.runtime!.strict_package_allowlist = input.strictPackageAllowlist
  }
  if (input.globalFileRoots) {
    draft.runtime!.global_file_roots = input.globalFileRoots
  }
  if (input.browseExtraRoots) {
    draft.runtime!.browse_extra_roots = input.browseExtraRoots
  }
  // 包仓库镜像：仅当输入显式提供时覆盖（空串也是合法值，表示清除镜像）。
  if (input.npmRegistry !== undefined) {
    draft.runtime!.npm_registry = input.npmRegistry.trim()
  }
  if (input.pipIndexURL !== undefined) {
    draft.runtime!.pip_index_url = input.pipIndexURL.trim()
  }
  if (input.uvIndexURL !== undefined) {
    draft.runtime!.uv_index_url = input.uvIndexURL.trim()
  }
  return draft
}

export function collectSettingsChanges(before: YAMLConfig, after: YAMLConfig): SettingsChange[] {
  const changes: SettingsChange[] = []
  const needsRestart = true
  const runtimeOnly = false

  addChange(changes, '同步 cron', before.sync.cron, after.sync.cron, needsRestart)
  addChange(
    changes,
    '同步超时',
    before.sync.timeout_s,
    after.sync.timeout_s,
    needsRestart,
    secondsLabel,
  )
  addChange(
    changes,
    '连接建立超时',
    before.connection.connect_timeout_s,
    after.connection.connect_timeout_s,
    needsRestart,
    secondsLabel,
  )
  addChange(
    changes,
    '初始退避',
    before.connection.retry_initial_backoff_s,
    after.connection.retry_initial_backoff_s,
    needsRestart,
    secondsLabel,
  )
  addChange(
    changes,
    '退避倍数',
    before.connection.retry_multiplier,
    after.connection.retry_multiplier,
    needsRestart,
  )
  addChange(
    changes,
    '退避上限',
    before.connection.retry_max_backoff_s,
    after.connection.retry_max_backoff_s,
    needsRestart,
    secondsLabel,
  )
  addChange(
    changes,
    '连续失败阈值',
    before.connection.failure_threshold,
    after.connection.failure_threshold,
    needsRestart,
  )
  addChange(
    changes,
    '按需探测冷却',
    before.connection.demand_reconnect_cooldown_s,
    after.connection.demand_reconnect_cooldown_s,
    needsRestart,
    secondsLabel,
  )
  addChange(
    changes,
    '按需等待上限',
    before.connection.demand_reconnect_wait_s,
    after.connection.demand_reconnect_wait_s,
    needsRestart,
    secondsLabel,
  )
  addChange(
    changes,
    '上游调用超时',
    before.aggregation.upstream_call_timeout_s,
    after.aggregation.upstream_call_timeout_s,
    needsRestart,
    secondsLabel,
  )
  addChange(
    changes,
    '工具调用策略',
    before.aggregation.tool_routing_strategy,
    after.aggregation.tool_routing_strategy,
    runtimeOnly,
    routingStrategyLabel,
  )
  addChange(
    changes,
    '管理监听端口',
    before.server.admin_addr,
    after.server.admin_addr,
    needsRestart,
    emptyLabel,
  )
  addChange(
    changes,
    '独立 MCP 监听端口',
    before.server.public_mcp_addr,
    after.server.public_mcp_addr,
    needsRestart,
    emptyLabel,
  )
  addChange(
    changes,
    '管理端口同时暴露 MCP',
    before.server.expose_mcp_on_admin_addr,
    after.server.expose_mcp_on_admin_addr,
    needsRestart,
    boolLabel,
  )
  addChange(changes, '日志级别', before.server.log_level, after.server.log_level, runtimeOnly)
  addChange(
    changes,
    '智能模式默认返回工具数',
    before.mcp_api.smart_discovery_limit,
    after.mcp_api.smart_discovery_limit,
    runtimeOnly,
  )
  addChange(
    changes,
    'MCP 请求体上限',
    before.mcp_api.request_body_limit_mib,
    after.mcp_api.request_body_limit_mib,
    runtimeOnly,
    mibLabel,
  )
  addChange(
    changes,
    '安全防护模式',
    before.security.mode,
    after.security.mode,
    runtimeOnly,
    securityModeLabel,
  )
  addChange(
    changes,
    '失败统计窗口',
    before.security.failure_window_s,
    after.security.failure_window_s,
    runtimeOnly,
    secondsLabel,
  )
  addChange(
    changes,
    '单 IP 失败阈值',
    before.security.max_failures_per_ip,
    after.security.max_failures_per_ip,
    runtimeOnly,
  )
  addChange(
    changes,
    '疑似 Key 失败阈值',
    before.security.max_failures_per_key_fingerprint,
    after.security.max_failures_per_key_fingerprint,
    runtimeOnly,
  )
  addChange(
    changes,
    'ACL 拒绝阈值',
    before.security.max_acl_denies_per_key_ip,
    after.security.max_acl_denies_per_key_ip,
    runtimeOnly,
  )
  addChange(
    changes,
    '首次封禁时长',
    before.security.first_block_duration_s,
    after.security.first_block_duration_s,
    runtimeOnly,
    secondsLabel,
  )
  addChange(
    changes,
    '最长自动封禁',
    before.security.max_block_duration_s,
    after.security.max_block_duration_s,
    runtimeOnly,
    secondsLabel,
  )
  addChange(
    changes,
    '封禁升级窗口',
    before.security.escalation_window_s,
    after.security.escalation_window_s,
    runtimeOnly,
    secondsLabel,
  )
  addChange(
    changes,
    '可信代理 CIDR',
    before.security.trusted_proxy_cidrs ?? [],
    after.security.trusted_proxy_cidrs ?? [],
    needsRestart,
    cidrListLabel,
  )
  addChange(
    changes,
    '自动封禁豁免 CIDR',
    before.security.exempt_cidrs ?? [],
    after.security.exempt_cidrs ?? [],
    runtimeOnly,
    cidrListLabel,
  )
  addChange(
    changes,
    '本地 stdio',
    before.runtime?.stdio_enabled ?? true,
    after.runtime?.stdio_enabled ?? true,
    runtimeOnly,
    (v) => (v ? '启用' : '禁用'),
  )
  addChange(
    changes,
    'stdio 命令白名单',
    before.runtime?.command_allowlist ?? [],
    after.runtime?.command_allowlist ?? [],
    runtimeOnly,
    (v) => (Array.isArray(v) && v.length > 0 ? v.join('、') : '默认列表'),
  )
  addChange(
    changes,
    '额外敏感环境变量前缀',
    before.runtime?.extra_sensitive_env_prefixes ?? [],
    after.runtime?.extra_sensitive_env_prefixes ?? [],
    runtimeOnly,
    (v) => (Array.isArray(v) && v.length > 0 ? v.join('、') : '无'),
  )
  addChange(
    changes,
    'stdio 进程加固',
    before.runtime?.process_hardening ?? true,
    after.runtime?.process_hardening ?? true,
    runtimeOnly,
    (v) => (v ? '启用' : '关闭'),
  )
  addChange(
    changes,
    '默认本地安全档位',
    before.runtime?.default_stdio_security_mode ?? 'standard',
    after.runtime?.default_stdio_security_mode ?? 'standard',
    runtimeOnly,
    (v) => {
      const m = String(v)
      if (m === 'strict') return '严格安全'
      if (m === 'unrestricted') return '完全放行'
      return '标准'
    },
  )
  addChange(
    changes,
    '严格档命令白名单',
    before.runtime?.strict_command_allowlist ?? [],
    after.runtime?.strict_command_allowlist ?? [],
    runtimeOnly,
    (v) => (Array.isArray(v) && v.length > 0 ? v.join('、') : '默认列表'),
  )
  addChange(
    changes,
    '严格档包白名单',
    before.runtime?.strict_package_allowlist ?? [],
    after.runtime?.strict_package_allowlist ?? [],
    runtimeOnly,
    (v) => (Array.isArray(v) && v.length > 0 ? v.join('、') : '默认列表'),
  )
  addChange(
    changes,
    '全局文件允许路径',
    before.runtime?.global_file_roots ?? [],
    after.runtime?.global_file_roots ?? [],
    runtimeOnly,
    (v) => (Array.isArray(v) && v.length > 0 ? v.join('、') : '无'),
  )
  addChange(
    changes,
    '路径浏览额外根',
    before.runtime?.browse_extra_roots ?? [],
    after.runtime?.browse_extra_roots ?? [],
    runtimeOnly,
    (v) => (Array.isArray(v) && v.length > 0 ? v.join('、') : '无'),
  )
  addChange(
    changes,
    '严格档仅 runtime 路径',
    before.runtime?.strict_path_only_runtime ?? true,
    after.runtime?.strict_path_only_runtime ?? true,
    runtimeOnly,
    (v) => (v ? '启用' : '关闭'),
  )
  addChange(
    changes,
    'npm 包仓库镜像',
    before.runtime?.npm_registry ?? '',
    after.runtime?.npm_registry ?? '',
    runtimeOnly,
    (v) => (String(v).trim() ? String(v) : '未覆盖（用子进程默认）'),
  )
  addChange(
    changes,
    'pip 包仓库镜像',
    before.runtime?.pip_index_url ?? '',
    after.runtime?.pip_index_url ?? '',
    runtimeOnly,
    (v) => (String(v).trim() ? String(v) : '未覆盖（用子进程默认）'),
  )
  addChange(
    changes,
    'uv 包仓库镜像',
    before.runtime?.uv_index_url ?? '',
    after.runtime?.uv_index_url ?? '',
    runtimeOnly,
    (v) => (String(v).trim() ? String(v) : '未覆盖（用子进程默认）'),
  )
  addChange(
    changes,
    '调用记录保留天数',
    before.statistics.retention_days,
    after.statistics.retention_days,
    runtimeOnly,
    daysLabel,
  )
  addChange(
    changes,
    '工具排行默认条数',
    before.statistics.top_limit_default,
    after.statistics.top_limit_default,
    runtimeOnly,
  )
  addChange(
    changes,
    '操作日志保留天数',
    before.audit.retention_days,
    after.audit.retention_days,
    runtimeOnly,
    daysLabel,
  )
  addChange(
    changes,
    '审计分页默认每页条数',
    before.audit.page_size_default,
    after.audit.page_size_default,
    runtimeOnly,
  )
  addChange(
    changes,
    '会话超时',
    before.auth.session_timeout_s,
    after.auth.session_timeout_s,
    runtimeOnly,
    secondsLabel,
  )

  return changes
}

export function settingsChangesRequireRestart(changes: SettingsChange[]): boolean {
  return changes.some((item) => item.requiresRestart)
}

export function settingsConfirmMessage(changes: SettingsChange[]): string {
  const preview = changes
    .slice(0, 8)
    .map((item) => `• ${item.label}：${item.before} → ${item.after}（${item.impact}）`)
  const extra =
    changes.length > preview.length ? `\n• 另有 ${changes.length - preview.length} 项配置变更` : ''
  const tail = settingsChangesRequireRestart(changes)
    ? '保存后网关会自动重启以应用相关配置，重启期间管理台和对外 MCP 服务会短暂不可用。'
    : '这些变更会写入配置文件，并直接应用到当前运行中的服务。'
  return `本次将保存 ${changes.length} 项配置变更：\n${preview.join('\n')}${extra}\n\n${tail}`
}

function addChange(
  changes: SettingsChange[],
  label: string,
  before: unknown,
  after: unknown,
  requiresRestart: boolean,
  format: ValueFormatter = String,
): void {
  if (sameValue(before, after)) return
  changes.push({
    label,
    before: format(before),
    after: format(after),
    impact: requiresRestart ? '保存后重启生效' : '保存后立即生效',
    requiresRestart,
  })
}

function sameValue(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}

function boolLabel(value: unknown): string {
  return value ? '开启' : '关闭'
}

function emptyLabel(value: unknown): string {
  const text = String(value).trim()
  return text === '' ? '未配置' : text
}

function secondsLabel(value: unknown): string {
  return `${value} 秒`
}

function daysLabel(value: unknown): string {
  return `${value} 天`
}

function mibLabel(value: unknown): string {
  return `${value} MiB`
}

function securityModeLabel(value: unknown): string {
  const key = String(value)
  return securityModeLabels[key] ?? key
}

function routingStrategyLabel(value: unknown): string {
  const key = String(value)
  return routingStrategyLabels[key] ?? key
}

function cidrListLabel(value: unknown): string {
  const values = Array.isArray(value) ? value.map(String) : []
  if (values.length === 0) return '未配置'
  if (values.length <= 2) return values.join('、')
  return `${values.slice(0, 2).join('、')} 等 ${values.length} 项`
}
