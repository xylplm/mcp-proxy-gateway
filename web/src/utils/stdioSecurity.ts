/**
 * 本地 stdio 运行安全档位（与后端 securityProfile 对齐）。
 */

export type StdioSecurityMode = 'standard' | 'strict' | 'unrestricted'

export type FileAccessMode = 'inherit' | 'deny' | 'allowlist' | 'unrestricted'
export type NetworkAccessMode = 'inherit' | 'deny' | 'allowlist' | 'unrestricted'
export type DependencyPolicyMode = 'inherit' | 'declared_only' | 'catalog_only' | 'unrestricted'

export interface FileAccessPolicy {
  mode: FileAccessMode
  paths: string[]
}

export interface NetworkPolicy {
  mode: NetworkAccessMode
  hosts: string[]
}

export interface SecurityProfile {
  mode?: StdioSecurityMode | ''
  fileAccess?: FileAccessPolicy
  network?: NetworkPolicy
  dependencyPolicy?: DependencyPolicyMode | ''
  /** 严格档追加的 npx/uvx 包白名单（与全局并集；支持 @scope/*）。 */
  packageAllowlist?: string[]
  allowSelfInstall?: boolean
  note?: string
}

export const STDIO_SECURITY_MODES: ReadonlyArray<{
  value: StdioSecurityMode
  label: string
  short: string
  desc: string
}> = [
  {
    value: 'standard',
    label: '标准',
    short: '标准',
    desc: '兼容常用模板：命令白名单 + 环境清理 + 进程清理。',
  },
  {
    value: 'strict',
    label: '严格安全',
    short: '严格',
    desc: '收紧命令/工作区；npx·uvx 仅可跑包白名单内目标；禁止脚本自装包。',
  },
  {
    value: 'unrestricted',
    label: '完全放行',
    short: '放行',
    desc: '最大限度兼容自定义脚本；与网关同权限执行，风险极高。',
  },
]

export function normalizeSecurityMode(
  raw: unknown,
  fallback: StdioSecurityMode = 'standard',
): StdioSecurityMode {
  const v = String(raw ?? '')
    .toLowerCase()
    .trim()
  if (v === 'standard' || v === 'strict' || v === 'unrestricted') return v
  return fallback
}

export function normalizeSecurityProfile(raw: unknown): SecurityProfile {
  if (raw == null || typeof raw !== 'object' || Array.isArray(raw)) {
    return { mode: '' }
  }
  const obj = raw as Record<string, unknown>
  const modeRaw = String(obj.mode ?? '')
    .toLowerCase()
    .trim()
  const mode =
    modeRaw === 'standard' || modeRaw === 'strict' || modeRaw === 'unrestricted' ? modeRaw : ''

  const fileAccessRaw =
    obj.fileAccess != null && typeof obj.fileAccess === 'object' && !Array.isArray(obj.fileAccess)
      ? (obj.fileAccess as Record<string, unknown>)
      : {}
  const networkRaw =
    obj.network != null && typeof obj.network === 'object' && !Array.isArray(obj.network)
      ? (obj.network as Record<string, unknown>)
      : {}

  const paths = Array.isArray(fileAccessRaw.paths)
    ? fileAccessRaw.paths
        .filter((p): p is string => typeof p === 'string')
        .map((p) => p.trim())
        .filter(Boolean)
    : []
  const hosts = Array.isArray(networkRaw.hosts)
    ? networkRaw.hosts
        .filter((h): h is string => typeof h === 'string')
        .map((h) => h.trim())
        .filter(Boolean)
    : []

  const faMode = String(fileAccessRaw.mode ?? 'inherit').toLowerCase() as FileAccessMode
  const netMode = String(networkRaw.mode ?? 'inherit').toLowerCase() as NetworkAccessMode
  const dep = String(obj.dependencyPolicy ?? '').toLowerCase() as DependencyPolicyMode | ''

  const profile: SecurityProfile = {
    mode,
    fileAccess: {
      mode: ['inherit', 'deny', 'allowlist', 'unrestricted'].includes(faMode) ? faMode : 'inherit',
      paths,
    },
    network: {
      mode: ['inherit', 'deny', 'allowlist', 'unrestricted'].includes(netMode)
        ? netMode
        : 'inherit',
      hosts,
    },
  }
  if (
    dep === 'declared_only' ||
    dep === 'catalog_only' ||
    dep === 'unrestricted' ||
    dep === 'inherit'
  ) {
    profile.dependencyPolicy = dep
  }
  if (Array.isArray(obj.packageAllowlist)) {
    profile.packageAllowlist = obj.packageAllowlist
      .filter((p): p is string => typeof p === 'string')
      .map((p) => p.trim())
      .filter(Boolean)
  }
  if (typeof obj.allowSelfInstall === 'boolean') {
    profile.allowSelfInstall = obj.allowSelfInstall
  }
  if (typeof obj.note === 'string' && obj.note.trim() !== '') {
    profile.note = obj.note.trim().slice(0, 300)
  }
  return profile
}

