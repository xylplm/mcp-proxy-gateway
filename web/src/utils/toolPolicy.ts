import type { ToolPolicyRule, ToolPolicyRuleRequest, ToolRoutingStrategy } from '@/api/rules'

export type AutoRiskTagKey = 'payment' | 'delete' | 'write' | 'send'

export const TOOL_POLICY_RISK_TAG_PRESETS = ['写入', '删除', '发送', '支付'] as const

export const TOOL_POLICY_AUTO_RISK_TAGS: Array<{
  key: AutoRiskTagKey
  label: string
  description: string
}> = [
  { key: 'payment', label: '支付', description: '触发支付、退款、下单等动作' },
  { key: 'delete', label: '删除', description: '删除、清空或移除数据' },
  { key: 'write', label: '写入', description: '创建、更新、导入、执行或同步' },
  { key: 'send', label: '发送', description: '发送消息、通知、发布或 Webhook' },
]

const autoRiskTagKeys = new Set<AutoRiskTagKey>(TOOL_POLICY_AUTO_RISK_TAGS.map((tag) => tag.key))

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

export function normalizeIgnoredRiskTags(tags: string[]): AutoRiskTagKey[] {
  const seen = new Set<AutoRiskTagKey>()
  const out: AutoRiskTagKey[] = []
  for (const raw of tags) {
    const key = raw.trim().toLowerCase() as AutoRiskTagKey
    if (!autoRiskTagKeys.has(key) || seen.has(key)) continue
    seen.add(key)
    out.push(key)
  }
  return out
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
    ignoredRiskTags: normalizeIgnoredRiskTags(rule.ignoredRiskTags ?? []),
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
