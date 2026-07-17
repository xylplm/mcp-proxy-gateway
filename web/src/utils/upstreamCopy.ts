/**
 * 上游复制（克隆预填）纯函数。
 *
 * 复制走现有 Create 链路：新 UUID / 新 sortOrder / 不共享 tool_cache 与规则绑定。
 * 连接参数与凭证深拷贝，避免表单改动污染列表内存对象。
 *
 * 注意：本文件供 node:test 直接加载，避免 `@/` 值导入（仅允许 type-only 或相对路径）。
 */

import { normalizeTags } from './upstreamTags.ts'

/** 与后端 maxNameLen 对齐。 */
export const UPSTREAM_NAME_MAX_RUNES = 100

type TransportType = 'stdio' | 'sse' | 'streamable-http' | 'websocket' | 'openapi'

interface ConnParams {
  command?: string
  args?: string[]
  env?: Record<string, string>
  cwd?: string
  url?: string
  headers?: Record<string, string>
  baseUrl?: string
  docUrl?: string
  docContent?: string
  authType?: string
  authName?: string
  authValue?: string
  [key: string]: unknown
}

function asTransport(value: unknown): TransportType {
  switch (value) {
    case 'stdio':
    case 'sse':
    case 'streamable-http':
    case 'websocket':
    case 'openapi':
      return value
    default:
      return 'stdio'
  }
}

interface UpstreamRateLimits {
  enabled: boolean
  perSecond?: number
  perMinute?: number
  perHour?: number
  perDay?: number
  perWeek?: number
  perMonth?: number
  timezone?: string
}

interface UpstreamConfigLike {
  name: string
  tags?: string[]
  transport: string
  connParams: ConnParams
  credential?: string
  enabled: boolean
  autoSync: boolean
  rateLimits?: UpstreamRateLimits
}

interface UpstreamLike {
  id: string
  config: UpstreamConfigLike
}

/** 复制预填表单源（无 id/state 等运行时字段）。 */
export interface UpstreamCloneFormSource {
  name: string
  tags: string[]
  transport: TransportType
  connParams: ConnParams
  credential: string
  enabled: boolean
  autoSync: boolean
  rateLimits: UpstreamRateLimits
  /** 仅展示用，提交时不使用。 */
  sourceId: string
  sourceName: string
}

function emptyRateLimits(): UpstreamRateLimits {
  return {
    enabled: false,
    perSecond: 0,
    perMinute: 0,
    perHour: 0,
    perDay: 0,
    perWeek: 0,
    perMonth: 0,
    timezone: 'UTC',
  }
}

function runeLength(value: string): number {
  return [...value].length
}

function truncateRunes(value: string, max: number): string {
  if (max <= 0) return ''
  const chars = [...value]
  if (chars.length <= max) return value
  return chars.slice(0, max).join('')
}

function deepCloneJSON<T>(value: T): T {
  if (value === null || typeof value !== 'object') return value
  return JSON.parse(JSON.stringify(value)) as T
}

function cloneConnParams(params: ConnParams | null | undefined): ConnParams {
  if (params == null || typeof params !== 'object' || Array.isArray(params)) return {}
  try {
    return deepCloneJSON(params)
  } catch {
    // 循环引用等异常结构时退回浅拷贝安全子集，避免复制流程崩溃。
    return {}
  }
}

function cloneRateLimits(limits: UpstreamRateLimits | null | undefined): UpstreamRateLimits {
  if (limits == null || typeof limits !== 'object') return emptyRateLimits()
  const cloned = deepCloneJSON(limits)
  return {
    ...emptyRateLimits(),
    ...cloned,
    enabled: Boolean(cloned.enabled),
  }
}

/**
 * 生成不与 existingNames 冲突的副本名。
 * 序列：`{name} 副本` → `{name} 副本2` → …；超长时截断源名再拼接。
 */
export function suggestUniqueUpstreamName(
  sourceName: string,
  existingNames: readonly string[],
): string {
  const baseRaw = sourceName.trim() === '' ? '上游' : sourceName.trim()
  const occupied = new Set(
    existingNames.map((name) => name.trim().toLowerCase()).filter((name) => name !== ''),
  )

  const tryName = (candidate: string): string | null => {
    const name = truncateRunes(candidate.trim(), UPSTREAM_NAME_MAX_RUNES)
    if (name === '') return null
    if (occupied.has(name.toLowerCase())) return null
    return name
  }

  const withSuffix = (base: string, suffix: string): string => {
    const maxBase = Math.max(1, UPSTREAM_NAME_MAX_RUNES - runeLength(suffix))
    return `${truncateRunes(base, maxBase)}${suffix}`
  }

  const first = tryName(withSuffix(baseRaw, ' 副本'))
  if (first !== null) return first

  for (let i = 2; i <= 10_000; i += 1) {
    const candidate = tryName(withSuffix(baseRaw, ` 副本${i}`))
    if (candidate !== null) return candidate
  }

  // 极端兜底：时间戳后缀，保证可提交。
  const stamp = String(Date.now())
  const fallback = tryName(withSuffix(baseRaw, `-${stamp}`))
  if (fallback !== null) return fallback
  return truncateRunes(`上游-${stamp}`, UPSTREAM_NAME_MAX_RUNES)
}

/**
 * 从已有上游构建创建表单预填数据。
 * 不包含 id / state / sortOrder / timestamps。
 */
export function buildUpstreamCloneFormSource(
  source: UpstreamLike,
  name: string,
): UpstreamCloneFormSource {
  const cfg = source.config
  return {
    name: truncateRunes(name.trim(), UPSTREAM_NAME_MAX_RUNES),
    tags: normalizeTags(cfg.tags ?? []),
    transport: asTransport(cfg.transport),
    connParams: cloneConnParams(cfg.connParams),
    credential: typeof cfg.credential === 'string' ? cfg.credential : '',
    enabled: Boolean(cfg.enabled),
    autoSync: Boolean(cfg.autoSync),
    rateLimits: cloneRateLimits(cfg.rateLimits),
    sourceId: source.id,
    sourceName: cfg.name,
  }
}