export function securityModeLabel(mode: string | undefined | null): string {
  const m = normalizeSecurityMode(mode, 'standard')
  return STDIO_SECURITY_MODES.find((x) => x.value === m)?.label ?? m
}

export function securityModeBadgeClass(mode: string | undefined | null): string {
  const m = normalizeSecurityMode(mode, 'standard')
  switch (m) {
    case 'strict':
      return 'bg-success-50 text-success-700 ring-1 ring-success-200 dark:bg-success-500/10 dark:text-success-400 dark:ring-success-500/30'
    case 'unrestricted':
      return 'bg-error-600 text-white ring-2 ring-error-300 shadow-sm dark:bg-error-500 dark:ring-error-400/50'
    default:
      return 'bg-brand-50 text-brand-700 ring-1 ring-brand-100 dark:bg-brand-500/10 dark:text-brand-300 dark:ring-brand-500/20'
  }
}

export function securityModeCardClass(mode: string | undefined | null): string {
  const m = normalizeSecurityMode(mode, 'standard')
  switch (m) {
    case 'strict':
      return 'border-success-200 bg-success-50/40 dark:border-success-500/25 dark:bg-success-500/5'
    case 'unrestricted':
      return 'border-error-400 bg-error-50 dark:border-error-500/50 dark:bg-error-500/10 shadow-[inset_0_0_0_1px_rgba(239,68,68,0.25)]'
    default:
      return 'border-gray-200 bg-gray-50/60 dark:border-white/10 dark:bg-white/[0.03]'
  }
}

export function securityRiskLabel(level: string | undefined | null): string {
  switch (String(level ?? '').toLowerCase()) {
    case 'critical':
      return '极高风险'
    case 'high':
      return '高风险'
    case 'medium':
      return '中等风险'
    case 'low':
      return '较低风险'
    default:
      return ''
  }
}

export function buildSecurityProfilePayload(input: {
  mode: StdioSecurityMode
  filePathsText: string
  networkMode: NetworkAccessMode
  networkHostsText: string
  packageAllowlistText?: string
  allowSelfInstall: boolean | null
  note: string
}): SecurityProfile {
  const paths = input.filePathsText
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean)
  const hosts = input.networkHostsText
    .split(/[\n,]/)
    .map((l) => l.trim())
    .filter(Boolean)
  const packages = (input.packageAllowlistText ?? '')
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean)

  const profile: SecurityProfile = {
    mode: input.mode,
    fileAccess: {
      mode:
        input.mode === 'strict'
          ? 'allowlist'
          : input.mode === 'unrestricted'
            ? 'unrestricted'
            : paths.length > 0
              ? 'allowlist'
              : 'inherit',
      paths,
    },
    network: {
      mode: input.networkMode,
      hosts,
    },
    dependencyPolicy:
      input.mode === 'strict'
        ? 'declared_only'
        : input.mode === 'unrestricted'
          ? 'unrestricted'
          : 'inherit',
  }
  if (packages.length > 0) {
    profile.packageAllowlist = packages
  }
  if (input.allowSelfInstall !== null) {
    profile.allowSelfInstall = input.allowSelfInstall
  } else if (input.mode === 'strict') {
    profile.allowSelfInstall = false
  }
  if (input.note.trim() !== '') {
    profile.note = input.note.trim().slice(0, 300)
  }
  return profile
}

export function preflightReadyLabelEx(
  ready: boolean,
  stdioEnabled: boolean,
  commandAllowed: boolean,
  securityOk?: boolean,
  securityError?: string,
): string {
  if (!stdioEnabled) return '本地 stdio 已禁用'
  if (!commandAllowed) return '启动命令不被策略允许'
  if (securityOk === false) return securityError?.trim() || '安全策略未满足'
  return ready ? '依赖与安全策略已就绪' : '依赖未就绪'
}

export function preflightToneEx(
  ready: boolean,
  stdioEnabled: boolean,
  commandAllowed: boolean,
  securityOk?: boolean,
  riskLevel?: string,
): 'success' | 'warning' | 'error' {
  if (!stdioEnabled || !commandAllowed || securityOk === false) return 'error'
  if (String(riskLevel ?? '').toLowerCase() === 'critical') return 'error'
  return ready ? 'success' : 'warning'
}
