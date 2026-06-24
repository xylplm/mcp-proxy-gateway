import type { ToolPolicyRule, ToolPolicyRuleRequest, ToolRoutingStrategy } from '@/api/rules'

export const TOOL_POLICY_RISK_TAG_PRESETS = ['写入', '删除', '发送', '支付'] as const

export function normalizePolicyRiskTags(tags: string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const raw of tags) {
    const tag = raw.trim()
    if (tag === '' || seen.has(tag)) continue
    seen.add(tag)
    out.push(tag)
  }
  return out.slice(0, 8)
}

export function toolPolicyToRequest(rule: ToolPolicyRule | ToolPolicyRuleRequest): ToolPolicyRuleRequest {
  return {
    pattern: rule.pattern,
    isRegex: rule.isRegex,
    enabled: rule.enabled,
    sortOrder: rule.sortOrder,
    routingStrategy: normalizeRoutingStrategy(rule.routingStrategy),
    cacheEnabled: rule.cacheEnabled,
    cacheTtlSeconds: rule.cacheEnabled ? Math.max(1, Number(rule.cacheTtlSeconds ?? 60)) : 0,
    riskTags: normalizePolicyRiskTags(rule.riskTags ?? []),
  }
}

export function normalizeRoutingStrategy(strategy?: ToolRoutingStrategy): ToolRoutingStrategy {
  if (strategy === 'priority_fill' || strategy === 'round_robin') return strategy
  return ''
}

export function routingStrategyLabel(strategy?: ToolRoutingStrategy): string {
  if (strategy === 'priority_fill') return '优先顺序'
  if (strategy === 'round_robin') return '轮询'
  return '不覆盖'
}

export function cachePolicyLabel(rule: Pick<ToolPolicyRule, 'cacheEnabled' | 'cacheTtlSeconds'>): string {
  if (!rule.cacheEnabled) return '未启用缓存'
  return `缓存 ${rule.cacheTtlSeconds ?? 0} 秒`
}
