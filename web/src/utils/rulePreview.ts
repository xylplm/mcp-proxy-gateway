import type { RuleScopeType } from '@/api/rules'
import { listUpstreamTools, type Upstream } from '@/api/upstreams'
import type { ToolDef } from '@/api/tools'

export interface ScopedRule {
  scopeType?: RuleScopeType
  upstreamIds?: string[]
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

export async function loadCachedToolsForEnabledUpstreams(
  upstreams: Upstream[],
): Promise<Record<string, ToolDef[]>> {
  const previewUpstreams = upstreams.filter((up) => up.config.enabled)
  if (previewUpstreams.length === 0) return {}

  const entries = await Promise.allSettled(
    previewUpstreams.map(async (up) => {
      const result = await listUpstreamTools(up.id, { ensure: false })
      return [up.id, result.tools] as const
    }),
  )

  const next: Record<string, ToolDef[]> = {}
  for (const entry of entries) {
    if (entry.status !== 'fulfilled') continue
    const [upstreamID, tools] = entry.value
    next[upstreamID] = tools
  }
  return next
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
