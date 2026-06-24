import { listUpstreamTools, type Upstream } from '@/api/upstreams'
import type { ToolDef } from '@/api/tools'
export {
  buildToolRulePreview,
  createOriginalNameMatcher,
  enabledUpstreamIDs,
  scopedEnabledUpstreamIDs,
  type ScopedRule,
  type ToolRulePreviewInput,
  type ToolRulePreviewItem,
  type ToolRulePreviewOptions,
  type ToolRulePreviewSummary,
} from '@/utils/rulePreviewCore'

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
