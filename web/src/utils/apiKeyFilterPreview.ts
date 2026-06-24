import type { APIKeyFilter } from '@/api/apikeys'
import type { ToolDetail } from '@/api/tools'
import { createOriginalNameMatcher } from './rulePreviewCore.ts'

export interface APIKeyFilterPreviewInput {
  pattern: string
  isRegex: boolean
  enabled?: boolean
}

export interface APIKeyFilterPreviewItem {
  key: string
  upstreamName: string
  originalName: string
  exposedName: string
}

export interface APIKeyFilterPreviewSummary {
  label: string
  items: APIKeyFilterPreviewItem[]
  hiddenCount: number
  totalCount: number
}

export interface APIKeyFilterPreviewOptions {
  limit?: number
  emptyLabel?: string
  disabledLabel?: string
  unavailableLabel?: string
  invalidPatternLabel?: string
  noHitLabel?: string
  hitLabel?: (count: number) => string
}

export function buildAPIKeyFilterPreview(
  rule: APIKeyFilter | APIKeyFilterPreviewInput,
  toolDetails: ToolDetail[],
  ready: boolean,
  options: APIKeyFilterPreviewOptions = {},
): APIKeyFilterPreviewSummary {
  const limit = Math.max(0, options.limit ?? 3)
  if (rule.enabled === false) {
    return emptySummary(options.disabledLabel ?? '规则未启用')
  }
  if (rule.pattern.trim() === '') {
    return emptySummary(options.emptyLabel ?? '填写匹配模式后显示预计影响')
  }
  if (!ready) {
    return emptySummary(options.unavailableLabel ?? '当前聚合来源暂不可用')
  }

  const matcher = createOriginalNameMatcher(rule.pattern, rule.isRegex)
  if (matcher === null) {
    return emptySummary(options.invalidPatternLabel ?? '匹配模式暂不可用')
  }

  const hits: APIKeyFilterPreviewItem[] = []
  for (const detail of toolDetails) {
    for (const source of detail.sources ?? []) {
      if (!matcher(source.originalName)) continue
      hits.push({
        key: `${source.upstreamId}:${source.originalName}:${hits.length}`,
        upstreamName: source.upstreamName,
        originalName: source.originalName,
        exposedName: detail.tool.name,
      })
    }
  }

  if (hits.length === 0) {
    return emptySummary(options.noHitLabel ?? '当前聚合来源未命中')
  }
  return {
    label: options.hitLabel?.(hits.length) ?? `当前聚合来源命中 ${hits.length} 个`,
    items: hits.slice(0, limit),
    hiddenCount: Math.max(0, hits.length - limit),
    totalCount: hits.length,
  }
}

function emptySummary(label: string): APIKeyFilterPreviewSummary {
  return {
    label,
    items: [],
    hiddenCount: 0,
    totalCount: 0,
  }
}
