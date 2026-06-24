import type { RuleScopeType } from '@/api/rules'
import type { Upstream } from '@/api/upstreams'
import type { ToolDef } from '@/api/tools'

export interface ScopedRule {
  scopeType?: RuleScopeType
  upstreamIds?: string[]
}

export interface ToolRulePreviewInput extends ScopedRule {
  pattern: string
  isRegex: boolean
  enabled?: boolean
}

export interface ToolRulePreviewItem {
  key: string
  upstreamId: string
  upstreamName: string
  originalName: string
  exposedName: string
}

export interface ToolRulePreviewSummary {
  label: string
  items: ToolRulePreviewItem[]
  hiddenCount: number
  totalCount: number
}

export interface ToolRulePreviewOptions {
  limit?: number
  emptyLabel?: string
  disabledLabel?: string
  noEnabledScopeLabel?: string
  invalidPatternLabel?: string
  noHitLabel?: string
  hitLabel?: (count: number) => string
}

export function enabledUpstreamIDs(upstreams: Upstream[]): string[] {
  return upstreams.filter((up) => up.config.enabled).map((up) => up.id)
}

export function scopedEnabledUpstreamIDs(rule: ScopedRule, upstreams: Upstream[]): string[] {
  const enabledIDs = new Set(enabledUpstreamIDs(upstreams))
  const scopedIDs = (rule.scopeType ?? 'all') === 'all'
    ? upstreams.map((up) => up.id)
    : rule.upstreamIds ?? []
  return scopedIDs.filter((id) => enabledIDs.has(id))
}

export function createOriginalNameMatcher(
  pattern: string,
  isRegex: boolean,
): ((originalName: string) => boolean) | null {
  const normalizedPattern = pattern.trim()
  if (normalizedPattern === '') return null
  if (!isRegex) return (originalName: string) => originalName === normalizedPattern

  try {
    const re = new RegExp(`^(?:${normalizedPattern})$`)
    return (originalName: string) => re.test(originalName)
  } catch {
    return null
  }
}

export function buildToolRulePreview(
  rule: ToolRulePreviewInput,
  upstreams: Upstream[],
  toolsByUpstream: Record<string, ToolDef[]>,
  options: ToolRulePreviewOptions = {},
): ToolRulePreviewSummary {
  const limit = options.limit ?? 3
  if (rule.enabled === false) {
    return emptySummary(options.disabledLabel ?? '规则未启用')
  }
  if (rule.pattern.trim() === '') {
    return emptySummary(options.emptyLabel ?? '填写匹配模式后显示预计影响')
  }

  const scopedIDs = scopedEnabledUpstreamIDs(rule, upstreams)
  if (scopedIDs.length === 0) {
    return emptySummary(options.noEnabledScopeLabel ?? '作用范围内暂无启用上游')
  }

  const matcher = createOriginalNameMatcher(rule.pattern, rule.isRegex)
  if (matcher === null) {
    return emptySummary(options.invalidPatternLabel ?? '匹配模式暂不可用')
  }

  const upstreamNames = new Map(upstreams.map((up) => [up.id, up.config.name || up.id]))
  const hits: ToolRulePreviewItem[] = []
  for (const upstreamID of scopedIDs) {
    const tools = toolsByUpstream[upstreamID] ?? []
    for (const tool of tools) {
      if (!matcher(tool.originalName)) continue
      hits.push({
        key: `${upstreamID}:${tool.originalName}:${hits.length}`,
        upstreamId: upstreamID,
        upstreamName: upstreamNames.get(upstreamID) ?? upstreamID,
        originalName: tool.originalName,
        exposedName: tool.name,
      })
    }
  }

  if (hits.length === 0) {
    return emptySummary(options.noHitLabel ?? '当前缓存未命中工具')
  }
  return {
    label: options.hitLabel?.(hits.length) ?? `当前缓存命中 ${hits.length} 个工具`,
    items: hits.slice(0, Math.max(0, limit)),
    hiddenCount: Math.max(0, hits.length - Math.max(0, limit)),
    totalCount: hits.length,
  }
}

function emptySummary(label: string): ToolRulePreviewSummary {
  return {
    label,
    items: [],
    hiddenCount: 0,
    totalCount: 0,
  }
}
