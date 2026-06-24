import type { ToolDetail } from '@/api/tools'

export type ToolRiskLevel = 'high' | 'medium' | 'low'

export interface ToolRiskTag {
  key: string
  label: string
  level: ToolRiskLevel
}

const riskRules: Array<{
  key: string
  label: string
  level: ToolRiskLevel
  patterns: RegExp[]
}> = [
  {
    key: 'payment',
    label: '支付',
    level: 'high',
    patterns: [
      /\b(pay|payment|charge|refund|invoice|checkout|billing|purchase|paid)\b/i,
      /支付|付款|扣款|退款|账单|发票|购买|付费/,
    ],
  },
  {
    key: 'delete',
    label: '删除',
    level: 'high',
    patterns: [/\b(delete|remove|drop|destroy|truncate|erase|purge|clear)\b/i, /删除|移除|清空|销毁/],
  },
  {
    key: 'write',
    label: '写入',
    level: 'medium',
    patterns: [
      /\b(write|create|update|patch|edit|modify|rename|move|upload|save|insert|upsert|commit|merge|deploy|execute|run)\b/i,
      /写入|创建|新增|更新|修改|编辑|重命名|移动|上传|保存|提交|部署|执行/,
    ],
  },
  {
    key: 'send',
    label: '发送',
    level: 'medium',
    patterns: [/(^|[^a-z0-9])(send|email|notify|publish|post|sms|webhook)(?=$|[^a-z0-9])/i, /发送|短信|推送/],
  },
]

export function toolRiskTags(detail: ToolDetail): ToolRiskTag[] {
  const text = searchableText(detail)
  const tags: ToolRiskTag[] = []
  for (const rule of riskRules) {
    if (rule.patterns.some((pattern) => pattern.test(text))) {
      tags.push({ key: rule.key, label: rule.label, level: rule.level })
    }
  }
  return tags
}

export function highestRiskLevel(tags: ToolRiskTag[]): ToolRiskLevel | null {
  if (tags.some((tag) => tag.level === 'high')) return 'high'
  if (tags.some((tag) => tag.level === 'medium')) return 'medium'
  if (tags.some((tag) => tag.level === 'low')) return 'low'
  return null
}

function searchableText(detail: ToolDetail): string {
  return [
    detail.tool.name,
    detail.tool.originalName,
    detail.tool.description,
    ...(detail.sources ?? []).flatMap((source) => [
      source.originalName,
      source.description,
    ]),
  ]
    .filter(Boolean)
    .join(' ')
}
